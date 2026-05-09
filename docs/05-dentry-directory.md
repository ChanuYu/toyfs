# toyfs — Dentry & Directory

이 문서는 `toyfs`의 **디렉토리 표현**과 **dentry**(directory entry) on-disk 구조를 정의한다.

핵심 원칙: **디렉토리도 일반 파일이다.** mode가 `S_IFDIR`이고 데이터 영역에
dentry 배열이 들어있을 뿐, 데이터 위치 표현은 inode → extent → 데이터 블록의
일반 파일과 동일한 메커니즘을 쓴다.

## 1. 기본 단위

| 항목 | 값 |
|------|----|
| dentry 레코드 크기 | 264 B 고정 |
| 최대 파일명 길이 | 255 byte |
| 4 KiB 블록당 dentry 수 | 4096 / 264 = **15개** |
| `.`/`..` 처리 | 디스크에 명시 저장 |
| 삭제 처리 | inode==0 tombstone (컴팩션 없음) |
| 디렉토리 데이터 4 KiB 초과 | extent로 자연 확장 |

## 2. on-disk 포맷

```
struct toyfs_dentry {
    u32  inode;            // 가리키는 inode 번호. 0 = 빈 슬롯 (tombstone)
    u8   name_len;         // 실제 이름 길이 (1 ~ 255). inode==0이면 의미 없음
    u8   file_type;         // 파일 타입 힌트 (아래 'file_type' 절). lookup에서
                            // inode를 안 읽고도 type을 알 수 있게 캐싱
    u16  reserved;
    u8   name[256];        // 0-padded byte 배열. name_len 바이트만 유효, 나머지 0
};
```

크기: 4 + 1 + 1 + 2 + 256 = **264 B**.

> 256B로 padding한 이유: `name_len`을 1B로 두면 0~255만 표현 가능. 1B short(0)는 빈 이름이라 의미 없으므로 max 255B 이름. 이름을 256B 버퍼에 0-padding으로 저장하면 길이 비교 + memcmp만으로 lookup이 가능. ext2처럼 가변 길이를 쓰면 record 경계 계산 코드가 추가되는데 학습용으론 불필요.

### 2.1 file_type 힌트 값

```
TOYFS_FT_UNKNOWN = 0
TOYFS_FT_REG     = 1   // 일반 파일
TOYFS_FT_DIR     = 2   // 디렉토리
TOYFS_FT_LNK     = 3   // 심볼릭링크
```

`readdir`이 결과의 type을 반환할 때 이 캐시 덕에 inode를 따로 읽지 않아도 된다.
ext4의 `EXT4_FT_*`와 동일 패턴. 진실의 원천은 inode의 mode이며, 이 필드는
unlink/rename/chmod 시 inode와 동기화되어야 한다.

> 1단계에서는 *동기화 의무*만 약속하고, 일관성 검증은 fsck (별도 시점)에서.

### 2.2 빈 슬롯 (tombstone) 정의

`inode == 0`이면 그 슬롯은 빈 자리.
- name_len, file_type, name 모두 의미 없음
- readdir에서 무시
- 새 entry 추가 시 이 슬롯에 우선 채움 (compaction 없이 재사용)

## 3. 디렉토리 inode

디렉토리 inode는 `03-inode.md`의 일반 inode와 같지만 다음이 특수:

- `mode & S_IFMT == S_IFDIR`
- `size` = 디렉토리 데이터의 총 byte = `blocks × 4096` (자투리 136 B/블록 포함). 자세한 건 7.1절
- `nlink` 의미:
  - 자기 자신의 `.` → +1
  - 부모의 dentry → +1 (이 디렉토리를 가리키는 부모 entry)
  - 자식 디렉토리 N개의 `..` → +N
  - 즉 자식 dir이 K개일 때 `nlink = 2 + K`

> 일반 파일의 nlink = 하드링크 수와 의미가 다르다는 점에 주의. ext4도 동일.

## 4. dentry 배열 레이아웃

디렉토리 데이터 = `[entry 0][entry 1]...[entry N-1]`. 264B 단위로 빽빽이.

```
디렉토리 데이터 첫 블록 (4 KiB):
[0..263]      entry 0 = "."  (inode = self_ino)
[264..527]    entry 1 = ".." (inode = parent_ino)
[528..791]    entry 2
[792..1055]   entry 3
...
[3960..4095]  entry 14 (= 마지막)   ← 264 × 15 = 3960
```

블록 끝에 `4096 - 3960 = 136 B`의 자투리 공간이 남지만 한 entry(264B)가 들어가지 않으므로 0 패딩.

> 자투리 공간을 활용하지 않는 이유: entry record가 블록 경계에 걸치지 않게 두면 코드가 단순. 한 슬롯 읽기 = 한 블록만 읽기. ext4도 dentry가 블록 경계를 안 넘게 record 길이를 조정한다.

## 5. 4 KiB 초과 — extent로 자연 확장

15번째 entry 다음에 새 entry를 추가하면:
1. 디렉토리 inode의 extent에 새 데이터 블록 1개 append (04번 문서의 append/merge 정책)
2. 두 번째 블록의 첫 슬롯에 새 entry 기록
3. 디렉토리 inode `size += 4096`, `blocks += 1` (size 정책은 7.1절)

extent 자체는 일반 파일과 동일하게 동작. 디렉토리 전용 분기 없음.

> 디렉토리는 truncate 안 함. unlink가 누적되어 빈 슬롯이 많아져도 디렉토리 데이터 크기는 줄지 않는다. 빈 슬롯은 새 entry로 재사용. ext4와 동일.

## 6. lookup 알고리즘

목표: 디렉토리 inode `D` 안에서 이름 `name`을 가진 entry 찾기.

```
func lookup(D: *Inode, name: string) (*Dentry, error):
    if len(name) == 0 || len(name) > 255: return EINVAL
    if (D.mode & S_IFMT) != S_IFDIR: return ENOTDIR

    blocks = D.size / 4096   // 디렉토리가 점유한 블록 수 (264 × 15가 4096보다 작아 자투리 있음)
    for blk in 0..blocks-1:
        physical = extent_lookup(D, blk)    // 04번 문서의 검색 알고리즘
        data = block_cache.read(physical)   // 06번 문서
        for slot in 0..14:                  // 블록당 15 슬롯
            entry = data[slot * 264 : slot * 264 + 264]
            if entry.inode == 0: continue   // tombstone
            if entry.name_len == len(name) &&
               bytes.Equal(entry.name[:entry.name_len], name):
                return entry, nil
    return ENOENT
```

비용: O(N), N = 전체 dentry 슬롯 수. 합의된 단순성. 디렉토리당 entry가 수만 개 이상 가는 워크로드는 학습 범위 밖.

## 7. insert 알고리즘 (새 entry 추가)

목표: dentry `e`를 디렉토리 `D`에 추가.

```
func insert(D: *Inode, e: Dentry) error:
    // 1) 동일 이름 충돌 검사
    if exists, _ := lookup(D, e.name); exists != nil: return EEXIST

    // 2) 빈 슬롯 찾기
    blocks = D.size / 4096
    for blk in 0..blocks-1:
        physical = extent_lookup(D, blk)
        data = block_cache.read(physical)
        for slot in 0..14:
            entry = data[slot * 264 : slot * 264 + 264]
            if entry.inode == 0:
                write e to that slot
                mark block dirty
                update D.ctime, D.mtime
                return OK

    // 3) 빈 슬롯 없음 → 디렉토리 끝에 append
    extent_append(D, 1 block)              // 04번 promote/merge 발생 가능
    write e to slot 0 of new block
    D.size += 4096                         // 한 블록 추가 (자투리 136 B 포함)
    D.blocks += 1
    return OK
```

### size 갱신 정책

- 새 블록을 할당했을 때 `D.size`를 `+ 4096`으로 키울지, `+ 264`로 키울지가 모호
- 우리 정책: **`D.size`는 "데이터 영역의 끝까지 byte 수" = 항상 `blocks × 4096`**
- readdir은 `D.size / 4096`의 블록 수를 기준으로 순회하고, 한 블록 안 슬롯은 0~14를 모두 탐색 (빈 슬롯 skip)
- 단순화의 대가로 `stat()` 결과의 size가 "사용 중인 entry 수 × 264"가 아님. ext4도 비슷하게 size가 의미 있는 값은 아님 (`fstat`에서 디렉토리 size는 구현 정의)

## 8. delete 알고리즘 (unlink/rmdir)

```
func delete(D: *Inode, name: string) error:
    found = lookup(D, name)
    if found == nil: return ENOENT
    if found is dir & dir not empty: return ENOTEMPTY  // rmdir 한정

    found.inode = 0           // tombstone
    // name_len, file_type, name은 그대로 둬도 무방 (의미 없음)
    mark block dirty
    update D.ctime, D.mtime

    decrement target inode's nlink
    if target was dir:
        decrement D.nlink     // 자식의 ".." 사라짐
    return OK
```

추가:
- target inode의 `nlink`가 0이 되고 `refcount`도 0이고 OpenCount도 0이면 → inode 해제 (03번 5.3)
- rmdir의 "비어있음" 검사는 디렉토리 내 dentry 중 `.`/`..` 외에 inode != 0인 entry가 있는지

## 9. rename 알고리즘 (개요)

같은 디렉토리 안 rename(`/a/foo` → `/a/bar`):
1. lookup(`a`, `foo`) → entry E
2. lookup(`a`, `bar`) → 충돌이면 처리 정책 (1단계는 EEXIST 반환, replace 안 함)
3. E의 name 영역을 `bar`로 덮어쓰기, name_len 갱신
4. file_type 그대로

다른 디렉토리 across rename(`/a/foo` → `/b/foo`):
1. delete(`a`, `foo`) — tombstone
2. insert(`b`, foo entry) — `b`에 새로 기록
3. foo가 디렉토리라면:
   - `a.nlink--`, `b.nlink++` (자식의 `..` 변동)
   - 자식의 `..` entry inode 번호를 b의 inode로 갱신 (자식 디렉토리 데이터 수정)

> rename은 한 transaction으로 묶음. 부분 실패 시 journal replay로 일관 복구. 자세한 건 `07-journal.md`.

## 10. mkdir / rmdir

### 10.1 mkdir (path P)

```
1. parent = lookup(parent of P)
2. ensure name 사용 가능 (lookup → ENOENT)
3. 새 inode 할당 (S_IFDIR | mode)
4. 새 inode의 extent로 데이터 블록 1개 할당
5. 그 블록에 . / .. dentry 2개 기록 (블록 첫 528 B). 나머지 13 슬롯은 inode==0
6. 새 inode size = 4096, blocks = 1, nlink = 2 (. + parent의 entry)
7. 부모 디렉토리에 P의 마지막 컴포넌트 entry 추가 (insert)
8. parent.nlink++ (새 자식 dir의 ".." 때문)
9. 모두 한 transaction
```

### 10.2 rmdir (path P)

```
1. inode = lookup(P)
2. mode가 dir이 아니면 ENOTDIR
3. 디렉토리 비어있는지 검사 (.과 .. 외 valid entry 없음)
4. parent에서 P의 entry tombstone (delete)
5. parent.nlink-- (자식의 ".." 사라짐)
6. 디렉토리 inode의 데이터 블록 free, inode 해제
7. 모두 한 transaction
```

## 11. invariant

- `.` entry의 inode == 디렉토리 자기 자신의 inode 번호
- `..` entry의 inode == 부모 디렉토리의 inode 번호 (root는 자기 자신을 가리킴, 즉 root.`..` == 1)
- `.`/`..`은 항상 첫 블록의 entry 0/1 슬롯에 위치
- 한 디렉토리 안에 같은 이름의 valid entry 없음
- file_type과 inode.mode 일치 (fsck 검증 대상)
- valid entry 수 + tombstone 수 == 슬롯 총합 (= blocks × 15)

## 12. in-memory 표현

```go
type Dentry struct {
    Inode    uint32
    NameLen  uint8
    FileType uint8
    _        uint16
    Name     [256]byte
}

// 디렉토리 작업용 헬퍼: 한 블록을 슬롯 단위로 본 뷰
type DirBlockView struct {
    Block    *block.CachedBlock  // 06번 문서
    Slots    [15]*Dentry         // 슬롯 i = block[i*264 : (i+1)*264] 해석본
}
```

`Dentry.Name`은 항상 256 byte. 이름이 짧으면 뒤가 0. `string(name[:name_len])`으로 사용.

## 13. Testing

### 13.1 Unit test

dentry round-trip:
- name 길이 1, 100, 255인 dentry를 marshal → unmarshal 했을 때 동일
- inode==0 (tombstone)인 dentry 직렬화/역직렬화

lookup:
- 빈 디렉토리(`.`, `..`만) → 다른 이름 lookup → ENOENT
- 첫 블록에 14개 entry → 모든 이름이 정확히 lookup
- 두 번째 블록까지 확장 (16+ entries) → 두 번째 블록 entry도 lookup 성공
- tombstone 슬롯 사이의 entry 정상 lookup
- 동일 prefix 다른 길이 (`abc` vs `abcd`) — 정확히 구분

insert:
- 빈 슬롯 재사용: 슬롯 5에 unlink → 새 insert 시 슬롯 5에 들어감 (블록 0 우선)
- 중복 이름 → EEXIST
- 첫 블록 가득 차면 두 번째 블록 자동 할당 + size/blocks 증가

delete:
- 정상 entry → tombstone, target nlink 감소
- 존재하지 않는 이름 → ENOENT
- 디렉토리 unlink 시도 → 별도 rmdir 경로 (unlink는 EISDIR)

mkdir/rmdir:
- mkdir 후 디렉토리에 `.`/`..` 정확히 존재, 부모 nlink +1, 자기 nlink == 2
- 비어있는 디렉토리 rmdir → 부모 nlink -1, inode 해제
- 비어있지 않은 디렉토리 rmdir → ENOTEMPTY

rename:
- 같은 dir 안 rename → name만 변경, inode 동일
- 다른 dir로 rename — 일반 파일 / 디렉토리 두 케이스 다 검증
- 디렉토리 cross-dir rename 후 자식의 `..`가 새 부모 inode를 가리키는지

invariant fuzz:
- 임의의 mkdir/rmdir/touch/unlink/rename 시퀀스 100~1000번 → 매 단계마다 11번 절 invariant 검증

### 13.2 Integration test (4단계 이후)
- `mkdir`, `ls`, `rmdir`, `mv`, `rm`, `touch`를 FUSE 마운트 위에서 실행
- `ls -la`의 nlink 값 검증 (자식 dir 추가/제거에 따라 정확히 변동)
- `find /mnt -type d` 같은 deep traversal이 모든 디렉토리를 찾는지
- 디렉토리에 entry 1000개 만들어서 lookup latency가 일정한지 (선형이므로 N에 비례하지만 O(N))

### 13.3 Crash recovery (5단계 이후)
- mkdir 도중 crash (parent insert 후 child inode 초기화 전) → journal replay로 복구
- rename across dir 도중 crash (delete 후 insert 전) → orphan 없이 일관 상태로 복구
- delete 직후 crash → tombstone이 디스크에 정상 반영

## 14. 다음 문서로의 안내

- `06-block-cache.md` — 디렉토리 데이터 블록 read/write가 cache를 거치는 경로
- `07-journal.md` — mkdir/rmdir/rename이 한 transaction으로 묶이는 방식
- `08-snapshot.md` — 디렉토리도 CoW 대상. 스냅샷 시 dentry 영역이 어떻게 공유/분기되는지
- `09-links-permissions.md` — 하드/심볼릭 링크가 dentry 차원에서 어떻게 표현되는지
