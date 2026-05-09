# toyfs — Roadmap

이 문서는 1단계(설계 문서) 이후 **2~5단계 구현 계획**을 정리한다.
00번 5절의 단계 정의를 구체적 작업 단위로 분해.

## 0. 단계 요약

| 단계 | 목표 | 주요 산출물 |
|------|------|-------------|
| 1 (완료) | 구조 설계 | `docs/00~10` (이 문서들) |
| 2 | 기본 ops 구현 | `pkg/` 패키지 + `cmd/mkfs` |
| 3 | unit test + 디버그 도구 | 각 패키지의 `_test.go` + `cmd/toyfs-dump` |
| 4 | FUSE 통합 + integration test | `cmd/mount` + `test/integration` |
| 5 | snapshot/recovery + crash test | snapshot 패키지 + `test/crash` |
| 6 (생략) | Linux VFS 연동 | (다음 프로젝트) |

## 1. 2단계 — 기본 ops 구현

### 1.1 디렉토리 구조

00번 6절에서 예정한 구조:

```
toyfs/
├── go.mod
├── docs/                   ✓ 1단계 완료
├── cmd/
│   ├── mkfs/               2단계 시작 (2.H)
│   ├── toyfs-dump/         3단계 (3.2)
│   ├── mount/              4단계 (4.A)
│   └── toyfs-snap/         5단계 (5.B)
├── pkg/
│   ├── block/              2.A
│   ├── superblock/         2.B
│   ├── inode/              2.C
│   ├── extent/             2.D
│   ├── dentry/             2.E
│   ├── journal/            2.F
│   ├── fs/                 2.G (위 모듈 묶는 ops 레이어)
│   └── snapshot/           5.A (snapshot/CoW/rollback)
└── test/
    ├── integration/        4단계
    └── crash/              5단계
```

### 1.2 작업 순서 (2.A → 2.G)

각 패키지는 *위에서 아래로 빌드*. 아래 패키지는 위 패키지에 의존:

#### 2.A `pkg/block` — block device + cache

선행 의존: 없음
산출물:
- `BlockDevice` (5.x: 06번 5절)
- `BlockCache` + `CachedBlock` (06번 2~3절)
- Get/Put/MarkDirty/Flush/JournalLock 등 API
- direct I/O는 인터페이스만 (06번 5.1)

검증:
- in-memory mock disk로 단위 동작 확인
- 256 슬롯 LRU 동작
- JournalLock된 슬롯 evict skip

#### 2.B `pkg/superblock`

선행: 2.A
산출물:
- on-disk struct serialization (02번 2절)
- magic/version/CRC 검증
- mkfs 시 영역 산정 함수
- mount/unmount 흐름 (02번 6절)

검증:
- Marshal/Unmarshal round-trip
- CRC 변조 감지
- geometry invariant 검증 함수

#### 2.C `pkg/inode`

선행: 2.A, 2.B
산출물:
- 256 B inode struct (03번 2절)
- 슬롯 위치 계산 (03번 1절)
- 할당/해제 (03번 5.1, 5.3)
- in-memory `Inode` 구조 + dirty 추적
- access 알고리즘 (03번 6절)

#### 2.D `pkg/extent`

선행: 2.A, 2.C
산출물:
- leaf/index entry 16 B 직렬화 (04번 2~3절)
- ExtentNode 직렬화 (04번 4절)
- 검색 알고리즘 (04번 7절)
- append/split/promote (04번 8절)
- truncate (04번 9절)
- 단순 first-fit 할당기 (04번 11절)

검증:
- depth 0/1/2 트리 round-trip
- invariant fuzz (04번 6절 7가지)

#### 2.E `pkg/dentry`

선행: 2.A, 2.C, 2.D
산출물:
- dentry 264 B 직렬화 (05번 2절)
- lookup/insert/delete (05번 6~8절)
- mkdir/rmdir/rename (05번 9~10절)
- 디렉토리 데이터 = 일반 파일 → extent 사용

검증:
- invariant (05번 11절)
- 빈 슬롯 재사용
- 두 번째 블록 자동 확장

#### 2.F `pkg/journal`

선행: 2.A, 2.B
산출물:
- journal superblock + 4종 record (07번 3~4절)
- escape 처리 (commit/replay 양쪽)
- Transaction 라이프사이클 (07번 5절)
- ordered 강제 — DirtyInodes flush (07번 6절)
- replay 4-pass (07번 9절)

검증:
- commit/replay round-trip
- escape 발생 케이스
- revocation 시나리오 (07번 8절)
- 미완 transaction 무시

#### 2.G `pkg/fs` — ops 레이어

선행: 2.A ~ 2.F 모두
산출물:
- POSIX-like ops 함수 (Lookup, Open, Read, Write, Create, Unlink, Mkdir, Rmdir, Rename, Truncate, Chmod, Chown, Stat, Readdir, Link, Symlink, Readlink)
- 각 ops가 transaction을 시작/commit
- 권한 체크 통합 (09번)

검증: 3단계에서 본격 unit test.

#### 2.H `cmd/mkfs`

선행: 2.A~2.F (G는 불필요)
산출물:
- CLI: `mkfs.toyfs <image> [-s size] [-N inodes] [-J journal_size]`
- 영역 산정 + 0 채움 + 각 영역 초기화
- root inode 1번 + `.`/`..` 생성
- backup superblock 작성

검증:
- 다양한 크기로 mkfs → toyfs-dump (3단계)로 검증

### 1.3 2단계 완료 기준

- mkfs로 64 MiB 이미지 생성 가능
- in-process API로 (= FUSE 없이) `Mkdir/Touch/Write/Read/Unlink` 등 호출 가능
- in-memory mock disk를 BlockDevice로 갈아끼워도 동작
- 모든 ops가 한 transaction으로 묶여 journal에 기록됨

## 2. 3단계 — unit test + 디버그 도구

### 2.1 테스트 작성 우선순위

각 패키지의 `*_test.go`. 우선순위는 **하위 → 상위**:

1. `pkg/block` — cache LRU, dirty flush, JournalLock pin
2. `pkg/superblock` — 직렬화 round-trip, CRC
3. `pkg/inode` — 직렬화, mode 해석, access
4. `pkg/extent` — depth별 round-trip, append/split/promote, invariant fuzz
5. `pkg/dentry` — lookup/insert/delete, mkdir/rmdir, invariant
6. `pkg/journal` — commit/replay, escape, revocation
7. `pkg/fs` — ops 통합. 위 6개가 갖춰진 후

각 패키지의 testing 시나리오는 해당 docs 문서의 testing 절 참고:
- 06번 §10
- 02번 §8
- 03번 §8
- 04번 §13
- 05번 §13
- 07번 §11
- 09번 §7

### 2.2 cmd/toyfs-dump

선행: 2단계 완료
산출물:
- CLI: `toyfs-dump <image> [-o type] [--snapshot ID]`
- 출력 종류:
  - `super`: superblock 모든 필드 사람 읽기 형태
  - `bitmap`: block/inode bitmap 비트별
  - `inode <num>`: inode 슬롯 모든 필드 + extent tree 펼침
  - `dir <inode>`: 디렉토리 inode의 dentry 리스트
  - `journal`: journal 영역의 transaction 목록 + 각 record
  - `refcount`: refcount table 비-zero 엔트리
  - `snapshot <id>`: snapshot_meta 슬롯 전체

검증:
- mkfs 직후 dump → 사람 검수 가능한 출력
- 각 ops 후 dump 비교로 변경 추적

### 2.3 3단계 완료 기준

- 모든 핵심 ops에 *happy path + 주요 에러 경로* unit test
- toyfs-dump로 디스크 상태 검수 가능
- in-memory mock으로 high-volume fuzz (extent/dentry invariant)

## 3. 4단계 — FUSE 통합 + integration test

### 3.1 FUSE 라이브러리 선택

후보:
- `github.com/hanwen/go-fuse/v2` (활발한 유지 보수, low-level)
- `bazil.org/fuse` (성숙, high-level이지만 유지보수 둔화)

권장: **hanwen/go-fuse v2**. low-level이라 우리 ops를 거의 1:1 매핑.

### 3.2 작업 항목

#### 4.A `cmd/mount` — FUSE 마운트 데몬

선행: 2단계 완료
산출물:
- CLI: `toyfs mount <image> <mountpoint> [-o options]`
- FUSE callback → `pkg/fs` ops 매핑
- caller uid/gid를 권한 체크에 전달 (09번 3.5)
- unmount 시 정상 절차 (10번 8절)

매핑 테이블:

| FUSE callback | toyfs ops |
|---|---|
| Lookup | fs.Lookup |
| Getattr | fs.Stat |
| Setattr | fs.Chmod / Chown / Truncate |
| Mknod / Create | fs.Create |
| Mkdir | fs.Mkdir |
| Unlink | fs.Unlink |
| Rmdir | fs.Rmdir |
| Symlink | fs.Symlink |
| Readlink | fs.Readlink |
| Link | fs.Link |
| Rename | fs.Rename |
| Open | fs.Open |
| Read | fs.Read |
| Write | fs.Write |
| Flush / Release | fs.Close (나누어 처리) |
| Fsync | journal.FlushAll + Sync |
| Readdir | fs.Readdir |
| Access | fs.Access (09번) |

#### 4.B `test/integration`

선행: 4.A
산출물:
- Go test가 임시 디렉토리에 mountpoint 만들기
- mount 데몬 백그라운드 실행
- POSIX 명령(`mkdir`, `cp`, `dd`, `cat`, `chmod`, `ln`, `ln -s`, `rm`, `mv`)을 `os/exec`로 실행
- unmount → 디스크 이미지 무결성 재검사

테스트 시나리오:
- 작은 파일 read/write
- 큰 파일 (extent split/promote 강제)
- 깊은 디렉토리 (재귀 mkdir/find)
- 하드링크 / 심볼릭링크 / dangling symlink / loop
- 권한 거부 케이스
- fsync 후 force kill → 재mount 시 데이터 살아남음
- 정상 unmount → 재mount 시 replay 없이 시작

### 3.3 4단계 완료 기준

- `mount.toyfs disk.img /mnt && cd /mnt && (POSIX 명령 모두 동작)`
- integration test 모든 시나리오 통과
- unmount → 다시 mount 정상

## 4. 5단계 — snapshot / recovery / crash test

### 4.1 작업 항목

#### 5.A `pkg/snapshot`

선행: 2~4단계
산출물:
- snapshot_meta 영역 관리 (08번 3절)
- snapshot create/delete/mount/rollback (08번 6~9절)
- refcount table 갱신 + SHARED flag
- ops 레이어와 통합 (write 시 CoW 트리거)

검증:
- 08번 §12 unit test 시나리오
- snapshot mount는 read-only enforcement

#### 5.B `cmd/toyfs-snap` — snapshot CLI

산출물:
- `toyfs-snap create <image>` → snapshot_id 반환
- `toyfs-snap list <image>` → 활성 snapshot 목록
- `toyfs-snap delete <image> <id>`
- `toyfs-snap mount <image> <id> <mountpoint>` (read-only)
- `toyfs-snap rollback <image> <id>` (open file 없을 때만)

#### 5.C crash 시뮬레이션 인프라

선행: 5.A
산출물:
- `BlockDevice`에 fault injection hook (10번 2.2)
- `FaultMode` enum + `SetFault` API
- crash 시뮬레이션 helper (10번 2)

#### 5.D `test/crash`

선행: 5.A, 5.C
산출물:
- 10번 §7의 시나리오 그룹 A~D 자동화
- 각 시나리오:
  1. ops 실행
  2. 임의 시점에 SetFault
  3. 프로세스 또는 데몬 강제 종료
  4. 재mount + replay
  5. 결과 invariant 검증

### 4.2 5단계 완료 기준

- snapshot 4개까지 create/delete 정상
- snapshot read와 활성 write 동시 진행 시 일관성 유지
- rollback 단순화 정책(3 제약) 안에서 정상 동작
- crash 시뮬레이션 그룹 A~D 모두 통과
- ordered 보장 검증 (메타가 가리키는 데이터는 valid)

## 5. 의존성 그래프 (요약)

```
1단계 docs
    │
    ▼
2.A block ─────┬─────┐
   │           │     │
   ▼           ▼     ▼
2.B sb     2.F journal (block + sb)
   │           │
   ▼           │
2.C inode      │
   │           │
   ▼           │
2.D extent     │
   │           │
   ▼           │
2.E dentry     │
   │           │
   └─────┬─────┘
         ▼
       2.G fs
         │
         ▼
       2.H mkfs
         │
         ▼
       3 testing + dump
         │
         ▼
       4 FUSE + integration
         │
         ▼
       5 snapshot + crash
```

## 6. 시간 계획 (학습용 추정)

학습용이라 정확 시간은 의미 작지만 *상대적 비중*:

| 단계 | 비중 |
|------|------|
| 2.A block | 작 |
| 2.B sb | 작 |
| 2.C inode | 중 |
| 2.D extent | **큼** (트리 알고리즘) |
| 2.E dentry | 중 |
| 2.F journal | **큼** (escape, ordered, replay) |
| 2.G fs | 중 (위 모듈 통합) |
| 2.H mkfs | 작 |
| 3 (test+dump) | 중 |
| 4 (FUSE) | 중 |
| 5 (snapshot+crash) | **큼** (CoW, rollback, crash sim) |

## 7. 1단계 작성 후 즉시 다시 봐야 할 것

2단계 시작 전 다음 항목 재검토 권장:

- **rollback의 transaction 한도 정책** (10번 §5.7): 단일 transaction 강제 vs 분할 + 진행 flag. 5.A 구현 시 결정
- **journal area 크기 충분성**: snapshot create의 메타데이터 변경량이 1 MiB journal로 충분한지. 부족하면 01번에서 journal 크기 키움
- **inode_table 사본 4 MiB의 영향**: 64 MiB 이미지에서 ~6.1% 메타 오버헤드. 학습용으로 OK이지만 측정 후 재검토 가능

## 8. 다음 프로젝트로 미루기

명시적으로 1단계 범위 밖 (다음 프로젝트):

- 동시성 (multi-thread, fine-grained locking)
- 데이터 저널링 (data=journal)
- xattr, ACL
- 큰 디렉토리 인덱싱 (htree/b-tree)
- 압축, 암호화, dedup
- quota, resize, defrag
- direct I/O 본격 구현
- lazy mtime 최적화
- Linux VFS / 커널 모듈
- multi-snapshot rollback
- read-write snapshot (subvolume/clone)
- 본격 fsck (자동 정정)
- ext4 기본의 hash directory 인덱싱

## 9. 마무리

1단계 설계 문서가 완료되면 이 roadmap을 따라 2단계로 진입.
설계가 맞물려 있어 *구현 도중* 일부 결정이 바뀔 수 있음 — 그 경우 해당 docs 파일을 *즉시 갱신*해서 코드와 문서가 분리되지 않도록 유지.
