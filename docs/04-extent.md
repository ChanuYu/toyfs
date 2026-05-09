# toyfs — Extent

이 문서는 `toyfs`의 **extent** on-disk 구조와 트리 형태를 정의한다.
extent는 "연속된 디스크 블록 범위"를 단일 레코드로 표현하는 자료구조로,
파일의 데이터 위치를 기록한다. 인접 문서:
- inode 안에서의 위치는 `03-inode.md` 6절 참고
- 큰 파일은 inode 인라인을 넘어 extent index 블록을 거치는 트리 형태

## 1. 기본 단위

| 항목 | 값 |
|------|----|
| extent record 크기 | 16 B 고정 |
| inode 인라인 슬롯 수 | 4 (= 64 B) |
| index block 크기 | 1 블록 (4 KiB) |
| index block 헤더 크기 | 16 B |
| index block 당 entry 수 | (4096 - 16) / 16 = **255** |
| 트리 최대 depth | 2 |

## 2. extent record 포맷 (leaf entry, 16 B)

leaf는 *실제 데이터 위치*를 가리키는 entry다.

```
struct toyfs_extent {
    u32  ee_block;        // 파일 안의 logical block 시작 번호
    u64  ee_physical;     // 디스크의 physical block 시작 번호
    u16  ee_len;           // 길이 (블록 단위, 1~65,535)
    u16  ee_flags;         // 비트 플래그 (아래 참고)
};
```

### 2.1 의미

- `ee_block`: 이 extent가 표현하는 파일 영역의 시작 logical block.
  실제 byte offset = `ee_block * block_size`. 이 extent는 logical block
  `[ee_block, ee_block + ee_len)` 구간을 커버
- `ee_physical`: 디스크상의 시작 블록 번호. 64-bit. backup superblock 등
  매우 큰 이미지에서도 안전
- `ee_len`: 블록 단위 길이. 16-bit이므로 **단일 extent 최대 길이 = 65,535 블록 = 256 MiB - 4 KiB**.
  64 MiB 이미지에서는 의미 없는 한계지만, 더 큰 이미지로 확장 시에도 충분
- `ee_flags`: 미래 확장용 비트. 1단계에서는 사용 안 하고 0으로 둠. 예약된 비트:

```
TOYFS_EXTENT_UNWRITTEN  = 1 << 0   // preallocated but not written (미사용)
TOYFS_EXTENT_SHARED     = 1 << 1   // 스냅샷과 공유 중 (CoW 직전)
TOYFS_EXTENT_INLINE_HEAD = 1 << 2  // (예약, 추후 사용)
```

`SHARED`는 *snapshot 공유 표시 캐시*. snapshot 생성 시 모든 extent에 set, 데이터 블록 CoW 후 새 extent의 SHARED는 0. 진실의 원천은 refcount table이며 SHARED는 빠른 판정 캐시. 자세한 건 `08-snapshot.md` 5.3절.

### 2.2 sparse 표현

sparse hole(데이터 없는 영역)은 **extent record를 두지 않는 것**으로 표현한다.
extent들 사이의 logical 갭이 곧 hole.

예: 파일이 logical block 0~9, 1000~1009을 가지고 그 사이는 hole이라면:
```
[ ee_block=0,    ee_physical=100, ee_len=10 ]
[ ee_block=1000, ee_physical=200, ee_len=10 ]
```

read 시 hole 영역은 0으로 채워 반환. write 시 새 extent 추가.

## 3. extent index entry 포맷 (16 B)

index는 *자식 노드 블록*을 가리키는 entry다. depth > 0인 노드의 entry는 모두 index.

```
struct toyfs_extent_idx {
    u32  ei_block;         // 자식 트리가 커버하는 logical block 시작
    u64  ei_child_block;   // 자식 index/leaf 블록의 physical block 번호
    u16  ei_child_count;   // 자식 노드 안의 사용 중 entry 수 (캐시)
    u16  ei_reserved;
};
```

### 3.1 의미

- `ei_block`: 이 자식 서브트리가 커버하는 logical block 범위의 *시작*. 끝은 다음 형제 entry의
  `ei_block` 직전까지 (마지막 형제이면 ∞)
- `ei_child_block`: 자식 노드를 담은 디스크 블록 번호
- `ei_child_count`: 자식 노드 헤더의 `count`를 캐싱. 트리 탐색 시 자식 블록을 안 읽고도
  binary search 범위를 알 수 있음. 실제 진실의 원천은 자식 노드 자체의 헤더이며,
  여기 캐시는 갱신 시 같이 맞춰야 함

### 3.2 leaf vs index 구분

같은 노드 안에 leaf entry와 index entry가 **섞이지 않는다**. 노드 헤더의 `depth`로 결정.
- `depth == 0`인 노드: 모든 entry는 leaf
- `depth > 0`인 노드: 모든 entry는 index

## 4. extent index block 포맷

index block은 1 블록(4 KiB) 고정. 헤더 + entry 배열.

```
struct toyfs_extent_block {
    // ---- header (16 B) ----
    u32  magic;           // "TXTB" = 0x42545854
    u16  depth;           // 0 = leaf node, 1 = depth-1 index node
                          // (트리 최대 depth 2이므로 0 또는 1만 가능)
    u16  count;           // 사용 중 entry 수
    u16  max;             // 최대 entry 수 (= 255 고정)
    u16  reserved;
    u32  checksum;        // 헤더(이 필드 제외) + entries 영역에 대한 CRC32

    // ---- entries (255 × 16 B = 4080 B) ----
    union {
        struct toyfs_extent      leaf[255];
        struct toyfs_extent_idx  idx[255];
    } entries;
};
```

총: 16 + 4080 = 4096 B 정확. 패딩 없음.

## 5. 트리 구조와 depth

```
inode.extent_depth == 0  (인라인 leaf)
  inode 안 inline_extents[64]에 leaf 0~4개
  (가장 간단한 경우)

inode.extent_depth == 1  (인라인이 index 1개를 가리킴)
  inode 안 inline_extents의 첫 슬롯 1개 = index entry
    → depth-0 index_block (leaf 들어있음, 최대 255개)

inode.extent_depth == 2  (트리 최대)
  inode 안 inline_extents의 첫 슬롯 1개 = index entry
    → depth-1 index_block (index 들어있음, 최대 255개)
        → depth-0 index_block (leaf 들어있음, 최대 255개) × 255
```

**용량 산정:**
- depth 0: 4 leaf
- depth 1: 255 leaf
- depth 2: 255 × 255 = 65,025 leaf

leaf 1개당 최대 65,535 블록(=256 MiB). 64 MiB 이미지의 데이터 블록 수가 약 15K개라
실제 leaf 수는 fragmentation 최악일 때도 ~15K 미만. **depth 2면 충분히 커버**.

> 주의: 위에서 inode의 `extent_depth`는 **트리 전체의 깊이**(0~2)이고,
> index block 헤더의 `depth`는 **그 노드 자신의 깊이**(0 또는 1)다.
> 트리가 depth 2라면 inode → depth-1 노드 → depth-0 노드 → 데이터 블록 순.

## 6. 트리 invariant

mount 시 / fsck 시 / 모든 ops 직후 다음이 성립해야 한다.

1. **타입 일관성**: 노드 헤더의 `depth == 0`이면 entry는 모두 leaf, `depth > 0`이면 모두 index
2. **logical 정렬**: 한 노드 안의 entry들은 `ee_block` (또는 `ei_block`) 오름차순
3. **logical 비중복**: 형제 entry들의 logical 범위가 겹치지 않음
4. **부모-자식 일치**: 부모 index entry의 `ei_block`은 자식 노드의 첫 entry `ee_block`/`ei_block`과 동일
5. **`ei_child_count` 캐시 정합**: 자식 노드 헤더의 `count`와 일치
6. **노드 비어있지 않음**: 사용 중 노드의 `count >= 1` (depth 0 leaf 노드도 동일).
   유일한 예외는 inode `extent_depth==0 && extent_count==0` (빈 파일)
7. **physical 범위 valid**: leaf의 `[ee_physical, ee_physical+ee_len)`이 superblock의
   `[data_block_start, data_block_start + data_block_count)` 안

## 7. 검색 알고리즘 (read 경로)

목표: 파일의 logical block L에 해당하는 physical block 찾기.

```
func find(inode, L) (physical, length, found bool):
    if inode.extent_depth == 0:
        scan inline_extents (최대 4개) — linear search
        if 어떤 leaf의 [ee_block, ee_block+ee_len) 안에 L 있음 → 반환
        else → hole (0으로 채움)

    else:
        node = read_block(inode.inline_extents[0].ei_child_block)
        loop:
            entries = node.entries
            // binary search: ei_block 오름차순이므로 lower_bound 가능
            i = largest i such that entries[i].ei_block <= L
            if i == -1 → hole
            if node.depth == 0:
                if L < entries[i].ee_block + entries[i].ee_len:
                    return (entries[i].ee_physical + (L - entries[i].ee_block),
                            entries[i].ee_len - (L - entries[i].ee_block),
                            true)
                else → hole
            else:
                node = read_block(entries[i].ei_child_block)
                continue
```

읽기 비용: depth 0 → inode만, depth 1 → +1 block I/O, depth 2 → +2 block I/O. 모두 block cache 적용.

## 8. 변경 알고리즘

### 8.1 append 정책 (merge)

새 write가 파일 끝에 연장하는 경우:
- 기존 마지막 leaf `{ee_block=B, ee_physical=P, ee_len=L}`
- 새 데이터의 logical 시작 = `B + L`
- 새 데이터의 physical 시작 = `P + L` (할당기가 그렇게 줬을 때)
- → 단순히 `ee_len`만 +k 증가. 새 extent 안 만듦

이 조건이 안 맞으면 새 leaf 1개 추가. 추가 후:
- 인라인이 4개 미만이면 인라인에 추가
- 인라인이 가득 찼으면 → 8.3절의 promote

### 8.2 split (중간 쓰기)

기존 leaf `{ee_block=B, ee_physical=P, ee_len=L}` 안에 떨어지는 새 write:
- write 영역: logical `[W, W+k)` 안에 `B <= W && W+k <= B+L` 조건
- physical 새 위치: `P'` (할당기가 정함)

split 결과:
```
[ B,   P,   W-B  ]    ← 앞쪽 (그대로)
[ W,   P',  k    ]    ← 새 데이터
[ W+k, P+(W+k-B), L-(W+k-B) ]  ← 뒤쪽 (그대로)
```

extent 1개 → 3개. 인라인 슬롯 부족하면 promote.

> 단순화: 기존 데이터 블록(앞/뒤 부분)은 *그 자리에 둔다*. 새 데이터만 새 블록에.
> CoW 모드(스냅샷이 잡고 있을 때)는 다름 → `08-snapshot.md`.

### 8.3 promote (depth 증가)

조건: leaf 추가가 필요한데 현재 노드가 가득 찬 경우.

**case A: inode 인라인 가득 (extent_depth==0, extent_count==4)**
1. 새 index 블록 한 개 할당 (`new_block`)
2. inode의 인라인 4개 leaf를 `new_block`의 entries[0..4]로 복사
3. 새 leaf를 entries[4]에 추가 → count=5
4. inode를 갱신: `extent_depth=1`, `extent_count=1`,
   `inline_extents[0] = {ei_block=0, ei_child_block=new_block, ei_child_count=5}`,
   나머지 슬롯 0
5. `new_block.depth=0`, `count=5`, checksum 갱신

**case B: depth 1 → depth 2 promote**

조건: inode `extent_depth==1`이고, 그 자식 depth-0 leaf 노드가 가득 참(count==255). 새 leaf 추가 필요.

1. 새 depth-0 leaf 노드 할당 (`new_leaf_block`). 새 leaf entry를 entries[0]로 기록 → count=1
2. 새 depth-1 index 노드 할당 (`new_idx_block`)
3. `new_idx_block.depth = 1`, entries:
   - entries[0] = `{ei_block=0, ei_child_block=<기존 가득 찬 depth-0 노드>, ei_child_count=255}`
   - entries[1] = `{ei_block=<새 leaf의 첫 ee_block>, ei_child_block=new_leaf_block, ei_child_count=1}`
   - count=2
4. inode 인라인 index entry가 `new_idx_block`을 가리키도록 갱신:
   - `inline_extents[0] = {ei_block=0, ei_child_block=new_idx_block, ei_child_count=2}`
5. inode `extent_depth=2` (extent_count=1 그대로)

> 주의: 기존 depth-0 leaf 노드는 *그 자리 그대로* 둠. 옮기지 않음. depth-1 노드만 새로 추가됨.

> **단순화**: 일단 promote된 트리는 *rebalance하지 않는다*. 한쪽 노드만 가득 차고 다른 노드는 거의 빈 상태가 되어도 그대로 둠. fsck나 별도 reorg 도구로 정리.
>
> 단, **truncate로 트리 전체가 빈 상태가 되면** depth/count 모두 0으로 복귀 (= "빈 파일" 상태). 자세한 건 9번 절. 이는 invariant 6 (count >= 1)을 어기지 않기 위한 처리.

### 8.4 rebalance — 안 함

학습용 단순화. 트리가 한쪽으로 치우쳐도 그대로 둔다.
- depth 1에서 새 leaf가 항상 마지막 노드에만 추가되는 경우 → 마지막 노드만 가득, 다른 노드는 거의 비어있음 가능
- 각 노드 invariant(`count >= 1`)만 유지
- 성능 영향은 받지만 정확성에는 무관

## 9. truncate

파일 크기를 N으로 줄이라는 요청:

1. logical block N 이후를 커버하는 leaf들을 찾아 처리
   - 완전히 N 이후에 있음 → leaf 자체를 제거, 데이터 블록 free
   - leaf의 일부만 N 이후 → leaf의 `ee_len`을 줄이고, 잘려나간 부분의 데이터 블록 free
2. 비어버린 leaf 노드(= count == 0)는 부모 index entry에서 제거 + 그 노드 블록 free (refcount 처리 후)
3. 빈 노드 제거가 부모에 전파되어 부모도 비면 동일하게 제거. 루트(=inode 인라인)까지 도달하면 inode `extent_depth=0, extent_count=0`으로 복귀
4. inode `size`, `blocks` 갱신
5. 모든 단계는 한 transaction으로 journal 기록

> **invariant 유지**: 모든 valid 노드는 `count >= 1` (6번 절). 빈 노드는 즉시 트리에서 제거되어 디스크에 남지 않음.
> **8.3 단순화와의 관계**: 8.3은 *promote 후 트리가 한쪽으로 치우쳐도 rebalance 안 함*을 의미. truncate로 빈 노드를 free하는 것과는 별개. depth는 트리가 *완전히 비어 빈 파일이 됐을 때만* 0으로 복귀.

> 데이터 블록 free는 refcount 1이면 bitmap clear, 그 이상이면 refcount만 감소
> (= 스냅샷이 잡고 있는 경우). 자세한 건 `08-snapshot.md`.

## 10. punch hole

`fallocate(FALLOC_FL_PUNCH_HOLE)` 시뮬레이션:
- truncate와 비슷하나 *중간*만 hole로 만듦
- 영향받는 leaf들에 대해:
  - 완전히 hole 영역 안 → 제거
  - hole이 leaf 앞쪽만 → `ee_block`/`ee_physical`을 앞으로 당기고 `ee_len` 감소
  - hole이 leaf 뒤쪽만 → `ee_len` 감소
  - hole이 leaf 중간 → split (8.2와 같은 메커니즘, 단 새 데이터 없이)
- inode `size`는 변경 없음, `blocks`만 감소

1단계에서 punch hole은 *선택적 구현*. 우선순위 낮음.

## 11. extent 할당기

새 extent를 위해 빈 데이터 블록을 찾는 정책. 1단계는 가장 단순한 **first-fit**:

1. 마지막 할당된 블록 위치를 superblock 외부에 in-memory로 캐시 (= `next_alloc_hint`)
2. 새 할당 요청이 오면 hint부터 시작해 block_bitmap을 스캔
3. 연속된 자유 블록 k개를 찾으면(요청한 길이) → 모두 set 후 그 시작 블록 반환
4. k개 연속을 못 찾으면 → 가능한 만큼만 반환 (= 작은 extent들로 split됨)
5. bitmap 끝까지 가도 없으면 처음부터 한 바퀴 더 → 그래도 없으면 ENOSPC

> 더 좋은 정책(buddy, group locality 등)은 학습 범위 밖. 다음 프로젝트.

## 12. in-memory 표현

```go
type Extent struct {
    LogicalBlock uint32
    PhysicalBlock uint64
    Length        uint16
    Flags         uint16
}

type ExtentIdx struct {
    LogicalBlock uint32
    ChildBlock   uint64
    ChildCount   uint16
    _            uint16
}

type ExtentNode struct {
    Magic, Depth, Count, Max uint16  // (Magic은 디스크에선 u32지만 in-memory는 사용 빈도 낮음)
    Checksum                 uint32
    // depth 0: Leaves 사용
    // depth >0: Indexes 사용
    Leaves   []Extent       // 길이 == Count
    Indexes  []ExtentIdx

    // ---- in-memory only ----
    DiskBlock uint64        // 이 노드가 속한 디스크 블록 번호
    Dirty     bool
}

type ExtentTree struct {
    Inode    *Inode          // 역참조
    RootIdx  *ExtentIdx      // depth>0일 때 inode의 inline 첫 슬롯 사본
    // 트리 전체를 in-memory로 들고 있지는 않음 (lazy load)
}
```

`ExtentTree`는 *경로 캐시* 정도만 들고 있고, 노드는 block cache를 통해 필요시 로드.

## 13. Testing

### 13.1 Unit test

레코드 직렬화:
- `Extent.Marshal/Unmarshal` round-trip (다양한 ee_block, ee_physical, ee_len, ee_flags 조합)
- `ExtentIdx` round-trip
- `ExtentNode` round-trip (depth 0, depth 1; count=0, count=1, count=255)
- checksum 검증 성공/실패

검색:
- 인라인 4개에 covering / non-covering / hole logical 입력 → 정확한 (physical, length) 또는 hole
- depth 1 트리에서 binary search 정확성 (255 leaf 채우고 모든 logical에 대해 검사)
- depth 2 트리에서 이중 traversal

append:
- 빈 inode에 logical 0부터 1개 블록씩 100번 append → 인라인 시작 → 5번째에서 promote → depth 1 → 트리 invariant 유지 검증
- merge 발생 케이스: 인접 physical/logical → length 증가만, 새 leaf 안 생김
- non-merge 케이스: 비인접 → 새 leaf 추가

split:
- 기존 leaf 중간에 write → 3개로 split, invariant 유지
- split 결과 인라인 4개 초과 → promote

truncate:
- 파일 끝 일부만 → leaf의 ee_len만 변경
- leaf 통째로 잘림 → leaf 제거, blocks 감소
- 빈 노드는 부모에서 제거됨 (9.2~9.3 절차). depth 자체는 단순화 정책상 줄어들지 않지만 *invariant 6 (count >= 1)은 유지* — 빈 노드는 즉시 free되어 트리에서 사라짐
- 모든 leaf 제거되어 트리 전체가 비면 → inode `extent_depth`/`extent_count` 모두 0 (빈 파일 상태로 복귀)

invariant fuzz:
- 임의의 write/truncate 시퀀스를 100~1000번 → 매 단계마다 7번 절 invariant 검증
- `go test -fuzz`로 split/merge 가장자리 케이스 발굴

### 13.2 Integration test (4단계 이후)
- `dd if=/dev/zero of=/mnt/foo bs=4K count=N` 다양한 N (1, 4, 16, 256, 1024) → 디스크 이미지 덤프해서 extent 수가 합리적인지
- `truncate -s` 다양한 크기로 줄이고 키우기 → stat으로 size/blocks 확인
- 큰 파일을 *순차로* 작성한 경우 vs *조각조각 다른 위치에* 작성한 경우 → 후자에서 extent 수가 더 많음을 확인

### 13.3 Crash recovery (5단계 이후)
- promote 도중 crash (새 index 블록 할당 후 inode 갱신 전) → journal replay로 복구되는지
- split 도중 crash (새 데이터 블록 write 후 leaf 갱신 전) → 일관 상태 복구

### 13.4 Fragmentation 측정 (6단계나 후속 프로젝트)
- 큰 이미지(256 MiB+)에서 의도적으로 디스크를 흩어 쓰고 → extent 수 / 파일 블록 수 비율로 fragmentation 정량화
- 학습용. 1단계 필수 아님

## 14. 다음 문서로의 안내

- `05-dentry-directory.md` — 디렉토리도 결국 *데이터 영역*에 dentry를 저장하는 파일이므로 extent를 통해 표현됨
- `06-block-cache.md` — extent index 블록 read/write가 block cache를 거치는 경로
- `07-journal.md` — promote/split/truncate가 한 transaction으로 묶이는 방식
- `08-snapshot.md` — `TOYFS_EXTENT_SHARED` 플래그와 CoW 시 extent 분기
