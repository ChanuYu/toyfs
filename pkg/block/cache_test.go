package block

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Skeleton tests derived from docs/06-block-cache.md §10.1.
// All tests start as t.Skip(...) so the suite stays green; flesh out as the
// implementation lands.

// ---- test helpers ----

// newTestCache builds an empty BlockCache backed by an empty disk image of
// numBlocks * 4 KiB sitting in a t.TempDir(). The image and any open file
// handle are cleaned up automatically when the test ends.
func newTestCache(t *testing.T, capacity, numBlocks int) *BlockCache {
	t.Helper()

	path := filepath.Join(t.TempDir(), "disk.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create disk image: %v", err)
	}
	t.Cleanup(func() { f.Close() })

	// Pre-size the file so ReadAt on any valid block returns zeros instead of
	// a short read.
	if err := f.Truncate(int64(numBlocks) * 4096); err != nil {
		t.Fatalf("truncate disk image: %v", err)
	}

	return &BlockCache{
		Slots:    make(map[uint64]*CachedBlock),
		Capacity: capacity,
		Device: &BlockDevice{
			File:      f,
			BlockSize: 4096,
			NumBlocks: uint64(numBlocks),
		},
	}
}

// ---- basic Get/Put / LRU ----

func TestGetCacheHit(t *testing.T) {
	c := newTestCache(t, 256, 16)
	// First Get: cache miss → slot is loaded from the device. RefCount == 1.
	fmt.Printf("First Access\n")
	first, err := c.Get(42)
	if err != nil {
		t.Fatalf("first Get(42) returned error: %v", err)
	}
	if first == nil {
		t.Fatal("first Get(42) returned a nil slot")
	}
	if first.BlockNum != 42 {
		t.Errorf("first.BlockNum = %d, want 42", first.BlockNum)
	}
	if first.RefCount != 1 {
		t.Errorf("after first Get: RefCount = %d, want 1", first.RefCount)
	}

	// Second Get on the same block: cache hit. We must get the *exact same*
	// slot pointer back (no realloc) and RefCount must bump to 2.
	fmt.Printf("Second Access\n")
	second, err := c.Get(42)
	if err != nil {
		t.Fatalf("second Get(42) returned error: %v", err)
	}
	if second != first {
		t.Errorf("second Get(42) returned a different slot pointer (got %p, want %p)", second, first)
	}
	if second.RefCount != 2 {
		t.Errorf("after second Get: RefCount = %d, want 2", second.RefCount)
	}

	// The cache must still track exactly one slot — the hit must not have
	// allocated anything new.
	if got := len(c.Slots); got != 1 {
		t.Errorf("len(c.Slots) = %d, want 1", got)
	}
}

func TestGetCacheMiss(t *testing.T) {
	c := newTestCache(t, 256, 16)

	blk, err := c.Get(55)
	if err != nil {
		t.Fatalf("Get failed to allocate new CachedBlock\n")
	}
	if blk.BlockNum != 55 {
		t.Fatalf("CachedBlock has inappropriate block num %d\n", blk.BlockNum)
	}
	if blk.RefCount != 1 {
		t.Errorf("Reference count should be 1\n")
	}

	if got := len(c.Slots); got != 1 {
		t.Errorf("len(c.Slots) = %d, want 1\n", got)
	}
}
func TestPutLeakDetection(t *testing.T) {
	c := newTestCache(t, 256, 16)
	blk, _ := c.Get(30)
	if blk.RefCount != 1 {
		t.Errorf("Refcount should be 1")
	}
	blk.Put()
	if blk.RefCount > 0 {
		t.Errorf("Refcount should be 0 after Put()")
	}
}
func TestEvictOnFullCacheChoosesLRUTail(t *testing.T) {
	c := newTestCache(t, 256, 256)
	for blk := 1; blk <= 256; blk++ {
		cachedBlk, _ := c.Get(uint64(blk))
		cachedBlk.Put() // make reference count of the cached block 0
	}
	if got := len(c.Slots); got != 256 {
		t.Fatalf("Cache size should be 256")
	}
	tail := c.LRUTail
	if tail.BlockNum != 1 {
		t.Errorf("Block number of tail should be %d, not %d", 1, tail.BlockNum)
	}
	fmt.Printf("Test: Get triggers eviction\n")
	c.Get(1000) // tail should be evicted
	if got := len(c.Slots); got != 256 {
		t.Fatalf("Cache size should be kept after eviction")
	}
	newTail := c.LRUTail
	if tail == newTail {
		t.Fatalf("Eviction does not happened")
	}
	if newTail.BlockNum != 2 {
		t.Errorf("newTail should be %d, not %d", 2, newTail.BlockNum)
	}

}
func TestEvictNoCandidateReturnsENOMEM(t *testing.T) {
	c := newTestCache(t, 256, 256)
	for blk := 1; blk <= 256; blk++ {
		c.Get(uint64(blk))
		// skip Put() to remain all the cached blocks not evictable
	}

	if got := len(c.Slots); got != 256 {
		t.Fatalf("Cache size should be 256")
	}

	_, err := c.Get(uint64(1000))
	if err != ENOMEM {
		t.Errorf("Get must return ENOMEM")
	}

	if c.LRUHead.BlockNum == 1000 {
		t.Fatalf("The last block should not be allocated to the cache")
	}
}
func TestGetMovesSlotToLRUHead(t *testing.T) {
	c := newTestCache(t, 256, 256)
	testcase := []struct {
		name  string
		input uint64
	}{
		{"init", 10},
		{"init", 20},
		{"init", 30},
		{"init", 40},
		{"init", 50},
		{"reaccess", 30},
		{"reaccess", 10},
	}
	for _, tc := range testcase {
		t.Run(tc.name, func(t *testing.T) {
			blk, _ := c.Get(tc.input)
			if blk != c.LRUHead || blk.BlockNum != tc.input {
				t.Errorf("%s: last accessed block %d should be located on LRUHead", tc.name, tc.input)
			}
		})
	}
}

// ---- dirty / flush ----

func TestMarkDirtyEvictionWritesBack(t *testing.T) {
	c := newTestCache(t, 128, 256)
	var str = "Newly Written Data"
	for i := 1; i <= 128; i++ {
		blk, _ := c.Get(uint64(i))
		if i == 1 {
			// Write New Data
			var data [4096]byte
			copy(data[:], str)
			blk.WriteData(&data)
		}
		blk.Put()
	}

	// trigger last data eviction
	c.Get(200)

	readData, err := c.Device.Read(1)
	if err != nil {
		t.Errorf("Device.Read Failed: %s\n", err)
	}
	if string((*readData)[:len(str)]) != str {
		t.Errorf("Incorrect Data: *readData - %q, org - %s\n", *readData, str)
	}
}
func TestFlushAllClearsDirty(t *testing.T) {
	// make dirty data set
	dirtySet := make([]int, 10)
	for i := range 10 {
		dirtySet[i] = rand.Intn(128) + 1
	}
	str := "Dirty Data"
	data := [4096]byte{}
	copy(data[:], str)

	// Make CachedBlock dirty if blockNumber is in the dirtyset
	c := newTestCache(t, 128, 256)
	for i := 1; i <= 128; i++ {
		blk, _ := c.Get(uint64(i))
		if slices.Contains(dirtySet, i) {
			blk.WriteData(&data)
		}
		blk.Put()
	}

	// Flush all the dirty cache
	c.FlushAll()

	// Check if there's dirty data exists
	for i := 1; i <= 128; i++ {
		blk, _ := c.Get(uint64(i))
		if blk.Dirty {
			t.Errorf("Block %d still marked dirty\n", blk.BlockNum)
		}
	}

	// Check whether all the data written to disk is correct
	for _, i := range dirtySet {
		blk, _ := c.Get(uint64(i))
		if string((*blk.ReadData())[:len(str)]) != str {
			t.Errorf("%d Block Data: %q\n", i, *blk.ReadData())
		}
	}
}
func TestSyncCallsDeviceFsync(t *testing.T) {
	// make dirty data set
	dirtySet := make([]int, 10)
	for i := range 10 {
		dirtySet[i] = rand.Intn(128) + 1
	}
	str := "Dirty Data"
	data := [4096]byte{}
	copy(data[:], str)

	// Make CachedBlock dirty if blockNumber is in the dirtyset
	c := newTestCache(t, 128, 256)
	for i := 1; i <= 128; i++ {
		blk, _ := c.Get(uint64(i))
		if slices.Contains(dirtySet, i) {
			blk.WriteData(&data)
		}
		blk.Put()
	}

	// Flush all the dirty cache
	c.Sync()

	// Check if there's dirty data exists
	for i := 1; i <= 128; i++ {
		blk, _ := c.Get(uint64(i))
		if blk.Dirty {
			t.Errorf("Block %d still marked dirty\n", blk.BlockNum)
		}
	}

	// Check whether all the data written to disk is correct
	for _, i := range dirtySet {
		blk, _ := c.Get(uint64(i))
		if string((*blk.ReadData())[:len(str)]) != str {
			t.Errorf("%d Block Data: %q\n", i, *blk.ReadData())
		}
	}
}

// ---- journal pinning ----

func TestJournalLockedSlotIsSkippedByEvict(t *testing.T) {
	//t.Skip("06 §10.1 — pin keeps slot resident")
	pinnedSet := make([]int, 10)
	for i := range pinnedSet {
		pinnedSet[i] = rand.Intn(128)
	}

	c := newTestCache(t, 128, 256)
	for i := range 128 {
		blk, _ := c.Get(uint64(i))
		if slices.Contains(pinnedSet, i) {
			blk.JournalLocked = true
		}
		blk.Put()
	}
}
func TestAllSlotsJournalLockedReturnsENOMEM(t *testing.T) {
	c := newTestCache(t, 128, 256)
	for i := 0; i < 128; i++ {
		blk, _ := c.Get(uint64(i))
		blk.JournalLocked = true
		blk.Put()
	}

	if got := len(c.Slots); got != 128 {
		t.Fatalf("Cache size should be 128")
	}

	_, err := c.Get(uint64(255))
	if err != ENOMEM {
		t.Errorf("Get must return ENOMEM")
	}

	if c.LRUHead.BlockNum == 255 {
		t.Fatalf("The last block should not be allocated to the cache")
	}
}
func TestJournalUnlockReenablesEviction(t *testing.T) {
	journalSet := make([]int, 10)
	for i := range 10 {
		journalSet[i] = rand.Intn(128)
	}

	// make journalSet slice have unique elements
	journalSet = func(nums []int) []int {
		set := make(map[int]struct{})
		res := make([]int, 0, len(nums))

		for _, i := range nums {
			if _, ok := set[i]; ok {
				continue
			}
			set[i] = struct{}{}
			res = append(res, i)
		}
		return res
	}(journalSet)

	c := newTestCache(t, 128, 256)
	for i := 0; i < 128; i++ {
		blk, _ := c.Get(uint64(i))
		// make the element of journal set JournalLocked, and the others keep referenced, so that no one in the cached evictable
		if slices.Contains(journalSet, i) {
			blk.JournalLocked = true
			blk.Put()
		}
	}

	if got := len(c.Slots); got != 128 {
		t.Fatalf("Cache size should be 128")
	}

	_, err := c.Get(uint64(255))
	if err != ENOMEM {
		t.Errorf("Get must return ENOMEM")
	}

	for _, i := range journalSet {
		blk, _ := c.Get(uint64(i))
		blk.JournalLocked = false
		blk.Put()
	}

	// new accesses for the size of journalSet
	for i := range len(journalSet) {
		_, err := c.Get(uint64(i + 128))
		if err == ENOMEM {
			t.Errorf("%d block failed to get cache entry\n", i+128)
		}
	}

	// check if there's any journalSet element
	cur := c.LRUHead
	for cur != nil {
		if slices.Contains(journalSet, int(cur.BlockNum)) {
			t.Errorf("journalSet element %d exist on cache\n", cur.BlockNum)
		}
		cur = cur.next
	}
}

// ---- frozen copy ----

func TestFreezeForCommitSnapshotsData(t *testing.T) {
	t.Skip("06 §10.1 — frozen retains pre-commit bytes")
}
func TestReleaseFrozenClearsCopy(t *testing.T) {
	t.Skip("06 §10.1 — FrozenData == nil after release")
}

// ---- ordering invariant ----

func TestFlushOnJournalLockedReturnsError(t *testing.T) {
	t.Skip("06 §10.1 — explicit Flush on pinned slot fails")
}

// ---- stress / leak ----

func TestStressGetPutMarkDirtyNoLeak(t *testing.T) {
	t.Skip("06 §10.4 — 100k random ops, all RefCount==0 at end")
}
