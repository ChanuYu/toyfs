# toyfs — Links & Permissions

이 문서는 `toyfs`의 **하드링크, 심볼릭링크, POSIX 권한 체크**를 한 곳에 정리한다.
03번 inode와 05번 dentry 문서의 산재된 약속을 묶어 일관된 view로 제시.

핵심 원칙:
- **하드링크**: 같은 inode를 여러 dentry가 가리킴. nlink로 추적
- **심볼릭링크**: 별도 file type. inode가 *경로 문자열*을 데이터로 보유 (또는 inline)
- **권한**: POSIX 단순 모델 (mode bits + uid/gid). ACL/capability 없음
- **snapshot의 refcount와 nlink는 다른 축**

## 1. 하드링크

### 1.1 정의

같은 inode를 두 개 이상의 dentry가 가리키는 상태. POSIX 명령으로는 `ln source target`.

### 1.2 자료구조

dentry는 inode 번호를 가짐 (05번 2절). 하드링크는 *별개의 dentry record*가 같은 inode 번호를 가리키는 것:

```
/a/file1  →  dentry { inode=42, name="file1", file_type=REG }
/b/file2  →  dentry { inode=42, name="file2", file_type=REG }
                       ↓
                 inode 42 (nlink=2)
```

inode 자체는 *어떤 dentry가 자기를 가리키는지* 모름. nlink 카운트만 들고 있음.

### 1.3 nlink 의미

03번 3.3절 정리:
- **일반 파일**: 그 파일을 가리키는 dentry 수 = 하드링크 수
- **디렉토리**: 자기 `.` (1) + 부모의 dentry (1) + 자식 디렉토리 N개의 `..` (N) = `2 + N`
- **심볼릭링크**: 그 링크를 가리키는 dentry 수 (링크 *대상*과 무관)

### 1.4 link 알고리즘 (`ln source target`)

```
func Link(source_path, target_path):
    src_inode := lookup(source_path)
    if src_inode == nil: return ENOENT
    if src_inode.mode is dir: return EPERM   // 디렉토리 하드링크 금지

    // target의 부모 디렉토리에 새 dentry 추가
    target_parent, target_name := split(target_path)
    target_parent_inode := lookup(target_parent)

    if exists(target_parent_inode, target_name): return EEXIST

    new_dentry := Dentry{
        inode:     src_inode.num,
        name_len:  len(target_name),
        file_type: file_type_of(src_inode.mode),
        name:      target_name (0-padded),
    }
    insert(target_parent_inode, new_dentry)         // 05번 7절

    src_inode.nlink++
    src_inode.ctime = now
    target_parent_inode.mtime = now
    target_parent_inode.ctime = now

    // 한 transaction으로 journal commit
```

> **디렉토리 하드링크 금지**: POSIX는 디렉토리 하드링크를 허용하지 않음 (loop 가능성). ext4/btrfs/ZFS 모두 동일. 우리도 EPERM 반환.

### 1.5 unlink와 inode 해제

```
func Unlink(path):
    parent_inode := lookup(parent of path)
    name := basename(path)
    target_inode := lookup(parent_inode, name)
    if target_inode == nil: return ENOENT
    if target_inode.mode is dir: return EISDIR   // rmdir 사용

    delete(parent_inode, name)                    // 05번 8절: tombstone
    target_inode.nlink--
    target_inode.ctime = now

    // inode 해제 조건 (03번 5.3절):
    if target_inode.nlink == 0 &&
       target_inode.refcount == 0 &&
       target_inode.OpenCount == 0:
        free_inode(target_inode)                  // extent 따라가며 모든 데이터 블록 free
```

자세한 free 조건은 03번 3.8절 참고.

### 1.6 nlink와 refcount의 차이 (재확인)

03번 3.8절에서 약속한 두 축:

| 축 | 의미 | 증가 시점 | 감소 시점 |
|---|---|---|---|
| `nlink` | **사용자 가시성** dentry 수 | 하드링크 생성 (link 호출) | unlink |
| `refcount` | **시스템 내부** (활성 + snapshot) 가리키는 수 | snapshot 생성 | snapshot 삭제 |

inode가 free 가능하려면 *둘 다 0 + open file 0*.

## 2. 심볼릭링크

### 2.1 정의

경로 문자열을 보유하는 특수 파일. read 시점에 그 경로가 *해석*됨. POSIX 명령으로는 `ln -s target linkname`.

심볼릭링크는 하드링크와 달리:
- *경로*를 가리킴 (= 문자열). 대상이 없어도 만들 수 있음 (dangling)
- *디렉토리 가리키기 가능* (loop 위험은 lookup 깊이 제한으로 회피)
- 파일시스템 경계 넘기 가능

### 2.2 자료구조 — fast vs slow symlink

03번 3.9절 결정:

**fast symlink (size <= 60 byte)**:
- inode의 `inline_data[60]`에 타깃 경로를 0-terminator 없이 직접 저장
- `extent_depth = 0`, `extent_count = 0`, blocks = 0
- inode 한 슬롯 안에 모든 정보 → 데이터 블록 안 씀

**slow symlink (size > 60 byte)**:
- 일반 파일과 동일하게 extent로 데이터 블록에 저장
- `extent_depth = 0`, `extent_count = 1`, `inline_extents[0] = {...}` 등
- 1개 extent로 충분 (긴 경로도 4 KiB 안에 들어감)

inode의 `flags` 비트 `TOYFS_INODE_INLINE_SYM` (03번 3.2)이 fast를 식별.

### 2.3 symlink 생성

```
func Symlink(target_path, link_path):
    parent, name := split(link_path)
    parent_inode := lookup(parent)
    if exists(parent_inode, name): return EEXIST

    new_inode := allocate_inode()
    new_inode.mode = S_IFLNK | 0777    // symlink mode는 보통 0777 (대상 권한이 진짜)
    new_inode.uid = caller_uid
    new_inode.gid = caller_gid
    new_inode.size = len(target_path)
    new_inode.nlink = 1
    new_inode.atime = new_inode.mtime = new_inode.ctime = new_inode.crtime = now

    if len(target_path) <= 60:
        // fast symlink
        new_inode.inline_data[:len(target_path)] = target_path
        new_inode.flags |= TOYFS_INODE_INLINE_SYM
        new_inode.extent_depth = 0
        new_inode.extent_count = 0
        new_inode.blocks = 0
    else:
        // slow symlink
        block := allocate_data_block()
        write block: target_path (0-padded to 4096)
        new_inode.inline_extents[0] = Extent{
            ee_block:    0,
            ee_physical: block,
            ee_len:      1,
        }
        new_inode.extent_depth = 0
        new_inode.extent_count = 1
        new_inode.blocks = 1

    new_dentry := Dentry{
        inode:     new_inode.num,
        name_len:  len(name),
        file_type: TOYFS_FT_LNK,
        name:      name,
    }
    insert(parent_inode, new_dentry)
    parent_inode.mtime = parent_inode.ctime = now

    // 한 transaction으로 commit
```

### 2.4 readlink — 경로 반환

```
func Readlink(path) (target string, error):
    inode := lookup(path)
    if inode == nil: return "", ENOENT
    if inode.mode is not S_IFLNK: return "", EINVAL

    if inode.flags & TOYFS_INODE_INLINE_SYM:
        return string(inode.inline_data[:inode.size]), nil
    else:
        block := inode.inline_extents[0].ee_physical
        data := block_cache.read(block)
        return string(data[:inode.size]), nil
```

### 2.5 path traversal 시 symlink 해석

`open("/a/link/file")` 같은 경로 해석에서 `link`가 symlink면:
1. readlink로 타깃 경로 얻음
2. 타깃이 절대경로면 root부터, 상대경로면 *symlink가 있던 디렉토리*에서 시작
3. 새 경로로 traversal 계속
4. 또 symlink면 재귀적으로 해석

**무한 루프 방지**: 한 번 lookup에서 따라간 symlink 수가 임계치(예: 40)를 넘으면 `ELOOP` 반환. POSIX/Linux 표준.

```
func Lookup(path) (inode, error):
    return lookupRec(path, 0)

func lookupRec(path, depth) (inode, error):
    if depth > 40: return nil, ELOOP

    cur := root_inode
    for component in path.split('/'):
        next := lookup_in_dir(cur, component)
        if next == nil: return nil, ENOENT

        if next.mode is S_IFLNK:
            target, err := readlink(next)
            if err != nil: return nil, err
            // 절대경로면 root부터, 아니면 cur에서 다시 시작
            base := root_inode if target.startswith('/') else cur
            // 남은 path 이어붙여 재귀
            remaining := join(target, rest_of_path_after(component))
            return lookupRec(remaining, depth+1)

        cur = next
    return cur, nil
```

### 2.6 symlink와 권한

symlink 자체의 mode bits는 **무시됨** (POSIX 표준). 보통 0777로 둠. 실제 권한 체크는 *symlink가 가리키는 대상*에 적용.

owner/group은 symlink 본체의 것 — `chown`은 가능하지만 의미는 작음.

## 3. POSIX 권한

### 3.1 모델 — mode bits + uid/gid

03번 3.1, 3.7절 정리:

inode가 들고 있는 정보:
- `mode` 16-bit: 상위 4 bit는 file type (S_IFREG/S_IFDIR/S_IFLNK), 하위 12 bit는 permission
- `uid` 32-bit: owner user ID
- `gid` 32-bit: owner group ID

permission 12 bit:
```
9 8 7   6 5 4   3 2 1   0  (bit 인덱스)
S G T   r w x   r w x   r w x
│ │ │   │ │ │   │ │ │   │ │ └ other execute
│ │ │   │ │ │   │ │ │   │ └── other write
│ │ │   │ │ │   │ │ │   └──── other read
│ │ │   │ │ │   │ │ └──────── group execute
│ │ │   │ │ │   │ └────────── group write
│ │ │   │ │ │   └──────────── group read
│ │ │   │ │ └──────────────── owner execute
│ │ │   │ └────────────────── owner write
│ │ │   └──────────────────── owner read
│ │ └──────────────────────── sticky bit
│ └────────────────────────── setgid
└──────────────────────────── setuid
```

1단계는 owner/group/other rwx 9비트만 의미를 가짐. setuid/setgid/sticky는 *저장은 하되 동작은 무시* (학습 단순화).

### 3.2 access 알고리즘

03번 6절 그대로:

```
func Access(inode, want) error:
    // want는 R_OK(4) | W_OK(2) | X_OK(1)의 조합

    if caller.uid == 0:
        // root는 모든 access 통과 (단, executable이 없는 파일에 X_OK는 거부 — Linux 동작)
        if want & X_OK and (inode.mode & 0o111) == 0:
            return EACCES
        return OK

    var perm uint16
    if caller.uid == inode.uid:
        perm = (inode.mode >> 6) & 0o7    // owner bits
    elif caller.gid == inode.gid:
        perm = (inode.mode >> 3) & 0o7    // group bits
    else:
        perm = inode.mode & 0o7            // other bits

    if (want & R_OK) and !(perm & 4): return EACCES
    if (want & W_OK) and !(perm & 2): return EACCES
    if (want & X_OK) and !(perm & 1): return EACCES
    return OK
```

### 3.3 ops에서 access 호출

각 ops가 어떤 권한을 요구하는지:

| ops | 어디에 어떤 권한? |
|---|---|
| open(O_RDONLY) | 파일에 R_OK |
| open(O_WRONLY) | 파일에 W_OK |
| open(O_RDWR) | 파일에 R_OK + W_OK |
| read | 이미 open된 fd. 추가 검사 없음 |
| write | 이미 open된 fd. 추가 검사 없음 |
| stat / lstat | 부모 디렉토리에 X_OK |
| readdir | 디렉토리에 R_OK |
| 디렉토리 진입 (lookup의 각 component) | 그 디렉토리에 X_OK |
| create | 부모에 W_OK + X_OK |
| unlink | 부모에 W_OK + X_OK |
| mkdir | 부모에 W_OK + X_OK |
| rmdir | 부모에 W_OK + X_OK |
| rename | 양 부모에 W_OK + X_OK |
| chmod | inode owner이거나 root |
| chown | root만 (POSIX 단순화) |
| readlink | 보통 검사 안 함 (LINK 자체 권한과 무관) |

### 3.4 chmod / chown

```
func Chmod(path, new_mode):
    inode := lookup(path)
    if caller.uid != 0 && caller.uid != inode.uid: return EPERM
    inode.mode = (inode.mode & 0xF000) | (new_mode & 0x0FFF)   // file type 보존
    inode.ctime = now
    // transaction commit

func Chown(path, new_uid, new_gid):
    inode := lookup(path)
    if caller.uid != 0: return EPERM   // 단순화: root만 가능
    if new_uid != -1: inode.uid = new_uid
    if new_gid != -1: inode.gid = new_gid
    inode.ctime = now
    // transaction commit
```

POSIX는 owner도 자기 파일의 group을 자기 보조 그룹으로 변경 가능하지만 1단계에서는 root 전용으로 단순화.

### 3.5 caller 정보 — FUSE에서 어떻게 얻는가

FUSE는 `fuse_context`에서 caller의 uid/gid/pid를 제공:
- `bazil.org/fuse`: `fs.Intr` / context에서 추출
- `hanwen/go-fuse`: `nodefs.Context.Caller`

1단계 구현은 이걸 그대로 사용. multi-thread 상황에서 context는 ops마다 다를 수 있지만 우리 single-thread 가정이라 한 번에 하나만 처리.

> **default uid/gid**: FUSE 마운트 시 `default_permissions` 옵션을 *주지 않으면* 권한 검사가 커널에서가 아니라 우리 코드에서 일어남. 우리 access 알고리즘이 진실의 원천. 자세한 건 4단계 FUSE 통합 시 확인.

## 4. snapshot과 link/permission의 상호작용

### 4.1 snapshot 시점에 link

snapshot 생성 후:
- 활성 FS에서 새 하드링크 생성 → nlink 변경. 활성 inode는 *수정됨* → full inode CoW (03번 7절)
- snapshot 입장에선 자기 inode_table 사본의 옛 nlink 그대로

→ snapshot mount 시 readdir로 본 nlink는 *snapshot 시점*의 값. 활성에서 add/remove한 link는 안 보임.

### 4.2 snapshot 시점에 chmod

snapshot 후 chmod → 활성 inode 수정 → CoW → 활성 inode_table에 새 슬롯 (또는 mode만 바뀐 새 inode). snapshot은 옛 mode 그대로.

이게 snapshot의 핵심 보장: **권한 변경도 시점이 분리됨.**

### 4.3 nlink 0 + snapshot이 가리키는 경우

활성에서 unlink로 nlink가 0이 됐는데 snapshot이 그 inode를 가리키고 있다면 (= snapshot의 inode_table 사본에 그 inode 슬롯이 valid):

- inode는 *활성 inode_table에서 free 가능* (활성 dentry 어디에도 없음)
- 그러나 inode 슬롯의 *물리적 메모리/디스크 영역*은 활성 inode_table에 그대로 있음 (활성에서 그 슬롯을 새 inode로 덮어써도 OK)
- snapshot은 *자기 사본*의 그 슬롯을 통해 옛 inode를 봄

03번 3.8절의 free 조건 `nlink == 0 && refcount == 0 && OpenCount == 0`이 여기서 의미를 가짐:
- snapshot이 가리키면 *활성 inode 자체*는 즉시 free되지만
- **데이터 블록의 free**는 refcount table을 따로 봐야 함 (snapshot이 데이터 블록을 가리키면 refcount > 0)

> 데이터 블록 free와 inode 슬롯 재사용은 서로 다른 메커니즘. inode 슬롯은 활성 bitmap만 보고 결정, 데이터 블록은 refcount table을 봄.

## 5. 구현 우선순위 (1단계)

다음 구현 우선순위로 단계적 진행:

### 5.1 핵심 (mvp)
- 일반 파일/디렉토리/symlink 3종 file type
- mode bits 9개 (rwx × 3) + uid/gid 저장
- access 알고리즘 (root/owner/group/other 분기)
- 하드링크 (link/unlink/nlink)
- fast/slow symlink (readlink, path traversal)

### 5.2 추가
- chmod (owner 또는 root)
- chown (root 전용)
- 디렉토리 진입 시 X_OK 검사
- ELOOP (symlink 깊이 40)

### 5.3 1단계 범위 밖 (미구현)
- setuid/setgid/sticky 동작 (저장은 하지만 effect 없음)
- ACL
- capability
- POSIX `faccessat` 등 추가 ops (필요 시 access로 wrap)
- cross-FS link (toyfs ↔ 호스트 FS)

## 6. invariant

mount 중:
1. 일반 파일/symlink의 nlink는 그 inode를 가리키는 dentry 수와 일치
2. 디렉토리의 nlink == 2 + (자식 디렉토리 수)
3. 디렉토리 하드링크 없음 (즉 일반 dentry로 같은 디렉토리 inode 가리키는 일 없음. 자식의 `..`는 별개)
4. fast symlink: `inode.size <= 60 && flags & INLINE_SYM != 0 && blocks == 0`
5. slow symlink: `inode.size > 60 && extent_count == 1 && blocks == 1`
6. mode의 file type bits와 dentry의 file_type 일치

snapshot 후:
7. snapshot의 inode_table 사본의 nlink는 *생성 시점*의 값 그대로
8. 활성에서 link/unlink는 활성 inode_table에만 영향, snapshot 사본 무관

## 7. Testing

### 7.1 Unit test

하드링크:
- ln source target → target.inode == source.inode, source.nlink += 1
- unlink target → source.nlink -= 1, target dentry tombstone
- 두 dentry 모두 unlink → inode 해제 (free 조건 만족 시)
- 디렉토리에 대한 link 시도 → EPERM
- target이 이미 존재 → EEXIST
- source가 없음 → ENOENT

심볼릭링크:
- fast symlink (path 길이 < 60) → inline_data에 저장, blocks == 0
- 정확히 60 byte path → fast (경계값)
- 61 byte path → slow, blocks == 1
- readlink → 정확한 경로 반환 (양쪽 모드 다)
- dangling symlink (대상 없음) → readlink는 OK, open은 ENOENT
- symlink로의 chmod → mode 변경 (의미는 없지만 저장됨)
- symlink loop (a → b → a) → ELOOP
- 깊이 40 symlink chain → ELOOP

권한:
- root는 0000 mode 파일도 read/write 가능
- owner는 owner bits만 검사
- group은 owner와 다른 uid에서만 group bits 검사
- 0644 파일을 다른 uid가 write 시도 → EACCES
- 0755 디렉토리를 다른 uid가 lookup → 진입 가능 (X_OK)
- 0644 디렉토리를 다른 uid가 lookup → EACCES (X_OK 없음)
- chmod owner 외 → EPERM
- chown non-root → EPERM

snapshot 상호작용:
- snapshot 후 hardlink 추가 → snapshot의 nlink 그대로, 활성은 +1
- snapshot 후 chmod → snapshot의 mode 그대로, 활성은 변경값
- nlink 0 + snapshot 가리킴 → inode 슬롯은 활성에서 free, 데이터 블록은 refcount > 0이면 유지

### 7.2 Integration test (4단계 이후)
- `ln a b` 후 `ls -l a b` → nlink 둘 다 2
- `ln -s` 후 `readlink` (POSIX 명령), `ls -l` 출력 정확
- `chmod 600 file && cat file` (다른 uid로) → permission denied
- 깊은 symlink 체인 cd로 따라가기 → ELOOP

### 7.3 Crash recovery (5단계)
- link 도중 crash → nlink 갱신/dentry 추가가 atomic으로 같이 일어나거나 같이 안 일어남
- chmod 도중 crash → 부분 적용 없음
- symlink 생성 도중 crash → inode + extent + dentry 일관

## 8. 다음 문서로의 안내

- `10-crash-recovery.md` — link/unlink/chmod transaction의 crash 시나리오
- `11-roadmap.md` — 5.1 ~ 5.3 우선순위가 2~5단계 작업에 어떻게 매핑되는지
