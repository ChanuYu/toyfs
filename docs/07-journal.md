# toyfs — Journal

이 문서는 `toyfs`의 **메타데이터 저널링** 구조와 동작을 정의한다.
crash recovery의 핵심 메커니즘이며, 06번 block cache의 `JournalLocked` /
`FrozenData` 필드가 여기서 실제로 사용된다.

핵심 원칙:
- **메타데이터만 journal**. 데이터 블록의 *내용*은 journal에 안 들어감
- **ordered 모드**: 메타데이터 commit 전에 관련 데이터가 본위치에 도달
- **running 1개 + committing 0~1개**. 동시에 두 commit 진행 안 함
- ops 단위로 transaction에 묶임. 한 ops의 모든 메타데이터 변경은 같은 transaction

## 1. 기본 단위

| 항목 | 값 |
|------|----|
| journal 영역 | superblock의 `journal_start` ~ `journal_start + journal_blocks` |
| journal 크기 | 1 MiB (= 256 블록), 01번 문서에서 결정 |
| record 크기 | 4 KiB (= 1 블록) |
| 최대 transaction 메타데이터 블록 수 | 203 (descriptor 1개 한도) |
| commit 트리거 | 5초 / fsync / journal 절반 도달 |
| 모드 | ordered (메타데이터 journal + 데이터 본위치 ordering) |

## 2. journal 영역 레이아웃

```
journal 영역 (256 블록):
[블록 0]   journal superblock
[블록 1]   ─┐
[블록 2]    │ circular log area (255 블록)
...         │ (descriptor / metadata / commit / revocation 레코드가 섞임)
[블록 255] ─┘
```

circular log이므로 head와 tail 포인터가 영역을 한 바퀴 돈다.

## 3. journal superblock (블록 0)

journal 영역의 첫 블록. fs superblock과 별개.

```
struct toyfs_journal_sb {
    u32  magic;           // "TJSB" = 0x42534A54
    u32  version;         // 1
    u32  block_size;      // 4096
    u32  total_blocks;    // 256
    u64  head_block;      // 다음 record가 들어갈 위치 (0~total_blocks-1)
    u64  tail_block;      // 가장 오래된 미체크포인트 transaction 시작 위치
    u64  next_txn_id;     // 다음 transaction에 부여할 ID (단조 증가)
    u32  flags;
    u32  checksum;        // 위 필드들에 대한 CRC32
    u8   pad[ /* fill to 4096 */ ];
};
```

`head_block == tail_block` && head/tail이 한 바퀴 안 돌았으면 = journal 비어있음.
journal full 조건은 head가 tail에 따라잡으려 할 때 (한 바퀴 차이).

mount 시 이 블록을 읽어 head/tail을 알고, replay 또는 정상 시작.

## 4. 4종 record 포맷

모든 record는 1 블록(4 KiB). 첫 12 byte가 **공통 헤더**:

```
struct toyfs_journal_record_header {
    u32  magic;           // 종류별 다름
    u32  txn_id;          // 이 record가 속한 transaction
    u32  type;             // 0=descriptor, 1=metadata, 2=commit, 3=revocation
};
```

### 4.1 Descriptor record (type=0)

transaction의 *목차*. 뒤따라오는 K개의 metadata block이 본위치 어디로 갈지 매핑.

```
struct toyfs_descriptor {
    header  hdr;             // magic="TJDS"=0x53444A54, type=0  (12 byte)
    u32     count;           // 뒤따라오는 metadata block 수 (K, ≤203)
    u32     reserved;
    struct {
        u64 target_block;    // 본위치 블록 번호
        u32 flags;            // bit 0 = TOYFS_DESC_ESCAPED (4.2.2 참고)
        u8  original_first4[4];  // escape된 경우 원래 첫 4 byte 보관. 아니면 0
    } entries[203];           // 203 × 20 byte = 4060 byte
    u8      pad[16];           // 4096 - 12 - 8 - 4060 = 16 byte 패딩
};
```

descriptor 1개 = 1 transaction (1단계 단순화). 203 메타데이터 블록을 넘는 transaction은 발생하지 않도록 ops 레이어에서 보장 (실제로 mkdir/unlink/rename 같은 단일 ops가 203개 메타데이터를 건드릴 일 없음).

`original_first4` 필드 도입 이유: escape 처리 시 메타데이터 블록의 첫 4 byte를 0으로 마스킹하는데, 본위치에 *고유 magic이 없는* 블록(bitmap, inode table, dentry 데이터)은 원래 첫 4 byte 값을 알 방법이 없으므로 descriptor entry에 보관해야 한다. 자세한 건 4.2.1, 9.3 참고.

ext4 jbd2의 entry는 12 byte로 더 작은데, 그건 블록 번호 32-bit 가정 + escape 시 보존 처리가 약간 다르기 때문. 우리는 64-bit 블록 + original_first4 보존으로 학습 명확성 우선.

### 4.2 Metadata record (type=1)

본위치 메타데이터 블록의 새 내용을 그대로 복사한 4 KiB. **헤더 없이** 4096 byte 통째.

```
struct toyfs_metadata_record {
    u8  data[4096];   // 본위치 블록의 사본 그대로
                      // 단, 첫 4 byte가 record-type magic 중 하나(TJDS/TJMD/TJCM/TJRV)와
                      // 우연히 일치하면 escape 처리되어 0x00000000으로 마스킹됨.
                      // descriptor entry의 flags에 ESCAPE 비트가 set되고, replay 시 복원.
};
```

### 4.2.1 escape 트릭

journal 영역을 *순차로 스캔*하면서 record 종류를 첫 4 byte로 구분하는 방식이라, metadata block의 첫 4 byte가 우연히 magic과 같으면 scan이 그걸 descriptor/commit/revocation으로 오인할 위험이 있다. ext4 jbd2의 표준 해결책을 그대로 채택.

**commit 시점**:
```
for blk in transaction.metadata_blocks:
    payload = cache.read(blk)            # 4096 byte
    flags = 0
    first4 = payload[0:4]
    if first4 == "TJDS" || first4 == "TJMD" || first4 == "TJCM" || first4 == "TJRV":
        payload[0:4] = 0x00000000
        flags |= TOYFS_DESC_ESCAPED
    descriptor.entries.append({target_block: blk, flags: flags})
    journal_write(payload)
```

**replay 시점**:
```
for entry in descriptor.entries:
    payload = journal_read(metadata_position)
    if entry.flags & TOYFS_DESC_ESCAPED:
        payload[0:4] = entry.original_first4   # commit 때 보관해둔 4 byte
    write_to_disk(entry.target_block, payload)
```

descriptor entry의 `original_first4` 필드(4.1절)에 commit 시점 첫 4 byte를 보관해두므로, 본위치 블록 종류를 알 필요 없이 복원 가능. 자기 magic이 있는 메타데이터(superblock, extent index 등)와 magic 없는 임의 데이터 메타데이터(bitmap, inode table, dentry) 모두 같은 메커니즘으로 처리.

### 4.2.2 descriptor entry의 ESCAPE 플래그

4.1절의 descriptor entry flags에 비트 추가:

```
TOYFS_DESC_ESCAPED  = 1 << 0   // 이 metadata block의 첫 4 byte는 원래 magic이었음.
                                // journal write 시 0으로 마스킹됨 → replay 시 복원 필요
```

### 4.2.3 record 종류 식별

scan 단계는 *첫 4 byte의 magic* + *이전 record 타입과의 위치 관계*로 종류를 결정:

- 첫 4 byte == TJDS → descriptor (transaction 시작)
- descriptor 직후의 K개 블록 → metadata (count는 descriptor에서 알 수 있음)
- 그 다음에 TJRV → revocation
- 그 다음에 TJCM → commit (transaction 끝)
- 그 외 magic 또는 위치 어긋남 → 미완 transaction. 무시하고 스캔 종료

metadata block은 *위치로 식별*되며, 첫 4 byte가 magic이 아닌 임의 값. escape 비트 덕에 "magic과 우연히 같은 값"은 시저장에서 제거되어 있음.

### 4.3 Commit record (type=2)

transaction이 완전하다는 마커.

```
struct toyfs_commit_record {
    header  hdr;          // magic="TJCM"=0x4D434A54, type=2
    u64     timestamp;    // commit 시각
    u32     txn_checksum; // 이 transaction의 모든 record(=descriptor + metadata*K + revocation*M)에 대한 CRC32
    u32     reserved;
    u8      pad[4068];
};
```

`txn_checksum`은 replay 시 *부분 write 감지*에 결정적. checksum 안 맞으면 그 transaction 무시.

### 4.4 Revocation record (type=3)

"이 블록은 더 이상 메타데이터로 대우하지 마라"는 마킹.

```
struct toyfs_revocation_record {
    header  hdr;          // magic="TJRV"=0x56524A54, type=3  (12 byte)
    u32     count;
    u32     reserved;
    u64     blocks[509];  // revoke된 본위치 블록 번호. 509 × 8 = 4072 byte
    u8      pad[4];        // 4096 - 12 - 8 - 4072 = 4 byte 패딩
};
```

revocation이 필요한 시나리오는 8.4절 참고.

## 5. transaction 라이프사이클

### 5.1 in-memory 표현

```go
type Transaction struct {
    ID          uint64
    State       TxnState     // Running / Committing / Committed / Checkpointed

    // 묶인 메타데이터 (06번 block cache의 슬롯들)
    MetaBlocks  []uint64     // 본위치 블록 번호 list

    // 옵션 B: 영향받은 inode 번호 list (commit 시 데이터 flush 대상)
    DirtyInodes map[uint32]bool

    // revocation 대상
    Revoked     []uint64

    // journal 영역에서 차지한 위치
    JournalStart uint64       // 이 transaction의 첫 record가 저장된 journal 블록
    JournalEnd   uint64       // 마지막 record + 1

    StartedAt    time.Time
}

type TxnState int
const (
    TxnRunning TxnState = iota
    TxnCommitting
    TxnCommitted
    TxnCheckpointed
)

type Journal struct {
    Running     *Transaction       // 1개 (nil인 적 없음, mount 직후 빈 transaction 생성)
    Committing  *Transaction       // 0개 또는 1개
    Checkpointing []*Transaction   // 0~여러 개

    Superblock  *toyfs_journal_sb  // in-memory 사본
    Cache       *BlockCache        // 06번
    Device      *BlockDevice
}
```

### 5.2 ops가 transaction에 메타데이터 추가

```go
func (j *Journal) AddMetadata(blockNum uint64, inodeNum uint32) {
    // 1. block cache에서 그 블록 Get은 caller가 이미 했음
    // 2. running transaction에 등록
    if !contains(j.Running.MetaBlocks, blockNum):
        j.Running.MetaBlocks = append(..., blockNum)
    if inodeNum != 0:
        j.Running.DirtyInodes[inodeNum] = true
}

func (j *Journal) AddRevocation(blockNum uint64) {
    j.Running.Revoked = append(..., blockNum)
}
```

caller(예: ops 레이어)는 메타데이터 cache 슬롯을 수정한 *직후* `AddMetadata`를 호출. 한 ops의 모든 메타데이터 변경은 같은 running에 누적.

### 5.3 transaction commit (running → committing → committed)

다음 트리거 중 하나가 발동:
- 5초 timer
- caller의 fsync
- journal 절반 도달 (= head가 tail로부터 ~128 블록 차이)

commit 절차:

```
func (j *Journal) Commit() {
    // ---- Phase 0: running을 committing으로 전환 ----
    j.Committing = j.Running
    j.Committing.State = TxnCommitting
    j.Running = newTransaction(j.NextTxnID())   // 새 빈 transaction

    T := j.Committing

    // ---- Phase 1: ordered 강제 — 데이터 본위치 flush ----
    for inode in T.DirtyInodes:
        for each dirty data block of inode:
            cache.Flush(dataBlock)              // 본위치 write + dirty=false
    cache.Sync()                                 // 디스크 sync

    // ---- Phase 2: 메타데이터 cache 슬롯 freeze ----
    for blockNum in T.MetaBlocks:
        b := cache.Get(blockNum)
        cache.JournalLock(b)                     // 본위치 flush 금지
        cache.FreezeForCommit(b)                 // FrozenData에 t_commit 사본
        // Get/Put 균형: defer cache.Put(b)

    // ---- Phase 3: journal 영역에 write (escape 처리 포함) ----
    descriptor := newDescriptor(T.ID)
    payloads := []
    for blockNum in T.MetaBlocks:
        b := cache.Get(blockNum)
        payload := copy(b.FrozenData)              // 4096 byte 복사
        flags := 0
        original := [4]byte{0, 0, 0, 0}
        first4 := payload[0:4]
        if first4 == "TJDS" || first4 == "TJMD" || first4 == "TJCM" || first4 == "TJRV":
            original = first4                       // 원래 첫 4 byte 보관
            payload[0:4] = 0x00000000               // 마스킹
            flags |= TOYFS_DESC_ESCAPED
        descriptor.entries.append({
            target:           blockNum,
            flags:            flags,
            original_first4:  original,
        })
        payloads.append(payload)

    journalWrite(j.head, descriptor)
    j.head = next(j.head)

    for payload in payloads:
        journalWrite(j.head, payload)              // 4096 byte 통째 (헤더 없음)
        j.head = next(j.head)

    if len(T.Revoked) > 0:
        revocation := buildRevocationRecord(T.Revoked)
        journalWrite(j.head, revocation)
        j.head = next(j.head)

    cache.Sync()                                 // descriptor + metadata + revocation이 디스크 도달

    // ---- Phase 4: commit record write ----
    commit := buildCommitRecord(T)
    journalWrite(j.head, commit)
    j.head = next(j.head)
    cache.Sync()                                 // commit record 디스크 도달

    // ---- Phase 5: 후처리 ----
    T.State = TxnCommitted
    j.Committing = nil
    j.Checkpointing = append(..., T)

    // FrozenData 해제 (cache의 현재 Data가 곧 최신 commit 반영본)
    for blockNum in T.MetaBlocks:
        b := cache.Get(blockNum)
        cache.ReleaseFrozen(b)
        // JournalLocked는 checkpoint까지 유지 (Phase 6에서 해제)

    // journal superblock 갱신
    j.Superblock.head_block = j.head
    writeJournalSuperblock()
    cache.Sync()

    // ---- Phase 6: caller에게 ack (fsync 호출자가 있으면 여기서 응답) ----
}
```

이 시점부터 transaction은 *살아남음 보장*. crash 나도 replay로 복구.

### 5.4 checkpoint (committed → checkpointed)

background flusher가 시간 두고 처리:

```
func (j *Journal) Checkpoint(T *Transaction) {
    // 1. 메타데이터 본위치 flush
    for blockNum in T.MetaBlocks:
        b := cache.Get(blockNum)
        cache.JournalUnlock(b)                   // 이제 본위치 flush 가능
        cache.Flush(b)                           // 본위치 write
    cache.Sync()

    // 2. tail 갱신
    j.Superblock.tail_block = T.JournalEnd
    writeJournalSuperblock()
    cache.Sync()

    // 3. transaction 자료구조 해제
    T.State = TxnCheckpointed
    remove T from j.Checkpointing
}
```

여러 transaction의 checkpoint 순서는 **txn_id 오름차순**. 옛 transaction부터 처리해야 tail 진행 가능.

> 단순화: 1단계는 매 commit 직후 즉시 checkpoint하는 정책으로 시작. ext4는 batch하지만 우리는 학습용으로 매번 처리해도 OK. 트레이드오프: 본위치 write 자주 발생 (= write coalescing 효과 작음) vs 코드 단순. 추후 batch로 발전 가능.

## 6. ordered 강제 — 옵션 B 상세

5.3 Phase 1의 핵심: **transaction에 묶인 inode들의 *모든 dirty data*를 본위치에 먼저 flush.**

### 6.1 어떤 데이터가 flush 대상인가

`T.DirtyInodes`에 등록된 각 inode에 대해:
- 그 inode의 extent를 따라가며 모든 데이터 블록 식별
- block cache에서 dirty인 슬롯만 flush

이 과정에서 *해당 inode와 무관한* 데이터 블록은 건드리지 않음. 다른 inode의 dirty data는 자기 transaction commit 시점에 처리.

### 6.2 무엇이 inode를 dirty로 등록하는가

ops 레이어가 inode 메타데이터(mode, size, mtime, extent 등)를 변경할 때마다:
1. inode 슬롯 cache 슬롯에 `MarkDirty`
2. journal에 `AddMetadata(inode_block, inode_num)`

여기서 `inode_num`이 transaction의 `DirtyInodes`에 등록됨.

**toyfs는 모든 write가 mtime 갱신을 동반하는 정책**(앞선 합의)이라, 데이터 write가 일어나는 모든 inode는 자동으로 transaction에 등록됨. 따라서 page cache의 dirty data 수명은 사실상 **≤ 5초** (다음 commit 시 강제 flush).

### 6.3 ext4의 lazy mtime 최적화는?

본 1단계에서는 *학습 목표 밖*. 다음 프로젝트에서 추가:
- in-place overwrite의 mtime 갱신을 lazy하게 처리
- 일부 데이터 write가 transaction을 만들지 않도록 → page cache 수명 늘어남
- 트레이드오프: 더 복잡한 분기, 일부 시나리오에서 mtime이 지연 보일 수 있음

## 7. 데드락과 transaction 크기 제한

ops가 transaction에 메타데이터를 너무 많이 추가하면(>203 블록) descriptor가 부족함. 1단계 정책:

- ops 단위는 *항상 203 블록 이하* 메타데이터를 건드림 (mkdir/unlink/rename 등 모두 ~10 블록 이하)
- ops가 시작될 때 running이 너무 차 있으면 (예: 240 블록 이상) → 새 ops 시작 전에 자동 commit 트리거

이 메커니즘은 ext4의 `start_this_handle` / `j_max_transaction_buffers`와 같은 패턴.

## 8. revocation — 예시 시나리오

revocation의 정확한 사용 케이스:

### 8.1 시나리오

1. 시각 t1: 디렉토리 D의 dentry 블록 B 추가. journal에 transaction T1으로 기록 (B의 새 내용)
2. 시각 t2: T1 commit, journal에 살아있음. 본위치 checkpoint *전*
3. 시각 t3: D 안의 모든 entry unlink → B free
4. 시각 t4: 일반 파일 F의 새 데이터 블록으로 *같은* B 재할당
5. 시각 t5: F에 사용자 데이터 write → cache.Flush로 B의 본위치에 사용자 데이터 도달 (메타데이터 아님, 본위치 직접)
6. 시각 t6: crash

다음 mount의 replay:
- T1이 journal에 살아있음 (checkpoint 안 됨)
- T1의 metadata record가 "B의 내용은 옛 dentry"라고 우김 → 사용자 데이터를 덮어씌움 → **데이터 손상**

### 8.2 해결

t3에서 블록 B를 free할 때 **revocation record**를 새 transaction에 추가:
- "B는 더 이상 메타데이터 아님. 이전 journal entry 무시"

replay 시 revocation 테이블 먼저 수집 → 해당 블록의 metadata record는 skip.

### 8.3 toyfs의 정책

다음 시점에 revocation 자동 추가:
- 메타데이터 블록(extent index, dentry, bitmap이 가리키는 디렉토리 데이터 블록 등)이 free될 때
- 1단계는 *bitmap 블록* / *inode table 블록* / *journal superblock* 자체는 절대 free되지 않으므로 revocation 대상 아님
- 실제 revocation 대상: **extent index 블록, 디렉토리 데이터 블록**

caller(extent free, rmdir 등)는 free 시점에 `journal.AddRevocation(blockNum)` 호출.

## 9. replay 알고리즘 (mount 시)

mount 시 fs superblock의 `TOYFS_SB_CLEAN == 0`이면 비정상 unmount. journal replay 호출.

### 9.1 Pass 1: scan

```
scan_results := []
pos := journal_sb.tail_block
while pos != journal_sb.head_block:
    record := readRecord(pos)
    if record.type == descriptor:
        txn_start := pos
        K := record.count
        target_blocks := record.entries[:K]
        pos = next(pos)

        // 다음 K개 record가 metadata
        metadata_records := []
        for i in 0..K-1:
            md := readRecord(pos)
            if md.type != metadata:
                fail "incomplete transaction"
            metadata_records = append(.., md)
            pos = next(pos)

        // 그 다음에 revocation 또는 commit
        revoked := []
        next_record := readRecord(pos)
        if next_record.type == revocation:
            revoked = next_record.blocks[:next_record.count]
            pos = next(pos)
            next_record = readRecord(pos)

        if next_record.type == commit:
            if verifyChecksum(next_record, descriptor, metadata_records, revoked):
                scan_results = append(.., (descriptor, metadata_records, revoked, next_record))
            // checksum 깨지면 무시 (= 부분 write로 간주)
            pos = next(pos)
        else:
            // commit 못 만남 = 미완 transaction. 무시하고 종료
            break

    else:
        // 예상치 못한 record. 끝
        break
```

### 9.2 Pass 2: revocation 테이블 수집

scan_results에서 모든 revoked block을 모음. 단, **transaction 순서에 유의**: 후속 transaction에서 같은 블록이 다시 메타데이터로 등록됐으면 revocation 무효.

```
revocation_table := map[blockNum]uint64   // blockNum → revoked at txn_id
for txn in scan_results:                  // txn_id 오름차순
    for blk in txn.revoked:
        revocation_table[blk] = txn.txn_id
    // metadata block은 revocation을 *덮어씀* (이후 다시 메타로 등록됨)
    for md_target in txn.descriptor.targets:
        if md_target in revocation_table:
            if revocation_table[md_target] < txn.txn_id:
                delete revocation_table[md_target]
```

### 9.3 Pass 3: replay

```
for txn in scan_results:                  // txn_id 오름차순
    for i, entry in enumerate(txn.descriptor.entries):
        target := entry.target_block
        if target in revocation_table && revocation_table[target] >= txn.txn_id:
            continue   // skip

        payload := txn.metadata_records[i]      // 4096 byte 그대로
        if entry.flags & TOYFS_DESC_ESCAPED:
            payload[0:4] = entry.original_first4  // commit 때 보관해둔 4 byte로 복원
        write_to_disk(target, payload)
```

descriptor entry에 `original_first4` 필드(4.1절)를 두었기 때문에 replay 로직이 단순. 본위치 블록의 종류(superblock, extent index, bitmap, inode table 등)에 따른 분기 없음.

> escape 처리는 모든 메타데이터 블록에 일률적으로 적용. 첫 4 byte가 record-type magic 4종(TJDS/TJMD/TJCM/TJRV) 중 하나와 같으면 escape 비트 set + original_first4 저장, 아니면 둘 다 0.

### 9.4 Pass 4: 마무리

```
// journal 영역 클리어
journal_sb.head_block = 0
journal_sb.tail_block = 0
writeJournalSuperblock()
cache.Sync()

// fs superblock의 CLEAN 비트 set
fs_sb.flags |= TOYFS_SB_CLEAN
writeFSSuperblock()
cache.Sync()
```

이 시점부터 정상 mount.

## 10. invariant

mount 중:
1. running transaction은 항상 1개 존재
2. committing은 0 또는 1개
3. checkpointing은 *임의 개수*
4. journal head/tail이 한 바퀴 안 돌아 head==tail이면 비어있음
5. T의 모든 metadata block의 cache 슬롯은 T가 commit 진행 중이면 `JournalLocked == true`
6. T의 모든 inode의 데이터 블록은 T가 commit 시작 시점에 본위치에 도달
7. revoked 블록은 같은 transaction이나 후속 transaction에서 메타데이터로 등록되지 않음

unmount 직후:
8. running, committing, checkpointing 모두 빈 상태
9. journal head == tail
10. fs superblock의 CLEAN == 1

## 11. Testing

### 11.1 Unit test

record 직렬화:
- 4종 record 모두 marshal/unmarshal round-trip
- descriptor entry 203개 한도 검증
- revocation entry 509개 한도 검증
- checksum 검증 (정상/변조)

transaction 라이프사이클:
- AddMetadata 호출이 running에 정확히 누적
- DirtyInodes 등록 정확
- commit 후 running 새로 생성, committing이 채워짐
- commit 절차 단계별 호출 순서 검증 (Phase 1 → 2 → 3 → 4)
- frozen 사본: commit 진행 중 cache.MarkDirty 호출 시 `FrozenData`에 t_commit 내용 보존

ordered 강제:
- T.DirtyInodes의 데이터가 commit 전 본위치 flush되는지 (mock cache로 호출 검증)
- 데이터 flush 실패 시 commit 중단

revocation:
- revocation record 작성 → replay 시 해당 블록 skip
- revocation 후 같은 블록이 후속 transaction에서 메타로 등록되면 → replay 시 새 메타로 적용

replay:
- 정상 commit된 transaction → 본위치에 정확히 적용
- 미완 transaction (commit record 없음) → 무시
- checksum 깨진 transaction → 무시
- 여러 transaction 순차 replay → 마지막 commit된 시점 상태와 동일
- escape 처리: 첫 4 byte가 magic과 일치하는 메타데이터 블록 → escape 비트 set + original_first4 저장 → replay 시 정확 복원
- escape 처리: 첫 4 byte가 magic과 일치하지 않는 일반 케이스 → escape 비트 0, original_first4 = 0

### 11.2 Integration test (4단계 이후)
- FUSE mount 위에서 mkdir/touch/write/rename 등 후 unmount → 다시 mount → journal 비어있음 + 데이터 일치
- 미체크포인트 transaction이 있는 상태에서 정상 unmount → 모두 checkpoint된 후 종료

### 11.3 Crash recovery (5단계)
- mkdir 도중 process kill (Phase 0~3 사이) → 다음 mount의 replay 후 mkdir이 *완전히 일어났거나 완전히 안 일어났거나*. 부분 상태 없음
- write 도중 process kill (Phase 1 데이터 flush 도중) → 데이터 일부만 본위치 → 메타데이터는 commit 안 됐으므로 옛 inode 상태 → 일관 유지
- 여러 transaction 누적 후 kill → replay로 모두 적용되거나, commit 안 된 마지막 것만 사라짐

### 11.4 Stress
- 1000회 mkdir + unlink 반복 → journal head/tail circular 동작 검증, full 안 됨
- transaction 203 블록 한도 근접 시 자동 commit 트리거

## 12. ext4와의 차이 (학습 참고)

| 항목 | ext4 jbd2 | toyfs |
|---|---|---|
| transaction 동시성 | running 1 + committing 1 (병렬) | 동일 |
| 데이터 모드 | data=ordered (기본) | ordered (선택지 없음) |
| descriptor entry 수 | ~340 (12 byte entry) | 203 (20 byte entry, original_first4 4 byte 포함) |
| frozen 사본 (b_frozen_data) | 있음 | 있음 (단순 형태) |
| metadata escape | magic 충돌 시 escape 비트 | 동일 (escape 비트 + original_first4 보관) |
| metadata record 헤더 | 없음 (4096 byte 통째) | 동일 (4096 byte 통째) |
| inode block 부분 갱신 | 없음 (escape로 통째 저장) | 없음 (동일) |
| revocation | 있음 | 있음 |
| lazy mtime | 있음 (성능 최적화) | 없음 (모든 write가 메타 변경) |
| checkpoint batch | 있음 | 없음 (즉시 checkpoint) |

## 13. 다음 문서로의 안내

- `08-snapshot.md` — 스냅샷 생성도 transaction 단위. 스냅샷 시점에 commit이 어떻게 끼어드는지
- `10-crash-recovery.md` — replay 시나리오의 구체적 단계와 검증
