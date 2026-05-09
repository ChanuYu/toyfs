# toyfs — Block Cache

이 문서는 `toyfs`의 **block cache** (= buffer cache) 자료구조와 동작을 정의한다.
모든 디스크 read/write는 이 캐시를 거친다(직접 I/O 우회는 1단계 범위 밖).

핵심 원칙:
- **캐시는 single source of truth**: 같은 블록의 모든 view는 캐시 슬롯 한 곳을 통과
- **write-back**: write는 즉시 디스크에 가지 않고 dirty 마킹만. 본위치 flush는 별도 시점
- **ordered journaling 친화**: journal 진행 중인 메타데이터 블록은 본위치에 못 가도록 pin

## 1. 기본 단위

| 항목 | 값 |
|------|----|
| 캐시 슬롯 수 | 256 (고정) |
| 슬롯 1개 크기 | 4 KiB (= 1 블록) |
| 캐시 총 크기 | 1 MiB |
| eviction 정책 | LRU |
| flush 정책 | write-back, dirty expire 5 sec |
| direct I/O | 1단계 미구현 (인터페이스 자리만 둠) |

## 2. 자료구조

```go
type CachedBlock struct {
    BlockNum     uint64        // 디스크 블록 번호 (key)
    Data         [4096]byte    // 블록 내용

    // ---- state ----
    Dirty        bool          // 본위치 flush가 아직 안 됨
    RefCount     int           // Get/Put 카운트. 0보다 크면 evict 불가
    JournalLocked bool         // journal commit이 진행 중. 본위치 flush 금지
    DirtyAt      time.Time     // dirty 마킹된 시각 (expire 판정용)

    // ---- frozen copy (07번 journal 문서 참고) ----
    FrozenData   *[4096]byte   // committing 중에 추가 수정이 오면 이쪽에 t_commit 시점 사본 보관

    // ---- LRU bookkeeping ----
    prev, next   *CachedBlock  // doubly linked list 노드
}

type BlockCache struct {
    Slots       map[uint64]*CachedBlock   // BlockNum → slot lookup
    LRUHead     *CachedBlock              // most recently used
    LRUTail     *CachedBlock              // least recently used (eviction 후보)
    Used        int                       // 현재 사용 중 슬롯 수 (≤ 256)
    Capacity    int                       // 256 고정

    Device      *BlockDevice              // 아래 5절
    // 1단계는 single-thread이므로 mutex 없음
}
```

`Slots`는 hashmap으로 O(1) lookup, `LRUHead/Tail`은 evict 후보를 O(1)에 찾기 위함.

## 3. 핵심 API

```go
// Get은 slot의 RefCount를 증가시키고 반환한다. 캐시에 없으면 디스크에서 로드.
// 호출자는 사용 후 반드시 Put을 호출해야 한다.
func (c *BlockCache) Get(blockNum uint64) (*CachedBlock, error)

// Put은 RefCount를 감소시킨다. 0이 되면 eviction 후보로 들어갈 수 있다.
func (c *BlockCache) Put(b *CachedBlock)

// MarkDirty는 슬롯을 dirty로 마킹하고 DirtyAt을 갱신한다.
// caller는 이미 Get을 호출한 슬롯에 대해 호출.
func (c *BlockCache) MarkDirty(b *CachedBlock)

// Flush는 특정 슬롯을 본위치에 즉시 write한다. journal에 묶여 있으면 에러.
func (c *BlockCache) Flush(b *CachedBlock) error

// FlushAll은 dirty이고 evict 가능한 모든 슬롯을 본위치에 write한다.
func (c *BlockCache) FlushAll() error

// Sync는 디바이스 fsync 호출.
func (c *BlockCache) Sync() error

// JournalLock/Unlock은 journal subsystem이 호출. commit 진행 중인 블록을 본위치 flush에서 제외한다.
func (c *BlockCache) JournalLock(b *CachedBlock)
func (c *BlockCache) JournalUnlock(b *CachedBlock)
```

### 3.1 Get/Put 패턴

caller는 다음 패턴을 지킨다:

```go
b, err := cache.Get(blockNum)
if err != nil { return err }
defer cache.Put(b)

// b.Data 읽기/쓰기
if writing { cache.MarkDirty(b) }
```

`Put` 안 하면 **leak** — 그 슬롯은 RefCount > 0으로 남아 영영 evict 안 됨. testing에서 leak 검출 필수.

### 3.2 Get 동작 상세

```
func Get(blockNum):
    if blockNum in Slots:
        b = Slots[blockNum]
        b.RefCount++
        LRU.move_to_head(b)
        return b

    # cache miss
    if Used < Capacity:
        b = new CachedBlock
    else:
        b = evict_one()
        if b == nil: return ENOMEM   # evict 가능한 슬롯이 하나도 없음
        delete Slots[b.BlockNum]

    b.BlockNum = blockNum
    b.Data = Device.read(blockNum)
    b.RefCount = 1
    b.Dirty = false
    b.JournalLocked = false
    Slots[blockNum] = b
    LRU.push_head(b)
    return b
```

### 3.3 evict_one 동작

```
func evict_one():
    # LRU tail에서 시작해 head 방향으로 evict 가능한 첫 슬롯 찾기
    b = LRUTail
    while b != nil:
        if b.RefCount == 0 && !b.JournalLocked:
            if b.Dirty:
                Device.write(b.BlockNum, b.Data)   # 본위치 flush
                b.Dirty = false
            LRU.remove(b)
            return b
        b = b.prev
    return nil   # 캐시 전체가 pin됨
```

evict 가능 조건: `RefCount == 0 && !JournalLocked`. dirty여도 journal에 안 묶여있으면 flush 후 evict 가능.

> **주의**: dirty인데 evict될 때는 *반드시* 본위치 write 후 evict. 그냥 버리면 데이터 손실.

## 4. ordered journaling 통합

ordered 모드의 핵심 ordering:

1. **데이터 블록 → 본위치 write 완료**
2. **메타데이터 → journal 영역 write + commit record 완료**
3. **메타데이터 → 본위치 write** (checkpoint)

### 4.1 journal subsystem이 cache를 쓰는 방식

journal subsystem (07번 문서)이 transaction을 commit할 때:

1. transaction에 묶인 모든 *데이터 블록*에 대해 `cache.Flush(dataBlock)` 호출 → 데이터가 본위치에 도달
2. transaction에 묶인 모든 *메타데이터 블록*에 대해 `cache.JournalLock(metaBlock)` 호출 → 본위치 flush 금지
3. journal 영역에 메타데이터 사본을 write (Frozen 사본 메커니즘은 4.2)
4. commit record write + sync
5. transaction이 *checkpoint될 때* 메타데이터 블록에 대해 `cache.JournalUnlock(metaBlock)` 호출 + 본위치 flush

cache는 (1)~(4) 동안 메타데이터 슬롯을 본위치에 보내지 않는다. (5) 이후에는 자유.

### 4.2 frozen 사본 (committing 중 추가 수정 처리)

상황: transaction T1이 commit 중(journal 영역에 쓰고 있음)인데, 같은 메타데이터 블록 B에 새 수정이 들어옴.

처리:
1. T1 commit이 시작될 때 B의 `JournalLocked = true`로 마킹
2. journal subsystem이 B의 현재 `Data` 사본을 `FrozenData`로 떠둠 (= t_commit 시점 사본)
3. 이후 들어오는 수정은 `Data`를 갱신 (cache의 최신 내용은 새 transaction T2에 묶임)
4. journal write는 `FrozenData`를 씀 (= T1 시점 내용)
5. T1 commit 완료 후 `FrozenData` 해제

```go
func (c *BlockCache) FreezeForCommit(b *CachedBlock):
    if b.FrozenData != nil: panic("double freeze")
    snapshot := b.Data   // value copy (4096 byte)
    b.FrozenData = &snapshot

func (c *BlockCache) ReleaseFrozen(b *CachedBlock):
    b.FrozenData = nil
```

> **단순화**: 본위치 flush는 "잡힌 모든 transaction이 commit 완료된 후"에만 일어나므로,
> flush 시점에는 항상 cache의 `Data`(= 최신 commit 반영본)를 그대로 쓰면 된다.
> Frozen은 *journal 영역에 쓰는 동안만* 의미를 가지는 임시 사본.

### 4.3 ordering 검증

cache가 ordered 모드를 어기지 않는지 다음 invariant로 검증:

- 어떤 메타데이터 블록 B에 대해, B의 `JournalLocked == true`인 동안 본위치에 write되지 않는다 (Flush 호출이 에러 반환)
- 데이터 블록은 `JournalLocked`가 항상 false (데이터는 journal에 안 들어감)
- transaction commit 순서: 데이터 본위치 flush → journal 영역 write → journal commit record → (나중에) 메타데이터 본위치 flush

## 5. block device 추상화

cache의 아래 계층:

```go
type BlockDevice struct {
    File       *os.File   // 디스크 이미지 파일 핸들
    BlockSize  int        // 4096 고정
    NumBlocks  uint64     // 이미지 전체 블록 수
}

func (d *BlockDevice) Read(blockNum uint64) ([4096]byte, error)
func (d *BlockDevice) Write(blockNum uint64, data [4096]byte) error
func (d *BlockDevice) Sync() error    // os.File.Sync()
```

`Read`/`Write`는 `pread`/`pwrite` (Go의 `os.File.ReadAt`/`WriteAt`) 사용 — file offset state 의존 없음.

### 5.1 direct I/O 자리

미래 확장용 인터페이스:

```go
type IOMode int
const (
    IOCached IOMode = iota   // 1단계 기본
    IODirect                 // 미구현
)

func (d *BlockDevice) ReadMode(blockNum uint64, mode IOMode) ([4096]byte, error)
```

1단계는 항상 `IOCached`. `IODirect`는 4단계 이후 FUSE의 `direct_io` 옵션과 연동할 때 구현.

## 6. flush 정책

### 6.1 명시 flush

- `cache.FlushAll()` — 가능한 모든 dirty 슬롯
- `cache.Sync()` — fsync
- `unmount` 시점: `FlushAll() + Sync()`

### 6.2 자동 flush (timer 기반)

학습용 단순화:
- 백그라운드 goroutine이 5초마다 깨어남 (= ext4의 `dirty_writeback_centisecs` 기본 5초 환산)
- dirty이고 `DirtyAt`이 5초 이상 지난 슬롯들에 대해 evict 가능 조건이면 본위치 flush
- `JournalLocked` 슬롯은 skip

> 1단계 구현 우선순위는 명시 flush. 자동 flush는 timer 1개로 단순하게 구현하되, 학습 가치 측면에서 dirty page writeback 메커니즘을 *직접 구현해보는 것*이 의미 있음.

## 7. invariant

mount 중 항상 성립:

1. `Slots`의 모든 entry는 LRU list에도 있음
2. LRU list 노드 수 == `Used`
3. `Used <= Capacity`
4. 모든 슬롯의 `RefCount >= 0`
5. `JournalLocked == true`인 슬롯은 evict되지 않는다 (LRU tail까지 가도 skip)
6. `Dirty == true`인 슬롯이 evict되면 그 직전에 본위치 write 완료
7. `FrozenData != nil`인 슬롯은 `JournalLocked == true`

unmount 직후:
8. 모든 슬롯이 `Dirty == false`
9. 모든 슬롯이 `JournalLocked == false`

## 8. caller가 지켜야 할 규약

ops 레이어, journal subsystem, ext/dentry 모듈 등 모든 caller:

- `Get` 호출했으면 반드시 `Put` (defer 권장)
- 디스크 write가 필요하면 `MarkDirty` 호출 (`Data`만 바꾸고 끝나면 안 됨)
- **메타데이터 블록**: 반드시 journal subsystem을 거쳐야 함. 직접 `Flush` 금지.
  본위치 flush는 *transaction이 checkpoint된 후*에만 (= `JournalLocked == false`인 시점)
- **데이터 블록**: ops 레이어가 직접 `Flush` 호출 가능. ordered mode의 (1)단계
  "데이터 본위치 write 완료"가 *메타데이터 transaction commit 시작 전*에 끝나야 함

## 9. in-memory only 필드 정리

`CachedBlock`의 모든 필드는 in-memory only. 디스크에는 `Data`만 본위치에 write됨.
unmount 시 cache 자체는 *해제됨*. 다음 mount는 빈 cache로 시작.

## 10. Testing

### 10.1 Unit test

기본 동작:
- `Get` cache hit/miss 정확성
- `Get` 후 `Put` 안 하면 evict 안 됨 (leak detection)
- 256 슬롯 모두 사용 후 새 `Get` → LRU tail evict
- evict 후보 없으면 (모두 RefCount > 0) ENOMEM
- LRU 순서: `Get`이 슬롯을 head로 이동

dirty/flush:
- `MarkDirty` 후 evict 시 본위치 write 검증 (`BlockDevice` mock으로 write 호출 확인)
- `FlushAll` 후 모든 슬롯 `Dirty == false`
- `Sync` 호출이 디바이스의 fsync로 전달

journal pinning:
- `JournalLock`된 슬롯은 evict 후보에서 skip
- 모든 슬롯이 JournalLocked인 극단 케이스 → ENOMEM
- `JournalUnlock` 후 다시 evict 가능

frozen 사본:
- `FreezeForCommit` 후 `Data`를 수정해도 `FrozenData`는 t_commit 시점 그대로
- `ReleaseFrozen` 후 `FrozenData == nil`

ordering invariant:
- `JournalLocked == true`인 슬롯에 대해 `Flush` 호출 → 에러 반환

### 10.2 Integration test (4단계 이후)
- FUSE 마운트 후 대량 write → unmount → 다시 mount → 데이터 일치
- write 후 fsync 호출 → 디스크 이미지에 즉시 반영 확인 (cache 우회 검증용)

### 10.3 Crash recovery (5단계 이후)
- write 도중 process kill (cache flush 전) → 다시 mount 시 journal replay로 복구
- ordered 모드 보장 검증: crash 후 마운트했을 때 메타데이터가 데이터를 가리키고 있다면 그 데이터는 *반드시* 디스크에 정상 존재

### 10.4 Stress / leak
- 임의의 Get/Put/MarkDirty 시퀀스 100K번 → 모든 슬롯 RefCount == 0으로 끝나는지
- 의도적으로 Put을 빠뜨린 시나리오에서 leak detection 발동 (검증용 helper 필요)

## 11. 다음 문서로의 안내

- `07-journal.md` — transaction 라이프사이클, frozen 사본 사용, ordered ordering 강제
- `08-snapshot.md` — 스냅샷 시점에 cache의 dirty가 어떻게 처리되는지
- `10-crash-recovery.md` — crash 시 cache 상태가 디스크에 어디까지 반영됐는지
