package extent

import "testing"

// Skeleton tests derived from docs/04-extent.md §13.1.

// ---- record serialization ----

func TestExtentRoundTrip(t *testing.T) {
	t.Skip("04 §13.1 — leaf 16 B round-trip across ee_block/ee_physical/ee_len/ee_flags")
}
func TestExtentIdxRoundTrip(t *testing.T)        { t.Skip("04 §13.1 — index 16 B round-trip") }
func TestExtentNodeRoundTripDepth0(t *testing.T) { t.Skip("04 §13.1 — leaf node, count={0,1,255}") }
func TestExtentNodeRoundTripDepth1(t *testing.T) { t.Skip("04 §13.1 — index node, count={1,255}") }
func TestExtentNodeChecksum(t *testing.T)        { t.Skip("04 §13.1 — checksum verify ok / corrupted") }

// ---- search ----

func TestFindInline_HitMissHole(t *testing.T) {
	t.Skip("04 §13.1 — inline 4 leaves, covering/non-covering/hole")
}
func TestFindDepth1BinarySearch(t *testing.T) {
	t.Skip("04 §13.1 — fill 255 leaves, every logical block resolves")
}
func TestFindDepth2(t *testing.T) { t.Skip("04 §13.1 — two-level traversal") }

// ---- append / merge ----

func TestAppendInlineUpToFour(t *testing.T) { t.Skip("04 §13.1 — 4 disjoint appends stay inline") }
func TestAppendMergesContiguousLeaf(t *testing.T) {
	t.Skip("04 §13.1 — adjacent logical+physical → ee_len grows, no new leaf")
}
func TestAppendNonContiguousAddsLeaf(t *testing.T) { t.Skip("04 §13.1 — non-adjacent → new leaf") }
func TestAppendFifthExtentTriggersPromote(t *testing.T) {
	t.Skip("04 §13.1 — 5th leaf → depth 0→1")
}

// ---- split ----

func TestSplitMidLeafYieldsThree(t *testing.T) {
	t.Skip("04 §13.1 — write into the middle of a leaf")
}
func TestSplitOverflowsInlineTriggersPromote(t *testing.T) {
	t.Skip("04 §13.1 — split that would push >4 inline → promote")
}

// ---- truncate ----

func TestTruncateShortensLeaf(t *testing.T) {
	t.Skip("04 §13.1 — partial leaf truncation only adjusts ee_len")
}
func TestTruncateRemovesLeafBlocks(t *testing.T) {
	t.Skip("04 §13.1 — fully truncated leaves freed")
}
func TestTruncateEmptiesTreeBackToInline(t *testing.T) {
	t.Skip("04 §13.1 + §9 — full truncate returns inode to extent_depth=0")
}

// ---- invariant fuzz ----

func TestInvariantFuzzWriteTruncate(t *testing.T) {
	t.Skip("04 §13.1 — random write/truncate sequence preserves §6 invariants")
}
