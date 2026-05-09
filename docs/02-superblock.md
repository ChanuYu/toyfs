# toyfs — Superblock

이 문서는 `toyfs`의 **superblock** on-disk 구조를 정의한다.
superblock은 파일시스템 전체 메타데이터의 진입점이다.

## 1. 위치와 크기

- 블록 1: **primary superblock**
- `total_blocks - 1`: **backup superblock** (사본)
- 크기: 1 블록 (4 KiB) 고정. 실제 사용은 ~256 B 정도이고 나머지는 0 패딩
- mount 시 가장 먼저 primary를 읽는다. 그게 깨졌으면 backup을 읽는다 (자동 복구는 1단계 범위 밖)

## 2. on-disk 포맷

모든 정수는 little-endian. C 풍의 의사 코드로 표현하지만, Go 구현 시
`encoding/binary`로 동일한 바이트 시퀀스를 생산한다.

```
struct toyfs_superblock {
    // ---- identification (32 B) ----
    u8   magic[4];              // "TOYF" = {0x54, 0x4F, 0x59, 0x46}
    u32  version;               // on-disk format version, 시작은 1
    u32  block_size;            // 4096 (mkfs 시 결정, 다른 값은 1단계에서 미지원)
    u32  flags;                 // 아래 'flags' 절 참고
    u8   uuid[16];              // 임의의 fs UUID (mkfs 시 랜덤 생성)

    // ---- geometry (64 B) ----
    u64  total_blocks;          // 이미지 전체 블록 수
    u64  inode_count;           // 전체 inode 슬롯 수
    u64  block_bitmap_start;    // 블록 번호
    u64  block_bitmap_blocks;
    u64  inode_bitmap_start;
    u64  inode_bitmap_blocks;
    u64  inode_table_start;
    u64  inode_table_blocks;

    // ---- subsystem regions (64 B) ----
    u64  journal_start;
    u64  journal_blocks;
    u64  snapshot_meta_start;
    u64  snapshot_meta_blocks;
    u64  refcount_table_start;
    u64  refcount_table_blocks;
    u64  data_block_start;
    u64  data_block_count;

    // ---- runtime counters (32 B) ----
    u64  free_blocks;           // 가용 데이터 블록 수
    u64  free_inodes;           // 가용 inode 슬롯 수
    u64  next_txn_id;           // journal transaction id 단조 증가 카운터
    u64  mount_count;           // 누적 마운트 횟수

    // ---- root & snapshot pointer (16 B) ----
    u32  root_inode;            // 항상 1 (예약)
    u32  active_snapshot_id;    // 0 = 활성 (스냅샷 아님)
    u64  reserved_root_pad;

    // ---- timestamps (16 B) ----
    u64  mkfs_time;             // mkfs 시각 (Unix epoch, ns)
    u64  last_mount_time;

    // ---- snapshot slot table (4 슬롯 × 16 B = 64 B) ----
    struct {
        u32 snapshot_id;        // 0 = 빈 슬롯
        u32 flags;
        u64 created_at;
    } snapshots[4];

    // ---- integrity (8 B) ----
    u32  checksum;              // CRC32 of all preceding bytes (이 필드 제외)
    u32  reserved_csum_pad;

    // ---- padding to 4096 B ----
    u8   pad[/* fill to 4096 */];
};
```

총 사용 영역 크기: 32 + 64 + 64 + 32 + 16 + 16 + 64 + 8 = **296 B**.
나머지 3,800 B는 0 패딩 + 추후 필드 확장용.

## 3. 필드별 의미

### 3.1 identification

- `magic`: 디스크 이미지가 toyfs인지 식별. 다른 4바이트면 mount 실패
- `version`: on-disk 포맷 버전. 호환되지 않는 변경은 이 숫자를 올린다.
  1단계에서는 항상 1
- `block_size`: 4096 고정. 다른 값을 발견하면 mount 실패 (1단계 한정)
- `flags`: 비트 플래그. 아래 4절 참고
- `uuid`: 디스크 이미지 식별자. mkfs 시점에 `crypto/rand` 등으로 16 byte 생성

### 3.2 geometry

각 영역의 시작 블록과 크기. 모두 mkfs 시점에 결정되며, mount 후에는 read-only로 취급한다.

mount 직후 다음 invariant를 검증한다:
- 영역들이 서로 겹치지 않음
- 모든 시작 블록이 `[0, total_blocks)` 범위
- 영역 끝(start + blocks)이 `total_blocks` 이하
- `data_block_start + data_block_count == total_blocks - 1` (backup superblock 자리 1블록 제외)

### 3.3 subsystem regions

- `journal_start`/`journal_blocks`: `07-journal.md` 참고. 이 영역의 첫 블록이 journal superblock
- `snapshot_meta_start`/`snapshot_meta_blocks`: `08-snapshot.md` 참고
- `refcount_table_start`/`refcount_table_blocks`: 데이터 블록당 2 byte refcount의 배열. 인덱스 i = i번째 데이터 블록의 refcount

### 3.4 runtime counters

mount 중에는 in-memory 사본을 갱신하고, sync/unmount 시 디스크에 반영한다.

- `free_blocks`/`free_inodes`: 정확성보다는 *근사*. block bitmap을 진실의 원천으로 본다.
  비싼 풀스캔을 피하려고 캐시하는 값. fsck가 풀스캔으로 재계산
- `next_txn_id`: journal commit마다 1 증가. wraparound 방지를 위해 64-bit
- `mount_count`: 통계용. 디버깅에 도움

### 3.5 root & snapshot pointer

- `root_inode`: 항상 1로 고정. 1단계에서는 inode 1번이 root 디렉토리 (`/`).
  필드를 둔 이유는 *어떤 inode가 root인지*가 명시적이어야 스냅샷 롤백 시 일관성을 잃지 않기 때문
- `active_snapshot_id`: 현재 마운트가 활성 FS인지(=0) 특정 스냅샷을 read-only로 mount했는지
  나타냄. 1단계에서는 항상 0; 스냅샷 mount는 5단계에서 추가

### 3.6 timestamps

- `mkfs_time`, `last_mount_time`: 단순 통계. CRC와 무관

### 3.7 snapshot slot table

4개의 고정 슬롯. 각 슬롯은:
- `snapshot_id == 0`이면 빈 슬롯
- `snapshot_id != 0`이면 해당 ID의 스냅샷이 활성. 자세한 메타데이터는 `snapshot_meta_start` 영역에 따로

여기 superblock 안에 4 슬롯을 둔 이유: mount 직후 superblock 한 번 읽으면
"현재 어떤 스냅샷들이 살아있는가"를 즉시 알 수 있게 하기 위함.

### 3.8 integrity

- `checksum`: CRC32. 이 필드 자신은 0으로 친 상태로 계산
- mount 시 검증 실패하면 backup superblock 시도

## 4. flags 비트 정의

```
TOYFS_SB_CLEAN              = 1 << 0   // unmount 시 0으로 클리어 후 디스크에 기록.
                                       // mount 시 이게 1이면 → "정상 unmount되지 않음" → journal replay 필요
TOYFS_SB_HAS_JOURNAL        = 1 << 1   // journal 활성 (1단계에서는 항상 1)
TOYFS_SB_HAS_SNAPSHOT       = 1 << 2   // 스냅샷 슬롯 중 하나라도 사용 중이면 1
TOYFS_SB_READ_ONLY          = 1 << 3   // mount read-only (스냅샷 mount 시 1)
TOYFS_SB_ROLLBACK_IN_PROGRESS = 1 << 4 // rollback이 여러 transaction에 걸쳐 진행 중. 자세한 건 10번 §5.7.1
                                       // (5단계 구현 시 함께 활성화)
```

mount 흐름에서 flags 사용:
1. superblock 로드
2. `TOYFS_SB_CLEAN`이 0이면 → journal replay 호출
3. mount 성공 시 `TOYFS_SB_CLEAN = 0`으로 디스크에 기록 (= "지금 mount 중")
4. unmount 시 모든 dirty 블록 flush → `TOYFS_SB_CLEAN = 1`로 기록

## 5. in-memory 표현

```go
type Superblock struct {
    // on-disk 필드 전부 들고 있음 (위 구조체와 1:1 대응)
    Magic        [4]byte
    Version      uint32
    BlockSize    uint32
    // ... 생략
    Snapshots    [4]SnapshotSlot
    Checksum     uint32

    // ---- in-memory only ----
    Dirty        bool       // sync 필요 여부
    DiskOffset   uint64     // 1 (primary 위치)
}
```

`Dirty`/`DiskOffset`은 디스크에 안 쓰이고, sync 시 어디에 기록해야 할지를 알리는 런타임 정보.

## 6. mkfs / mount / unmount 시 동작

### 6.1 mkfs
1. 이미지 파일을 0으로 채워 생성
2. geometry 계산 → 모든 `*_start`, `*_blocks` 결정
3. 각 영역 0/초기값으로 채움 (bitmap의 메타데이터 영역 비트는 별도 정책 — 우리 선택은 "데이터 영역만 비트맵에 포함"이므로 여기는 따로 마킹 안 함)
4. inode 1번에 root 디렉토리 inode 생성, `.`/`..` dentry 1쌍 작성
5. journal superblock 초기화
6. snapshot 슬롯 모두 0
7. refcount table 모두 0 (데이터 블록 미사용)
8. flags = `TOYFS_SB_CLEAN | TOYFS_SB_HAS_JOURNAL`
9. CRC 계산 → 기록
10. primary와 backup 양쪽에 동일하게 기록
11. fsync

### 6.2 mount
1. primary superblock 1블록 읽기
2. magic 검증, CRC 검증 (실패 시 backup 시도, 그것도 실패면 mount 실패)
3. version 검증 (1단계는 1만 허용)
4. flags 확인 → `TOYFS_SB_CLEAN == 0`이면 journal replay
5. flags의 CLEAN 비트를 0으로 set → 디스크 기록 → fsync
6. `mount_count++`, `last_mount_time` 갱신
7. in-memory `Superblock` 구조체 구성

### 6.3 unmount
1. 모든 open file 닫기
2. block cache의 dirty 블록 모두 flush
3. journal commit + checkpoint
4. flags의 CLEAN 비트를 1로 set
5. CRC 재계산 후 primary/backup 양쪽에 기록
6. fsync 후 이미지 파일 close

## 7. fsck (1단계 범위 밖, 메모만)

- primary와 backup CRC 모두 깨졌을 때 어떻게 복구할지는 1단계에서 다루지 않음
- 단, mkfs와 dump 도구가 있으니 수동 검수는 가능

## 8. Testing

### 8.1 Unit test
- `Superblock.Marshal() / Unmarshal()` round-trip: 임의 값을 채운 superblock을 직렬화 후 다시 역직렬화해서 동일한 구조체가 나오는지
- CRC 검증:
  - 정상 superblock → `Verify()` 성공
  - 페이로드 1바이트 변조 → `Verify()` 실패
  - checksum 필드만 변조 → `Verify()` 실패
- Magic / version 검증:
  - 잘못된 magic → mount 거부
  - 미지원 version → mount 거부
- Geometry invariant:
  - 영역 겹침 → mkfs 거부
  - `data_block_start + data_block_count > total_blocks - 1` → mkfs 거부
- Flags 비트 set/clear의 정확성

### 8.2 Integration test (4단계 이후)
- mkfs → mount → unmount 사이클 후 `TOYFS_SB_CLEAN`이 1로 남는지
- mkfs → mount 도중 강제 종료 (process kill) → 다음 mount에서 CLEAN == 0으로 인지하는지
- primary 파괴 시뮬레이션: hex 편집으로 magic 변조 → mount는 backup으로 성공해야 함 (5단계 이후 추가 가능)

### 8.3 Crash recovery (5단계 이후)
- mount 중 디스크 이미지를 cp로 스냅샷
- 카피본의 flags 검사 → CLEAN == 0 (mount 중에는 이래야 정상)
- 카피본을 mount → journal replay 트리거 → 정상 mount 완료

## 9. 다음 문서로의 안내

- `03-inode.md` — inode 1번(root) 포맷이 이 문서에서 참조됨
- `07-journal.md` — `journal_start`/`journal_blocks`/`next_txn_id`가 의미를 갖는 곳
- `08-snapshot.md` — `snapshots[4]`의 슬롯 구조와 `active_snapshot_id` 사용
