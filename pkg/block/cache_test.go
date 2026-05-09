package block

import "testing"

// Skeleton tests derived from docs/06-block-cache.md §10.1.
// All tests start as t.Skip(...) so the suite stays green; flesh out as the
// implementation lands.

// ---- basic Get/Put / LRU ----

func TestGetCacheHit(t *testing.T)      { t.Skip("06 §10.1 — cache hit path") }
func TestGetCacheMiss(t *testing.T)     { t.Skip("06 §10.1 — cache miss loads from device") }
func TestPutLeakDetection(t *testing.T) { t.Skip("06 §10.1 — Get without Put keeps refcount > 0") }
func TestEvictOnFullCacheChoosesLRUTail(t *testing.T) {
	t.Skip("06 §10.1 — 256 slots full → tail evicted")
}
func TestEvictNoCandidateReturnsENOMEM(t *testing.T) {
	t.Skip("06 §10.1 — every slot pinned → ENOMEM")
}
func TestGetMovesSlotToLRUHead(t *testing.T) { t.Skip("06 §10.1 — recency update") }

// ---- dirty / flush ----

func TestMarkDirtyEvictionWritesBack(t *testing.T) {
	t.Skip("06 §10.1 — dirty evict writes home location")
}
func TestFlushAllClearsDirty(t *testing.T) {
	t.Skip("06 §10.1 — FlushAll → no dirty slots remain")
}
func TestSyncCallsDeviceFsync(t *testing.T) { t.Skip("06 §10.1 — Sync forwards to device") }

// ---- journal pinning ----

func TestJournalLockedSlotIsSkippedByEvict(t *testing.T) {
	t.Skip("06 §10.1 — pin keeps slot resident")
}
func TestAllSlotsJournalLockedReturnsENOMEM(t *testing.T) {
	t.Skip("06 §10.1 — every slot pinned by journal")
}
func TestJournalUnlockReenablesEviction(t *testing.T) {
	t.Skip("06 §10.1 — unlock makes slot evictable")
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
