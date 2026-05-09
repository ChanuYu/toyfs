# toyfs — Inode

이 문서는 `toyfs`의 **inode** on-disk 구조를 정의한다.
inode는 파일 한 개 (디렉토리, 일반 파일, 심볼릭링크 모두 포함)의
메타데이터를 담는다. 파일의 *내용*은 inode에 직접 들어가지 않고
extent tree를 통해 데이터 블록에 저장된다 (`04-extent.md` 참고).

## 1. 위치와 크기

- 모든 inode는 superblock의 `inode_table_start` 영역에 순차 배치
- inode 크기: **256 B 고정**
- 4 KiB 블록 한 개에 16개 inode가 들어감
- inode 번호 N의 위치:
  - block offset = `inode_table_start + (N / 16)`
  - byte offset within block = `(N % 16) * 256`
- inode 번호 0은 **예약** (invalid sentinel)
- inode 번호 1은 **root 디렉토리** (`/`)

## 2. on-disk 포맷

```
struct toyfs_inode {
    // ---- type & permission (8 B) ----
    u16  mode;                  // file type + permission bits (아래 'mode' 절)
    u16  flags;                 // toyfs-specific flags (아래 'flags' 절)
    u32  uid;

    // ---- ownership & link (8 B) ----
    u32  gid;
    u16  nlink;                 // 하드링크 수. dir의 경우 자식 dir의 ".." 포함
    u16  reserved_link_pad;

    // ---- size & block count (16 B) ----
    u64  size;                  // 파일 크기 (바이트). dir이면 dentry 영역 총 크기
    u64  blocks;                // 점유한 데이터 블록 수 (extent index 블록 포함)

    // ---- timestamps (32 B, 모두 ns 단위 Unix epoch) ----
    u64  atime;                 // 마지막 접근
    u64  mtime;                 // 마지막 데이터 수정
    u64  ctime;                 // 마지막 메타데이터 변경
    u64  crtime;                // 생성 시각 (ext4 crtime과 동일)

    // ---- extent tree header (72 B) ----
    // 자세한 건 `04-extent.md`. 여기서는 inode가 들고 있는 부분만 정의.
    u16  extent_depth;          // 0 = inline extents (아래 inline_extents에 직접 저장)
                                // >0 = inline_extents의 첫 entry가 extent index 블록 포인터
    u16  extent_count;          // depth==0이면 사용 중인 인라인 extent 수 (0~4),
                                // depth>0이면 인덱스 entry 수 (현재는 항상 1, 인라인 슬롯 1개만 사용)
    u32  extent_generation;     // CoW 마다 증가. 스냅샷 vs 활성 구분에 사용
    u8   inline_extents[64];    // 16 B × 4 슬롯.
                                // depth==0: 인라인 extent 0~4개를 직접 저장
                                // depth>0: 첫 슬롯 1개만 사용, extent index 블록을 가리키는 entry
                                //         (나머지 3 슬롯은 0)

    // ---- snapshot bookkeeping (8 B) ----
    u32  snapshot_id;           // 0 = 활성 FS의 inode, !=0 = 특정 스냅샷에 묶인 inode
    u32  refcount;              // 이 inode를 참조하는 스냅샷 수 (활성 + 스냅샷)
                                // nlink와는 다른 축임. 'refcount vs nlink' 절 참고

    // ---- inline data (60 B) ----
    // 짧은 심볼릭링크의 타깃 경로를 여기 저장 (ext4 fast symlink 패턴).
    // mode가 symlink이고 size <= 60이면 inline_extents가 아니라 여기에 직접.
    // 그 외 file type에서는 0으로 남김.
    u8   inline_data[60];

    // ---- integrity (4 B) ----
    u32  checksum;              // 이 필드 제외 252 B에 대한 CRC32

    // ---- padding ----
    u8   pad[/* fill to 256 */];
};
```

총 사용 영역: 8 + 8 + 16 + 32 + 72 + 8 + 60 + 4 = **208 B**
나머지 48 B는 0 패딩 + 추후 필드 확장용.

## 3. 필드별 의미

### 3.1 mode

POSIX `mode_t`와 호환되는 16-bit 값:

```
상위 4비트: file type
  0x8000  S_IFREG    일반 파일
  0x4000  S_IFDIR    디렉토리
  0xA000  S_IFLNK    심볼릭링크
  0x0000  empty       inode bitmap에서 free 처리

하위 12비트: permission
  rwxrwxrwx + setuid/setgid/sticky
```

1단계는 위 3개 type만 지원. character/block device, FIFO, socket은 지원 안 함.

### 3.2 flags (toyfs 전용)

```
TOYFS_INODE_DIRTY      = 1 << 0   // 메모리에서만 의미. 디스크에는 항상 0으로 기록
TOYFS_INODE_IMMUTABLE  = 1 << 1   // 스냅샷에 속한 inode는 수정 불가 (5단계 이후)
TOYFS_INODE_INLINE_SYM = 1 << 2   // size<=60인 symlink가 inline_data에 저장됨
```

### 3.3 nlink

- 일반 파일: 그 파일을 가리키는 dentry 수
- 디렉토리: 자기 자신의 `.` + 부모의 dentry + 자식 디렉토리의 `..` 합. 즉 자식 dir N개 → nlink = 2 + N
- 심볼릭링크: 그 링크를 가리키는 dentry 수 (링크 자체가 가리키는 대상과는 무관)

`nlink == 0`은 사용자 가시성이 사라졌다는 의미일 뿐, 즉시 free 가능 조건은 아니다.
실제 free 가능 조건은 3.8절에 정리한 `nlink == 0 AND refcount == 0 AND OpenCount == 0`.

### 3.4 size, blocks

- `size`: 사용자 관점의 파일 크기. dir의 경우 dentry 데이터의 총 byte 수 (sparse 없음)
- `blocks`: 실제 점유한 4 KiB 블록 수. extent index 블록도 포함. **sparse hole은 포함 안 함**

### 3.5 timestamps

- 모두 8-byte ns 단위 Unix epoch
- 1단계에서는 atime을 매 read마다 갱신하지 않고 *생략* 가능. 학습 우선순위 낮음. mkfs/touch 시점만 정확하면 OK
- ctime은 chmod, chown, rename 등 메타데이터 변경 시 갱신
- mtime은 write/truncate 시 갱신
- crtime은 create 시 한 번만 기록, 이후 불변

### 3.6 extent header

extent tree의 자세한 정의는 `04-extent.md`. 여기서는 *inode가 들고 있는 부분*만:

- `extent_depth == 0`: 작은~중간 파일. `inline_extents` 64 B(= 16 B × 4 슬롯)에 인라인 extent를 0~4개 저장.
  5번째 extent가 필요해지는 시점에 depth를 1로 올리고, 인라인의 4개 extent를 새 index 블록의 leaf로 옮긴 뒤
  inode의 `inline_extents` 첫 슬롯에 그 index 블록을 가리키는 1개 entry만 둠
- `extent_depth > 0`: 큰 파일. `inline_extents`의 첫 슬롯 1개만 사용 — extent **index block**을 가리키는 1개 entry.
  나머지 3 슬롯은 0. 실제 extent들은 그 index 블록 (또는 그 아래 트리)에 들어 있음
- `extent_count`: depth==0이면 인라인 extent 수 (0~4), depth>0이면 항상 1 (=인라인의 첫 슬롯 1개를 쓴다는 의미)
- `extent_generation`: 이 inode의 extent tree가 CoW로 새로 쓰일 때마다 증가.
  스냅샷 inode와 활성 inode를 비교할 때 빠르게 같은지/다른지 판정

> **인라인 extent 4개 결정 근거**: ext4 inode와 동일 숫자. 작은 파일은 index 블록 없이 inode 한 번 읽기로 끝남.
> 5번째 extent부터 depth가 1로 올라가며 트리 분기가 학습 시점에 명시적으로 발생 → fragmentation 테스트가 의미를 가짐.

### 3.7 snapshot bookkeeping

- `snapshot_id`: 0 = 활성 FS, !=0 = 특정 스냅샷의 시점 사본
- `refcount`: **inode 수준의 refcount**. 같은 inode를 활성 + 여러 스냅샷이 공유할 때 증가.
  주의: 데이터 블록 refcount(`refcount_table`)와는 다른 축. 자세한 건 다음 절

### 3.8 refcount vs nlink — 헷갈리기 쉬움

| 축 | 의미 | 증가 시점 | 감소 시점 |
|---|---|---|---|
| `nlink` | **사용자 가시성** dentry 수 | 하드링크 생성 | unlink |
| `refcount` | **시스템 내부** 이 inode를 가리키는 (활성 + 스냅샷) 수 | snapshot 생성 | snapshot 삭제 |

inode가 free 가능하려면:
- `nlink == 0` AND `refcount == 0` AND open file 없음

스냅샷이 있는 한 nlink가 0이어도 inode를 free할 수 없다. 데이터 블록도 마찬가지로
별도 refcount 테이블에서 관리되며, 두 축이 독립적으로 0이 되어야 free.

### 3.9 inline_data (fast symlink)

- mode가 symlink이고 size <= 60이면 inline_data에 타깃 경로를 0-terminated 없이 직접 저장
  (size가 정확한 길이라서 terminator 불필요)
- 이 경우 `extent_depth == 0`, `extent_count == 0`, `inline_extents`는 의미 없음
- 그 외 file type에서는 0 패딩으로 남김

장점: 짧은 symlink는 데이터 블록을 하나도 안 씀 → 디스크 효율 + lookup 빠름.
ext4도 동일 패턴.

### 3.10 checksum

- CRC32. 이 필드 제외 252 B에 대해 계산
- inode 읽기 시 검증, 실패하면 EIO
- 디스크 자체 깨짐 외에 메모리 오염도 일부 잡아냄

## 4. in-memory 표현

```go
type Inode struct {
    Num          uint32     // inode 번호 (디스크에 안 쓰임)

    // on-disk 필드 전부
    Mode         uint16
    Flags        uint16
    UID, GID     uint32
    Nlink        uint16
    Size         uint64
    Blocks       uint64
    Atime, Mtime, Ctime, Crtime uint64
    ExtentDepth  uint16
    ExtentCount  uint16
    ExtentGen    uint32
    InlineExt    [64]byte   // 16B × 4 슬롯. 해석은 ExtentDepth/ExtentCount가 결정
    SnapshotID   uint32
    Refcount     uint32
    InlineData   [60]byte
    Checksum     uint32

    // ---- in-memory only ----
    Dirty        bool
    OpenCount    int        // open()으로 잡혀있는 fd 수
    extentTree   *ExtentTree  // lazy load. 큰 파일에서만 사용
}
```

`extentTree`는 inode를 처음 읽을 때 disk에서 lazy load. extent_depth==0이면 nil 가능 (인라인만 있으니).

## 5. inode 라이프사이클

### 5.1 할당 (create / mkdir / symlink)
1. `inode_bitmap`에서 free 비트 찾아 set
2. inode table의 해당 슬롯을 0으로 클리어
3. mode/uid/gid/nlink/timestamps 초기화
4. extent_depth=0, extent_count=0
5. checksum 계산
6. journal에 기록 → 디스크에 기록

### 5.2 수정 (write / chmod / rename / ...)
1. in-memory inode 갱신
2. ctime (또는 mtime) 갱신
3. dirty 마킹
4. journal commit 시점에 디스크 반영

### 5.3 해제
조건: `nlink == 0 && refcount == 0 && OpenCount == 0`
1. extent tree를 따라가며 모든 데이터 블록 free (refcount 감소 또는 bitmap 클리어)
2. inode_bitmap의 해당 비트 클리어
3. 디스크에는 0으로 클리어할 필요 없음 (다음 할당 시 어차피 덮어씀). checksum만 0으로 두면 valid 시그널은 사라짐
4. 모든 단계는 한 transaction으로 journal에 기록

## 6. 권한 체크 (POSIX 단순 버전)

`access(inode, want)` 알고리즘:
1. 호출자가 root (uid==0) → 항상 OK
2. 호출자 uid == inode.uid → owner 비트 (rwx 상위 3비트) 검사
3. 호출자 gid == inode.gid → group 비트 (중간 3비트) 검사
4. 그 외 → other 비트 (하위 3비트) 검사
5. `want` (R_OK / W_OK / X_OK) 비트 모두 set이면 OK, 아니면 EACCES

`open(O_WRONLY)` 시 W_OK, `read()` 시 R_OK, dir 진입 시 X_OK 등.
1단계는 ACL/capability 없음 — 위 5단계 알고리즘이 전부.

## 7. CoW와 inode

스냅샷 생성 시:
- 활성 inode를 *건드리지 않음*. 스냅샷 슬롯에 "snapshot_id S에 root inode = 1번을 가리킨다"만 기록
- 이후 활성 inode를 수정하려 하면:
  1. inode 자체를 새 슬롯에 복사 (CoW)
  2. 새 슬롯의 inode를 수정
  3. 디렉토리 dentry가 새 inode 번호를 가리키도록 갱신
  4. 옛 inode는 스냅샷이 계속 참조

이 과정에서 `extent_generation`이 증가. 스냅샷 inode의 generation < 활성 inode의 generation.

> **note**: 위는 *full inode CoW* 모델이고, ext4-snapshot 같은 일부 설계는 inode를 그대로 두고 데이터 extent만 CoW한다. toyfs는 단순화를 위해 full CoW 채택. 자세한 건 `08-snapshot.md`.

### 7.1 btrfs/ZFS와의 모델 차이 — 참고

btrfs와 ZFS는 toyfs와 다른 길로 같은 결과(시점 분리)를 낸다.

- **btrfs**: inode가 fs b-tree의 *leaf entry*로 들어감. inode 수정 = 그 leaf 블록을 CoW.
  부모 internal node도 CoW로 따라 다시 쓰고, 결국 fs tree 루트까지 → superblock의 root 포인터 교체로 끝.
  inode 번호는 변하지 않고, "그 번호의 데이터가 디스크 어디에 있는가"만 바뀜
- **ZFS**: dnode(=inode)가 dnode array의 한 entry. dnode 수정 = 그 dnode를 담은 블록을 CoW.
  indirect block, MOS, uberblock까지 모두 CoW. inode 번호 불변

두 FS는 inode table을 고정 위치 배열로 두지 않기 때문에 위 모델이 자연스럽다.
**toyfs는 inode table을 고정 배열로 두는 ext4 계열**이라 같은 방식이 안 통한다.
그래서 우리는 inode 슬롯 자체를 새로 할당하는 *full inode CoW*를 택했다.
inode 번호가 바뀌므로 **부모 디렉토리의 dentry까지 갱신**해야 하고, 이게 root까지 연쇄된다.
결과적으로 btrfs의 *path CoW*와 사실상 같은 일을 하지만, 메커니즘은 "트리 노드 CoW"가 아니라
"슬롯 재할당 + dentry 갱신"이라는 점이 핵심 차이.

## 8. Testing

### 8.1 Unit test
- `Inode.Marshal() / Unmarshal()` round-trip: 각 file type, 각 extent_depth, inline data 포함/미포함 케이스
- CRC 검증:
  - 정상 inode → 검증 통과
  - 페이로드 1바이트 변조 → 실패
- mode 해석:
  - mode = S_IFREG | 0644 → IsRegular(), Permission() == 0644
  - mode = S_IFDIR | 0755 → IsDir()
  - mode = S_IFLNK | 0777 → IsSymlink()
- nlink 카운트:
  - mkdir로 새 디렉토리 만들면 부모 nlink가 1 증가 (자식의 ".." 때문)
  - rmdir로 자식 디렉토리 제거하면 부모 nlink가 1 감소
  - 하드링크 생성/제거 시 대상 inode의 nlink 변화
- inline symlink:
  - size <= 60: inline_data에 직접 저장, blocks == 0
  - size > 60: extent로 저장, inline_data == 0
- 권한 체크:
  - root는 모드 0000이어도 통과
  - owner는 owner 비트만 검사
  - 다른 uid는 group/other 비트 분기
- 라이프사이클:
  - 할당된 inode 번호가 inode_bitmap에 set
  - 해제 후 free_inodes 카운트 증가 (근사값이지만)

### 8.2 Integration test (4단계 이후)
- `touch`, `mkdir`, `ln`, `ln -s`, `chmod`, `chown`, `rm`, `rmdir`을 FUSE mount 위에서 실행 후 inode 상태가 stat 결과와 일치
- `ls -l` 출력의 nlink가 hardlink 추가/제거에 따라 정확히 변동
- 짧은 symlink (`ln -s short /mnt/...`)와 긴 symlink (60자 초과) 둘 다 정상 동작

### 8.3 Crash recovery (5단계 이후)
- inode 할당 직후 crash → 다음 mount의 journal replay로 일관 복구
- 부분 갱신된 inode (checksum 안 맞는 상태)가 디스크에 남으면 mount 시 detect → journal replay로 정정

## 9. 다음 문서로의 안내

- `04-extent.md` — `extent_depth`, `extent_count`, `inline_extents`의 정확한 의미와 인덱스 블록 포맷
- `05-dentry-directory.md` — 디렉토리 inode가 가리키는 데이터 블록의 dentry 포맷
- `08-snapshot.md` — `snapshot_id`, `refcount`, `extent_generation`이 스냅샷 메커니즘에서 어떻게 동작하는지
- `09-links-permissions.md` — nlink, fast symlink, 권한 체크의 세부 시나리오
