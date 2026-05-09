# toyfs — Overview

## 1. 프로젝트 목표

`toyfs`는 파일시스템의 내부 동작을 깊이 이해하기 위한 학습용 프로토타입이다.
실서비스 품질이 아니라 **파일시스템을 구성하는 핵심 자료구조와 알고리즘을
직접 구현하면서 익히는 것**이 본질적 목적이다.

### 학습 대상
- on-disk 자료구조 설계 (superblock, inode, extent, dentry, journal)
- in-memory 자료구조 (block cache, open file table 등)와 on-disk의 관계
- 기본 파일/디렉토리 ops의 구현 메커니즘
- FUSE 인터페이스를 통한 사용자 공간 파일시스템 마운트
- CoW 기반 스냅샷과 메타데이터 저널링 기반 crash recovery

### 비목표 (Non-goals)
- 성능 최적화 (throughput / latency 튜닝)
- 동시성 제어 (single-threaded 가정, 큰 글로벌 락 하나로 충분)
- POSIX 완전 준수 (xattr, ACL, 큰 디렉토리 인덱싱 제외)
- 프로덕션 신뢰성 (장기간 가동, 디스크 깨짐 복구 등)
- Linux VFS / 커널 모듈 연동 (다음 프로젝트로 분리)

## 2. 언어 및 도구

- **언어**: Go
  - 학습 곁가지로 Go도 익히는 것이 부수적 목표
  - FUSE 바인딩이 성숙: `bazil.org/fuse` 또는 `hanwen/go-fuse` 중 4단계에서 결정
- **타깃 OS**: Linux/macOS (FUSE 가능한 환경)
- **개발 환경**: macOS (이 프로젝트의 호스트)

## 3. 스코프 — 무엇을 구현하는가

### 포함
- 단일 파일을 디스크 이미지로 사용하는 on-disk FS
- block 단위 I/O와 buffer cache 계층
- inode + extent tree 기반 파일 표현
- 디렉토리 = dentry 선형 리스트 (큰 디렉토리 인덱싱 제외)
- 기본 ops: create, open, read, write, close, unlink, mkdir, rmdir, readdir, lookup, rename, truncate, chmod, chown, stat
- 하드링크, 심볼릭링크
- 권한 체크 (mode bits + uid/gid)
- 메타데이터 저널링 + crash recovery
- CoW 기반 스냅샷 (생성/삭제/롤백)
- FUSE를 통한 마운트
- 디스크 이미지 덤프 도구 (`toyfs-dump`)

### 제외
- xattr, ACL
- 동시성 (multi-thread, fine-grained locking)
- 데이터 저널링 (메타데이터만 저널)
- 큰 디렉토리용 htree/b-tree 인덱싱
- 압축, 암호화, 중복제거
- quota, resize, defrag
- Linux VFS 연동 / 커널 모듈

## 4. 시스템 레이어

```
┌──────────────────────────────────────────────┐
│  사용자 공간 명령 (ls, cp, dd, cat, ...)      │
└──────────────────────────────────────────────┘
                    │
                    ▼ (system call)
┌──────────────────────────────────────────────┐
│  Linux/macOS VFS                              │
└──────────────────────────────────────────────┘
                    │
                    ▼ (FUSE protocol)
┌──────────────────────────────────────────────┐
│  toyfs FUSE adapter   ── 4단계에서 추가        │
└──────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────────────┐
│  toyfs core: ops layer                        │
│   create / read / write / lookup / ...        │
└──────────────────────────────────────────────┘
                    │
        ┌───────────┼────────────┐
        ▼           ▼            ▼
   ┌─────────┐ ┌─────────┐ ┌────────────┐
   │ inode   │ │ dentry  │ │ extent     │
   │ manager │ │ manager │ │ allocator  │
   └─────────┘ └─────────┘ └────────────┘
        │           │            │
        └───────────┼────────────┘
                    ▼
┌──────────────────────────────────────────────┐
│  Journal (metadata only)                      │
└──────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────────────┐
│  Block cache (in-memory, LRU, dirty/clean)    │
└──────────────────────────────────────────────┘
                    │
                    ▼
┌──────────────────────────────────────────────┐
│  Block device abstraction                     │
│  (단일 이미지 파일에 대한 ReadAt/WriteAt)     │
└──────────────────────────────────────────────┘
                    │
                    ▼
              [disk.img]  ← 호스트 FS의 평범한 파일
```

핵심 원칙:
- **모든 디스크 접근은 block cache를 거친다.** ops 레이어가 disk를 직접 보지 않음
- **모든 메타데이터 변경은 journal을 거친다.** 데이터 블록은 journal 우회 가능
- **모든 메타데이터 변경은 transaction 단위로 묶인다.** atomic 단위

## 5. 단계별 산출물

| 단계 | 작업 | 산출물 |
|------|------|--------|
| 1 | 구조 설계 | `docs/` 아래 설계 문서 (현재 단계) |
| 2 | 기본 ops 구현 | `pkg/` 아래 Go 패키지, mkfs CLI |
| 3 | unit test | 각 패키지의 `_test.go`, 테스트용 디버그 도구 |
| 4 | FUSE 통합 | mount 가능한 바이너리, integration test |
| 5 | 스냅샷/리커버리 | snapshot/recovery 모듈 + 테스트 |
| 6 | (생략) | — |

## 6. 디렉토리 구조 (예정)

```
toyfs/
├── docs/                  # 설계 문서 (1단계)
├── cmd/
│   ├── mkfs/              # 디스크 이미지 포맷팅
│   ├── toyfs-dump/        # 이미지 덤프 (디버깅용)
│   └── mount/             # FUSE 마운트 데몬 (4단계)
├── pkg/
│   ├── block/             # block device + cache
│   ├── superblock/
│   ├── inode/
│   ├── extent/
│   ├── dentry/
│   ├── journal/
│   ├── snapshot/
│   └── fs/                # ops 레이어 (각 모듈을 묶는다)
├── test/
│   ├── integration/       # FUSE 마운트 후 POSIX 명령 검증
│   └── crash/             # crash recovery 시나리오
└── go.mod
```

이 구조는 가이드일 뿐이며 2단계에서 구현하면서 조정될 수 있다.

## 7. 명명 규약

- 패키지/CLI: 모두 `toyfs` 또는 `toyfs-*` 접두
- 디스크 이미지: 기본 `disk.img`
- magic number: `TOYF` (0x54 0x4F 0x59 0x46) — superblock에서 사용
- 버전: `v0` 시작, on-disk 포맷 변경 시 증가

## 8. 다음 문서로의 안내

- `01-disk-layout.md` — 디스크 이미지 전체 레이아웃 (블록 0번부터 끝까지)
- `02-superblock.md` — superblock 필드와 의미
- `03-inode.md` — inode 구조
- `04-extent.md` — extent tree
- `05-dentry-directory.md` — 디렉토리 표현
- `06-block-cache.md` — buffer cache
- `07-journal.md` — 메타데이터 저널
- `08-snapshot.md` — CoW 스냅샷
- `09-links-permissions.md` — 링크와 권한
- `10-crash-recovery.md` — crash 시뮬레이션과 복구
- `11-roadmap.md` — 2~5단계 진행 계획
