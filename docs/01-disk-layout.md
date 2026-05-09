# toyfs — Disk Layout

이 문서는 `toyfs` 디스크 이미지(=호스트 FS의 단일 파일)의
**블록 단위 전체 레이아웃**을 정의한다.
on-disk 자료구조의 *위치*만 다루며, 각 자료구조의 *내부 필드*는
이후 문서(`02-superblock.md` 이하)에서 다룬다.

## 1. 기본 단위

| 항목 | 값 | 비고 |
|------|----|------|
| 블록 크기 | 4096 B (4 KiB) | mkfs 시점에 superblock에 기록 |
| 블록 번호 | 64-bit unsigned int | 0이 첫 블록 |
| 디스크 이미지 크기 | 기본 64 MiB (= 16,384 블록) | mkfs 옵션으로 변경 가능 (extent fragmentation 테스트 시 키움) |
| 최대 이미지 크기 | 추후 확정 (extent 포맷이 결정) | `04-extent.md` 참고 |

블록 번호는 **이미지 파일 시작점부터의 4KB 단위 오프셋**이다.
즉 블록 N의 byte offset = `N * 4096`.

## 2. 전체 레이아웃 (블록 단위)

기본 64 MiB 이미지 기준 예시:

```
블록 번호    내용                                      크기 (블록)
─────────  ───────────────────────────────────────  ────────────
0          Boot/reserved (현재는 0으로 채움)          1
1          Superblock (primary)                       1
2          Block bitmap                               B_bm
2+B_bm     Inode bitmap                               I_bm
...        Inode table                                I_tbl
...        Journal area                               J
...        Snapshot metadata area                     S
...        Data blocks                                D
끝-1       Superblock (backup)                        1
```

각 영역의 크기는 mkfs 시점에 이미지 크기와 옵션을 보고 계산되며,
계산 결과는 superblock에 기록된다. 부팅 후에는 superblock만
읽으면 모든 영역의 시작 블록을 알 수 있다.

## 3. 영역별 상세

### 3.1 Block 0 — Boot / Reserved

- 1 블록 고정
- 현재는 사용하지 않음. 모두 0으로 초기화
- 이후 부트로더 / partition info 등이 필요해질 경우를 위한 자리

### 3.2 Block 1 — Primary Superblock

- 1 블록 고정
- 파일시스템 전체 메타데이터 (magic, version, 영역 시작 블록, 카운터, 스냅샷 슬롯 등)
- 자세한 내용은 `02-superblock.md`
- mount 시 가장 먼저 읽는 블록

### 3.3 Block bitmap

- **데이터 블록 영역만** 비트맵으로 추적한다. 메타데이터 영역(0번~데이터 시작 직전)은 bitmap에 포함하지 않으며, 항상 "사용 중"으로 간주된다.
- 4KB = 32,768 비트 = 32,768개의 데이터 블록 상태를 1블록으로 표현
- 기본 64 MiB 이미지에서 데이터 블록은 약 14,800개 → `B_bm = 1` 블록
- 비트 1 = 사용 중, 0 = free
- 비트맵의 비트 인덱스 i는 `data_block_start + i` 블록에 대응

### 3.4 Inode bitmap

- inode 테이블 슬롯의 할당 상태를 1비트씩 표현
- inode는 256 B 크기 가정 (자세한 건 `03-inode.md`)
- 4 KB 블록당 16개 inode가 들어가므로, inode bitmap 1블록 = 32,768개 inode 상태
- 기본 설정에서 inode bitmap은 1블록이면 충분

### 3.5 Inode table

- inode 구조체의 배열
- inode 번호 0은 예약 (invalid)
- inode 번호 1은 root 디렉토리 (`/`)
- 크기는 mkfs 옵션 `-N <inodes>`로 결정. 기본값은 "데이터 블록 수 / 4" 정도

### 3.6 Journal area

- 메타데이터 변경을 위한 circular log
- 자세한 건 `07-journal.md`
- 크기는 mkfs 옵션 `-J <size>`로 결정. 기본 1 MiB (= 256 블록)
- 내부에 자체 헤더 블록(=journal superblock) 1개 + 로그 블록들

### 3.7 Snapshot metadata area

- 스냅샷 슬롯의 메타데이터 + bitmap/inode_table 사본
- 자세한 건 `08-snapshot.md`
- 슬롯 수 4개 고정. 슬롯당 ~259 블록(헤더 1 + block_bitmap 1 + inode_bitmap 1 + inode_table 256)
- 4 슬롯 × ~259 = **약 1040 블록 (~4 MiB)**
- 스냅샷 데이터 블록 자체는 별도 영역이 아니라 일반 data blocks에 저장됨
  (CoW 방식이므로 refcount만 있으면 됨)

### 3.8 Block refcount table — *snapshot용*

CoW 스냅샷을 위해 **데이터 블록마다 refcount**가 필요하다.
- 각 블록당 16-bit (= 2바이트). 한 블록에 동시 공유되는 스냅샷 최대 4개 + 활성 1개를 충분히 커버 (실제로는 16-bit이라 65,535까지 가능; 2바이트로 잡은 이유는 03번 1번 답변 참고 — overflow 감지 마진 + 정렬 편의)
- 4 KB 블록 = 2,048개의 refcount 엔트리
- 기본 64 MiB 이미지(데이터 블록 ~14,800개)에서는 `~8` 블록

이 테이블은 `Snapshot metadata area` 직후에 위치한다.

> 참고: refcount 자료구조의 갱신 자체도 메타데이터이므로 journal을 거친다.

### 3.9 Data blocks

- 실제 파일 데이터, 디렉토리 dentry, extent index 블록이 모두 여기 들어간다
- inode 자체는 들어가지 않음 (inode table은 별도 영역)
- 가장 큰 영역. 이미지의 대부분이 여기

### 3.10 끝 블록 — Backup Superblock

- 마지막 블록(= `total_blocks - 1`)에 superblock 사본 1개
- primary가 깨졌을 때 fsck 같은 도구가 복구에 사용
- 1단계 프로토타입에서는 *작성만* 하고 자동 복구는 구현 범위 밖

## 4. mkfs 시점에 결정되는 값

mkfs는 다음 입력으로 호출된다:

```
mkfs.toyfs <image_path> [-s <size>] [-N <inodes>] [-J <journal_size>]
```

- 이미지 파일을 `<size>` 만큼 0으로 채워 만든 뒤,
- 위 레이아웃대로 각 영역을 배치하고,
- superblock에 다음 값을 기록:
  - `total_blocks`
  - `block_size` (= 4096)
  - `inode_count`
  - `block_bitmap_start`, `inode_bitmap_start`
  - `inode_table_start`, `inode_table_blocks`
  - `journal_start`, `journal_blocks`
  - `snapshot_meta_start`, `snapshot_meta_blocks`
  - `refcount_table_start`, `refcount_table_blocks`
  - `data_block_start`, `data_block_count`
  - `root_inode` (= 1)

## 5. 정렬과 endian

- 모든 on-disk 정수 필드는 **little-endian**
- 각 자료구조는 4 또는 8바이트 경계에 정렬
- block-aligned 자료구조 (superblock, inode, journal record 등)는
  남는 공간을 0 패딩
- 패딩 영역은 미래 필드 확장용

## 6. 자료구조 스택 다이어그램

논리적 의존 관계 (위가 아래에 의존):

```
            ┌─────────────────────┐
            │ ops layer           │
            └─────────┬───────────┘
                      │
        ┌─────────────┼──────────────┐
        ▼             ▼              ▼
   ┌────────┐    ┌────────┐    ┌──────────┐
   │ inode  │    │ dentry │    │ snapshot │
   └───┬────┘    └───┬────┘    └─────┬────┘
       │             │                │
       ▼             │                ▼
   ┌────────┐        │           ┌─────────┐
   │ extent │        │           │refcount │
   └───┬────┘        │           └────┬────┘
       │             │                │
       └─────────────┼────────────────┘
                     ▼
              ┌─────────────┐
              │ journal     │
              └──────┬──────┘
                     ▼
              ┌─────────────┐
              │ block cache │
              └──────┬──────┘
                     ▼
              ┌─────────────┐
              │ disk image  │
              └─────────────┘
```

## 7. 크기 산정 예시 (기본값)

- 이미지: 64 MiB → 16,384 블록
- Boot: 1
- Superblock primary: 1
- Block bitmap: 1 (데이터 블록 ~14.8K개를 커버하려면 1블록으로 충분)
- Inode bitmap: 1
- Inode table: 256 블록 (4,096 inodes; 데이터 블록 수 / 4 정책의 근사)
- Journal: 256 블록 (1 MiB)
- Snapshot meta: 1040 블록 (4 슬롯 × ~259 블록, 자세한 건 `08-snapshot.md`)
- Refcount table: 8 블록 (~16 KiB; 데이터 블록 ~14.8K × 2 byte)
- Backup superblock: 1
- 합계 메타데이터: 약 1,565 블록 (~6.1 MiB)
- **데이터 블록: 약 14,819 블록 (≈ 58 MiB)**

이 수치는 1단계 검토용이며, 실제 mkfs 구현 시 미세 조정될 수 있다.
extent fragmentation 같은 큰 데이터셋이 필요한 테스트에서는
`mkfs.toyfs -s 256m` 또는 그 이상으로 키워서 사용한다.

## 8. 검증할 invariant

mkfs 직후, 그리고 fsck 시 다음 조건이 성립해야 한다:

- 모든 영역의 시작 블록이 superblock에 기록된 값과 일치
- 영역들이 겹치지 않음
- 영역 합계 ≤ `total_blocks`
- block bitmap이 데이터 영역만 커버한다는 정의가 superblock의 `data_block_start` / `data_block_count`와 일치 (메타데이터 영역은 비트맵에 포함되지 않음)
- mkfs 직후 block bitmap의 모든 비트는 0 (데이터 영역 아무것도 안 씀; 단, root 디렉토리의 데이터 블록 1개 할당분만 1)
- inode bitmap의 0번은 1로 마킹됨 (예약), 1번은 1로 마킹됨 (root)
- root inode가 유효한 디렉토리 inode이고 `.`/`..` 두 dentry를 가짐

## 9. Testing 섹션 (이 컴포넌트 단위 테스트)

`pkg/block` 또는 `cmd/mkfs` 단계에서 다음을 검증한다.

### 9.1 Unit test (in-memory disk)
- mkfs 함수에 다양한 이미지 크기 (1MiB, 16MiB, 256MiB) 입력 → 위 invariant가 모두 성립하는지
- 영역 시작/크기 계산 함수에 경계값 (이미지가 작아서 데이터 블록이 거의 없거나, 0 또는 음수가 되는 케이스) 입력 → 적절한 에러 반환
- superblock 직렬화/역직렬화 round-trip
- block bitmap 비트 set/clear/test의 정확성

### 9.2 Integration test (4단계 이후)
- 실제 mkfs로 이미지를 만들고 `toyfs-dump`로 덤프 → 사람이 읽고 검수 가능한 형태
- 두 번 mkfs 했을 때 결과가 동일 (deterministic)

### 9.3 Crash recovery (5단계 이후)
- mkfs 자체는 idempotent해야 함. mkfs 도중 중단 시 일관된 상태가 아니어도 OK이지만,
  사용자가 다시 mkfs 하면 정상 동작해야 한다.
