package snapshot

import "testing"

// Skeleton tests derived from docs/08-snapshot.md §12.1.

// ---- create ----

func TestCreateRegistersSlotAndCopiesMeta(t *testing.T) {
	t.Skip("08 §12.1 — slot, bitmap+inode_table copies populated")
}
func TestCreateWhenSlotsFullReturnsENOSPC(t *testing.T) {
	t.Skip("08 §12.1 — 4 active → 5th create fails")
}
func TestCreatePostInvariants(t *testing.T) {
	t.Skip("08 §12.1 — invariants 6, 7 (SHARED set, refcount += 1)")
}

// ---- CoW ----

func TestCoWOnlySharedBlocks(t *testing.T) { t.Skip("08 §12.1 — only refcount>1 blocks cloned") }
func TestRefcountAdjustOnCoW(t *testing.T) { t.Skip("08 §12.1 — old --, new = 1") }
func TestExtentIndexBlockCoW(t *testing.T) { t.Skip("08 §12.1 — promote-with-CoW combo") }
func TestInodeCoWPropagatesToParentDentry(t *testing.T) {
	t.Skip("08 §12.1 — full inode CoW updates parent dentry")
}

// ---- read ----

func TestSnapshotKeepsUnlinkedFile(t *testing.T) {
	t.Skip("08 §12.1 — file unlinked in active still visible in snapshot")
}
func TestSnapshotKeepsOldContent(t *testing.T) {
	t.Skip("08 §12.1 — content modification not visible in snapshot")
}
func TestSnapshotKeepsOldDirEntry(t *testing.T) {
	t.Skip("08 §12.1 — new active dentries hidden from snapshot")
}
func TestSnapshotRootTraversalReachesAllFiles(t *testing.T) { t.Skip("08 §12.1") }

// ---- delete ----

func TestDeleteFreesSnapshotOnlyBlocks(t *testing.T) {
	t.Skip("08 §12.1 — only blocks unique to snapshot freed")
}
func TestDeleteDoesNotFreeSharedBlocks(t *testing.T) {
	t.Skip("08 §12.1 — refcount stays 1 with active")
}
func TestDeleteOnMountedSnapshotReturnsEBUSY(t *testing.T) { t.Skip("08 §12.1") }

// ---- rollback ----

func TestRollbackConstraintsViolated(t *testing.T) {
	t.Skip("08 §12.1 — open file / other op / extra snapshot → EBUSY/EINVAL")
}
func TestRollbackRestoresMetadata(t *testing.T) { t.Skip("08 §12.1 — invariants 10, 11, 12") }
func TestRollbackPostReadsMatchSnapshot(t *testing.T) {
	t.Skip("08 §12.1 — same path returns snapshot content")
}
func TestRollbackPostWriteWorks(t *testing.T) { t.Skip("08 §12.1 — no SHARED → no CoW branch") }

// ---- invariant fuzz ----

func TestInvariantFuzzMixedOpsWithSnapshots(t *testing.T) {
	t.Skip("08 §12.1 — random ops + create/delete preserve invariants 1–9")
}
