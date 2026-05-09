# toyfs — Snapshot

이 문서는 `toyfs`의 **snapshot** 메커니즘을 정의한다.
기존 결정사항(02번 superblock의 snapshot slot, 03번 full inode CoW, 01번 refcount table)이
여기서 결합된다.

핵심 원칙:
- **snapshot은 read-only 시점 사본**. 쓰기 가능 분기(branch/clone)는 1단계 범위 밖
- **메타데이터(bitmap, inode table)는 통째 사본**. snapshot 생성 시 snapshot_meta 영역에 복사
- **데이터/디렉토리/extent index 블록은 refcount 기반 공유 + CoW**
- **활성 FS와 snapshot은 동시 mount 가능** (snapshot은 read-only)
- **롤백은 단순화된 형태로 1단계 포함**

## 1. 기본 단위

| 항목 | 값 |
|------|----|
| snapshot 슬롯 수 | 4 (superblock 안) |
| snapshot 1개당 슬롯 크기 | 헤더 1 + block_bitmap 1 + inode_bitmap 1 + inode_table 256 = **259 블록 (~1 MiB)** |
| snapshot_meta 영역 총 크기 | 4 슬롯 × 259 블록 = 1036 블록 + align padding ≈ **1040 블록 (~4 MiB)** |
| CoW 트리거 | refcount > 1 |
| 롤백 제약 | open file 없음 + 다른 ops 진행 안 함 + **다른 snapshot 0개** |

> **주의**: 01번 문서의 snapshot_meta 영역 크기를 ~4 MiB로 확대. 02번 superblock의 snapshot slot 수를 4로 축소. 7번 갱신 사항이 1, 2번 문서로 역전파됨 — 1단계 합의 후 일괄 수정.

## 2. snapshot 슬롯 (superblock 내부)

02번 문서의 superblock 안 `snapshots[8]`을 **`snapshots[4]`로 축소**:

```
struct {
    u32 snapshot_id;        // 0 = 빈 슬롯
    u32 flags;
    u64 created_at;
} snapshots[4];
```

`snapshot_id`는 1부터 단조 증가. 0은 활성 FS 또는 빈 슬롯.

`flags` 비트:
- bit 0: TOYFS_SNAP_VALID — 정상 생성 완료
- bit 1: TOYFS_SNAP_MOUNTED — 현재 어디선가 read-only mount 중

## 3. snapshot_meta 영역 — 슬롯별 메타 사본

01번 문서의 `snapshot_meta_start` 영역. 4 슬롯 각각이 다음 구조를 가짐:

```
struct toyfs_snapshot_slot_meta {
    // ---- header (1 블록) ----
    u32  magic;                    // "TSSM"
    u32  snapshot_id;
    u64  created_at;
    u32  root_inode_num;            // 이 시점의 root inode 번호 (현재는 항상 1)
    u32  reserved;
    u64  bitmap_copy_block_start;    // block bitmap 사본 시작 (이 영역 내부 오프셋)
    u64  bitmap_copy_block_count;
    u64  inode_bitmap_copy_block_start;
    u64  inode_bitmap_copy_block_count;
    u64  inode_table_copy_block_start;
    u64  inode_table_copy_block_count;
    u8   pad[/* fill to 4096 */];

    // ---- block bitmap 사본 (~1 블록) ----
    // ---- inode bitmap 사본 (~1 블록) ----
    // ---- inode table 사본 (~256 블록) ----
};
```

총 슬롯당 ~259 블록. 4 슬롯 × 259 = ~1036 블록 + 약간의 align padding.

mkfs 시점에 snapshot_meta 영역을 모두 0으로 채움. 슬롯의 `magic == "TSSM" && snapshot_id != 0`이면 사용 중.

## 4. snapshot 자료구조 정리 (전체)

snapshot 메커니즘이 다루는 자료구조와 각각의 처리 방식:

| 자료구조 | snapshot 시점에 어떻게 처리하나 |
|---|---|
| superblock | 활성 그대로. snapshot 슬롯에 등록만 |
| block bitmap | snapshot_meta에 통째 사본 |
| inode bitmap | snapshot_meta에 통째 사본 |
| inode table | snapshot_meta에 통째 사본 |
| 활성 inode (자체 슬롯) | 수정 시 full inode CoW (새 슬롯 할당) |
| 디렉토리 데이터 블록 | refcount table 공유 + write 시 CoW |
| 파일 데이터 블록 | refcount table 공유 + write 시 CoW |
| extent index 블록 | refcount table 공유 + write 시 CoW |
| journal 영역 | snapshot과 무관 (생성 시점에 commit 완료 보장) |
| superblock 사본 (backup) | snapshot과 무관 |

## 5. CoW 메커니즘 — 두 축

### 5.1 inode CoW (full inode)

03번 문서 7절 그대로. 활성 FS에서 inode I 수정 시 새 슬롯에 I' 복사 → 부모 dentry 갱신 → root까지 전파.

**snapshot은 자기 inode_table 사본을 통해 옛 I를 봄.** 활성 inode_table에서 그 슬롯이 어떻게 되든 무관 (snapshot의 사본은 별개).

활성 inode_bitmap에서 옛 슬롯의 비트는 *재사용 가능 시점*에 0으로 클리어:
- 활성 dentry가 더 이상 그 슬롯을 안 가리키면 즉시 0
- 옛 슬롯의 인메모리 inode 자료구조도 해제

활성 입장에선 그 슬롯이 free. snapshot 입장에선 자기 사본의 그 슬롯이 그대로 살아있음. 두 view가 *물리적으로 분리*되어 있어 충돌 없음.

### 5.2 데이터 블록 CoW (refcount 기반)

04번 문서 8절을 보강.

**refcount table** (01번 3.8): 데이터 블록 영역의 모든 블록에 1:1 대응하는 16-bit 카운터. 인덱스 i = `data_block_start + i` 블록의 refcount.

#### snapshot 생성 시
```
for each inode I in 활성 inode_bitmap에서 set:
    for each extent E in I:
        for each data_block B in E.physical_range:
            refcount_table[B - data_block_start] += 1
```

64 MiB 이미지(데이터 블록 ~15K개)에서 inode 4096개를 brute force 스캔해도 즉시 끝남. 학습 단순성 우선.

#### 활성 write 시 CoW 트리거
```
func WriteToBlock(inode I, logical_offset L, data):
    physical = extent_lookup(I, L)
    rc_idx = physical - data_block_start
    if refcount_table[rc_idx] > 1:
        // 공유 중. CoW 발동
        new_block = allocate_data_block()
        copy(physical → new_block)
        new_rc_idx = new_block - data_block_start
        refcount_table[new_rc_idx] = 1
        refcount_table[rc_idx] -= 1
        // extent 갱신: I의 extent에서 L을 가리키는 entry의 ee_physical 변경
        // (참고: extent 자체도 SHARED 상태일 수 있어 extent index 블록도 CoW 가능)
        update_extent(I, L, new_block)
        physical = new_block
    write(physical, data)
```

#### extent index 블록도 CoW 대상

extent index 블록(04번 4절)은 데이터 블록 영역에 있음. 따라서 *데이터 블록과 동일하게* refcount table로 추적.

활성 inode가 새 데이터 블록을 추가하느라 자기 extent index 블록을 수정해야 한다면:
- index 블록의 refcount > 1이면 CoW 발동
- 새 index 블록 할당, 옛 내용 복사 후 갱신
- 부모 (= inode 자체 또는 상위 index)가 새 index 블록을 가리키도록 갱신

### 5.3 SHARED flag — 빠른 판정 캐시

extent의 `ee_flags`에 `TOYFS_EXTENT_SHARED = 1 << 1` (04번 2.1):

snapshot 생성 시 모든 extent의 SHARED flag를 set. 활성 write 시 SHARED가 set이면 refcount table을 읽어 확인 후 CoW.

CoW 후 새 블록의 extent는 SHARED = 0. 옛 extent는 SHARED 유지 (snapshot이 여전히 가리킴).

> **단순화**: SHARED는 *최적화 캐시*이지 진실의 원천이 아니다. 진실의 원천은 refcount table. 둘이 어긋난 경우 fsck로 정정.

## 6. snapshot 생성 (create)

```
func CreateSnapshot() (snapshot_id, error):
    // 1. 빈 슬롯 찾기
    slot_idx := find slot where superblock.snapshots[i].snapshot_id == 0
    if slot_idx == -1: return ENOSPC

    // 2. 새 ID 부여
    new_id := superblock.next_snapshot_id++

    // 3. 메타 사본 — 통째 복사
    target_slot := snapshot_meta[slot_idx]
    target_slot.magic = "TSSM"
    target_slot.snapshot_id = new_id
    target_slot.created_at = time.Now()
    target_slot.root_inode_num = 1                  // 항상 root

    // block_bitmap 통째 복사 (1 블록)
    copy(superblock.block_bitmap_start → target_slot.bitmap_copy)
    // inode_bitmap 통째 복사 (1 블록)
    copy(superblock.inode_bitmap_start → target_slot.inode_bitmap_copy)
    // inode_table 통째 복사 (256 블록)
    copy(superblock.inode_table_start → target_slot.inode_table_copy)

    // 4. 모든 데이터 블록 + extent index 블록의 refcount += 1
    for each inode I in 활성 inode_bitmap (set bits):
        for each extent E in I:
            for each block B in E.physical_range:
                refcount_table[B - data_block_start] += 1
            // E의 SHARED flag set
            E.ee_flags |= TOYFS_EXTENT_SHARED
        // extent index 블록도 refcount += 1 (extent_depth > 0인 경우)
        if I.extent_depth > 0:
            refcount_table[I의 index 블록 - data_block_start] += 1
            (재귀적으로 트리 traversal)

    // 5. superblock의 snapshot 슬롯 등록
    superblock.snapshots[slot_idx] = {
        snapshot_id: new_id,
        flags:       TOYFS_SNAP_VALID,
        created_at:  time.Now(),
    }
    superblock.flags |= TOYFS_SB_HAS_SNAPSHOT

    // 6. 모두 한 transaction으로 journal commit
    return new_id, nil
```

비용: 통째 복사 ~258 블록 + refcount table 갱신 (~15K 블록 × 16-bit). 디스크 I/O는 ~520 블록 정도. 64 MiB 이미지에서 짧은 시간에 완료.

## 7. snapshot mount (read-only)

snapshot을 read-only mountpoint로 마운트:

```
func MountSnapshot(snapshot_id, mountpoint):
    // 1. snapshot 슬롯 찾기
    slot_idx := find slot where superblock.snapshots[i].snapshot_id == snapshot_id
    if slot_idx == -1: return ENOENT

    // 2. snapshot_meta 슬롯 로드
    slot_meta := snapshot_meta[slot_idx]

    // 3. snapshot 전용 in-memory FS view 생성
    view := SnapshotView{
        Superblock:        활성 superblock의 사본 (read-only marker set),
        BlockBitmap:       slot_meta.bitmap_copy,
        InodeBitmap:       slot_meta.inode_bitmap_copy,
        InodeTable:        slot_meta.inode_table_copy,
        RootInode:         slot_meta.root_inode_num,
        ReadOnly:          true,
    }

    // 4. flags에 MOUNTED set
    superblock.snapshots[slot_idx].flags |= TOYFS_SNAP_MOUNTED

    // 5. FUSE에 view를 mount
    return fuse_mount(mountpoint, view)
```

snapshot view는 활성 FS와 *완전히 별개*의 in-memory 구조체. read-only이므로 cache는 read-only로 동작 (write 호출 시 EROFS).

snapshot view의 *데이터 블록 read* 경로:
- inode → extent → physical block 번호
- block_cache.Get(physical) → 디스크에서 읽음
- 활성 FS와 *같은 block cache 인스턴스*를 공유하므로, 같은 블록을 양쪽에서 read하면 cache hit
- 데이터 블록 자체는 활성/snapshot 양쪽에서 read-only 시각으로 동일 (CoW 전까지)

> 활성과 snapshot 동시 mount는 read-only safety로 충돌 없음. single-thread 가정 위에서 read 동시성도 lock-free.

## 8. snapshot 삭제 (delete)

```
func DeleteSnapshot(snapshot_id):
    // 1. 슬롯 찾기 + mount 검사
    slot_idx := find slot
    if superblock.snapshots[slot_idx].flags & TOYFS_SNAP_MOUNTED:
        return EBUSY    // mount 중인 snapshot은 삭제 불가

    slot_meta := snapshot_meta[slot_idx]

    // 2. snapshot이 가리키는 모든 데이터/index 블록의 refcount 감소
    // snapshot의 inode_table 사본을 traversal
    for each inode I in slot_meta.inode_bitmap_copy (set bits):
        I_data := slot_meta.inode_table_copy[I]
        for each extent E in I_data:
            for each block B in E.physical_range:
                refcount_table[B - data_block_start] -= 1
                if refcount_table[...] == 0:
                    block_bitmap[B - data_block_start] = 0  // 활성에서도 free
        // extent index 블록도 동일 처리
        if I_data.extent_depth > 0:
            for each index block IB in I_data's tree:
                refcount_table[IB] -= 1
                if 0:
                    block_bitmap[...] = 0

    // 3. snapshot_meta 슬롯 클리어 (magic, snapshot_id 모두 0)
    zero out snapshot_meta[slot_idx]

    // 4. superblock 슬롯 비움
    superblock.snapshots[slot_idx] = {0, 0, 0}
    if 모든 슬롯이 0:
        superblock.flags &= ~TOYFS_SB_HAS_SNAPSHOT

    // 5. 한 transaction으로 commit
```

`block_bitmap[B] = 0` 처리에 주의:
- refcount[B]가 0이 되면 그 블록은 *어떤 inode도 안 가리킴* → free
- 활성 block_bitmap 비트도 0으로 (이미 활성에서 0이었거나, 이번에 0으로)

snapshot 삭제 후 활성 FS의 view에는 영향 없음. 단순히 디스크 공간이 회수될 뿐.

## 9. snapshot 롤백 (rollback)

**제약 3가지** (1단계 단순화):
1. 활성 FS에 *open file 없음*
2. 다른 ops 진행 안 함 (rollback 자체가 단독 ops)
3. **다른 snapshot 0개** (롤백 대상 외에 다른 snapshot이 없을 것)

3번 제약이 핵심 단순화: 다른 snapshot이 없으니 데이터 블록 refcount는 *오직 활성 FS와 롤백 대상 snapshot* 둘만 가리킴 → 롤백 후 모든 블록의 refcount = 1로 정리 가능.

```
func Rollback(snapshot_id):
    // 1. 제약 검사
    if 활성 FS에 open file 있음:                     return EBUSY
    if 다른 ops 진행 중:                              return EBUSY
    if 다른 snapshot이 존재 (snapshots[i] != 0 for i != target): return EINVAL

    // 2. journal 정리
    journal.FlushAll()
    journal.WaitForAllCheckpoint()
    // 이 시점에 모든 transaction이 본위치까지 도달

    slot_idx := find slot where superblock.snapshots[i].snapshot_id == snapshot_id
    slot_meta := snapshot_meta[slot_idx]

    // 3. 활성 메타데이터 사본 복원
    copy(slot_meta.bitmap_copy → 활성 block_bitmap_start)
    copy(slot_meta.inode_bitmap_copy → 활성 inode_bitmap_start)
    copy(slot_meta.inode_table_copy → 활성 inode_table_start)

    // 4. 데이터 블록 refcount 정리
    // 다른 snapshot 0개 제약 덕분에 단순:
    // - snapshot 시점에 가리키던 블록: refcount = 1 (활성만 가리킴)
    // - snapshot 시점 이후 활성에서만 새로 할당된 블록: free
    // bitmap이 복원되었으므로 block_bitmap을 진실의 원천으로 정리:
    for each block_index in 0..data_block_count-1:
        if 활성 block_bitmap[block_index] == 1:
            refcount_table[block_index] = 1
        else:
            refcount_table[block_index] = 0
            // 만약 이전에 활성이 가리키던 데이터가 있더라도, snapshot이 그걸 안 가리키면
            // 위 bitmap 복원으로 0이 됨 → 자동으로 free 상태

    // 5. extent SHARED flag 정리 — 모두 0으로 (이제 공유 없음)
    // 단, 활성 inode_table이 사본으로 덮였으므로 extent의 ee_flags도 사본 시점 그대로.
    // snapshot 시점에 SHARED set이었던 것이 그대로 옴 → 일괄 클리어 필요.
    for each inode I in 활성 inode_table:
        for each extent E in I:
            E.ee_flags &= ~TOYFS_EXTENT_SHARED

    // 6. snapshot 슬롯 비움 (롤백 대상 snapshot은 사라짐)
    zero out snapshot_meta[slot_idx]
    superblock.snapshots[slot_idx] = {0, 0, 0}
    superblock.flags &= ~TOYFS_SB_HAS_SNAPSHOT

    // 7. superblock root_inode (= 1) 갱신은 불필요. 우리 모델에서 root는 항상 inode 1.
    //    inode_table 사본 복원으로 inode 1의 내용도 자동으로 옛 시점

    // 8. 모든 단계를 한 transaction으로 journal commit
    return OK
```

### 9.1 롤백의 핵심 통찰

학습 가치 큰 포인트:
1. **메타데이터 통째 사본 모델 덕에 "복원"이 *디스크 복사*로 끝남** — 트리 traversal 없이 영역 단위 일괄 복원
2. **다른 snapshot 0개 제약**이 4단계 refcount 정리를 *bitmap 한 번 스캔*으로 단순화
3. **journal 정리(flush + wait checkpoint)** 가 atomic 롤백의 전제 — 중간 상태 없이 *시점이 통째로 바뀜*

### 9.2 제약을 풀면 어떻게 복잡해지는가 (참고)

다른 snapshot이 있는 경우의 refcount 정리:
- 롤백 후 활성 FS의 블록 = 롤백 대상 snapshot이 가리키던 블록
- 다른 snapshot들도 같은 블록을 가리킬 수 있음
- refcount = (활성 카운트 1) + (다른 snapshot들이 가리키는 카운트)
- 다른 snapshot의 bitmap 사본을 모두 OR해서 "이 블록을 가리키는 snapshot 수"를 구해야 함

다음 프로젝트로 분리해서 학습 가치를 챙기는 게 좋음.

## 10. 활성 FS와 snapshot의 동시 read 의미론

활성 FS가 read 중에 snapshot도 read 중이라면:

- **inode 슬롯**: 활성은 자기 inode_table을, snapshot은 자기 사본을 봄. 분리됨
- **데이터 블록**: 같은 디스크 블록을 양쪽에서 read 가능 (block cache 공유)
- **bitmap**: 양쪽이 *각자 사본*을 봄. 활성 bitmap의 갱신은 snapshot 사본에 영향 없음

활성 write가 들어오면:
- inode CoW: 활성 inode_table에 새 슬롯 할당 → snapshot은 자기 사본의 옛 슬롯 그대로
- 데이터 CoW: refcount > 1이면 새 블록 할당 → snapshot은 옛 블록 그대로

→ 활성 write 동안 snapshot read는 *어떤 일관성 깨짐도* 일어나지 않음.

## 11. invariant

mount 중 항상:

1. superblock의 `snapshots[]`에서 valid 슬롯 수 = snapshot_meta에서 magic == "TSSM"인 슬롯 수
2. snapshot의 root_inode_num이 가리키는 inode가 그 snapshot의 inode_table_copy에 valid
3. extent의 SHARED flag가 set이면 그 extent의 모든 데이터 블록 refcount > 1 (역은 미보장 — fsck로 정정)
4. refcount_table[B] == 0이면 활성 block_bitmap[B] == 0
5. refcount_table[B] > 0이면 적어도 한 (활성 inode 또는 snapshot)이 그 블록을 가리킴

snapshot 생성 직후:
6. 모든 활성 inode의 모든 extent가 SHARED flag set
7. 모든 데이터 블록의 refcount는 *생성 전 값 + 1* (정확히는 +1, 활성에서만 가리키던 블록 기준)

snapshot 삭제 직후:
8. 그 snapshot이 가리키던 모든 블록의 refcount -= 1
9. refcount == 0인 블록은 활성 bitmap[B] == 0

롤백 직후:
10. 활성 inode_table/bitmap은 snapshot 시점과 byte-by-byte 일치
11. 모든 extent SHARED flag = 0
12. refcount_table은 활성 bitmap과 1:1 일치 (set이면 1, clear면 0)

## 12. Testing

### 12.1 Unit test

생성:
- mkfs 직후 snapshot 생성 → 슬롯 등록 + 사본 정확
- snapshot 4개 가득 → 5번째 생성 시 ENOSPC
- 생성 직후 invariant 6, 7 검증

CoW 트리거:
- snapshot 생성 후 활성 write → refcount > 1인 블록만 CoW
- write 후 refcount 감소/증가 정확
- extent index 블록 CoW: 인라인 5개 추가 시 promote와 CoW 동시 발생
- inode CoW: 부모 dentry까지 연쇄 갱신

snapshot read:
- snapshot 생성 후 활성 unlink → snapshot에는 그 파일이 여전히 보임
- snapshot 생성 후 활성에서 파일 내용 변경 → snapshot에선 옛 내용
- snapshot 생성 후 활성에서 디렉토리 새 entry 추가 → snapshot에선 옛 dentry
- snapshot의 root inode부터 traversal로 모든 파일 reach 가능

삭제:
- snapshot 1개 + write로 분기 만들기 → 삭제 시 snapshot이 단독 가리키던 블록만 free
- 활성과 공유하는 블록은 refcount 1로 남고 free 안 됨
- mount 중 snapshot 삭제 시도 → EBUSY

롤백:
- 제약 위반 케이스 (open file, 다른 ops, 다른 snapshot 존재) → EINVAL/EBUSY
- 정상 롤백 → 활성 메타데이터 = snapshot 시점, refcount_table 정합 (invariant 10~12)
- 롤백 후 활성에서 같은 path read → snapshot 시점 내용
- 롤백 후 활성에서 write → 정상 동작 (CoW 분기 없음)

invariant fuzz:
- 임의 ops 시퀀스 + 임의 snapshot create/delete → 매 단계마다 invariant 1~9 검증

### 12.2 Integration test (4단계 이후)
- FUSE에서 활성과 snapshot을 두 mountpoint로 동시 mount
- 활성에서 변경 후 snapshot mountpoint에서 *옛 내용* 보이는지
- snapshot mountpoint에서 write 시도 → EROFS

### 12.3 Crash recovery (5단계)
- snapshot 생성 도중 crash → 다음 mount의 replay 후 *완전히 생성됐거나 안 됐거나*
- CoW write 도중 crash → 메타데이터/데이터 일관 유지
- 롤백 도중 crash → 다음 mount에서 롤백 transaction이 replay되어 완료, 또는 미완 transaction으로 무시되어 롤백 전 상태로 복귀

### 12.4 Stress
- 1000회 (snapshot create + write + delete) 사이클 → refcount table leak 없음
- 4 슬롯 한도 근접 시나리오

## 13. ext4 / btrfs / ZFS와의 비교

| 항목 | ext4 | btrfs | ZFS | toyfs |
|---|---|---|---|---|
| snapshot 지원 | 없음 | 있음 (subvolume) | 있음 (TXG) | 있음 (단순화) |
| 슬롯 수 | - | 무제한 | 무제한 | 4 (고정) |
| 메타데이터 분리 | LVM 사용 | b-tree CoW | dnode CoW | 통째 사본 |
| 데이터 공유 | LVM block CoW | extent ref tree | birth time + dead list | refcount table |
| read-only / 분기 | block-only | 분기 (subvolume) | read-only + clone | read-only만 |
| 롤백 | 별개 도구 (LVM) | subvolume swap | 즉시 (uberblock 교체) | 단순화 (3 제약) |

toyfs는 *학습 디버깅을 위한 명시성*에 최적화된 모델. 통째 사본 덕에 snapshot의 모든 메타데이터가 한 영역에 모여 있어 dump 떠서 확인 가능. 실제 FS는 디스크 효율을 위해 더 정교한 자료구조를 쓰지만, 본질적 메커니즘(CoW, refcount, 시점 분리)은 동일.

## 14. 이전 문서 갱신 사항

이 문서의 결정에 따라 다음 문서를 갱신해야 함:

- **01-disk-layout.md** §3.7: snapshot_meta 영역 크기를 ~1040 블록(~4 MiB)으로 확대
- **02-superblock.md** §3.7, §2: snapshot 슬롯 수 8 → 4
- **04-extent.md** §2.1: SHARED flag의 의미 확정 (snapshot 공유 표시)

이 갱신은 1단계 합의 완료 후 일괄 적용.

## 15. 다음 문서로의 안내

- `09-links-permissions.md` — 하드링크의 nlink와 snapshot의 refcount가 어떻게 독립적인지
- `10-crash-recovery.md` — snapshot 생성/삭제/롤백 도중 crash 시 복구 시나리오
