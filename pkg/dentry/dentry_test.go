package dentry

import "testing"

// Skeleton tests derived from docs/05-dentry-directory.md §13.1.

// ---- record serialization ----

func TestDentryRoundTripVariousLengths(t *testing.T) {
	t.Skip("05 §13.1 — name length 1, 100, 255 round-trip")
}
func TestDentryTombstoneRoundTrip(t *testing.T) {
	t.Skip("05 §13.1 — inode==0 marshals/unmarshals consistently")
}

// ---- lookup ----

func TestLookupEmptyDir(t *testing.T) {
	t.Skip("05 §13.1 — only ./.. → ENOENT for any other name")
}
func TestLookupSingleBlock(t *testing.T) {
	t.Skip("05 §13.1 — 14 entries fill the rest of the first block")
}
func TestLookupSecondBlock(t *testing.T) {
	t.Skip("05 §13.1 — 16+ entries → entries in block 2 are found")
}
func TestLookupSkipsTombstones(t *testing.T) {
	t.Skip("05 §13.1 — entries on either side of a tombstone resolve")
}
func TestLookupDistinguishesPrefixes(t *testing.T) {
	t.Skip("05 §13.1 — abc vs abcd are not confused")
}

// ---- insert ----

func TestInsertReusesEmptySlot(t *testing.T) {
	t.Skip("05 §13.1 — unlink at slot 5 → next insert lands at slot 5")
}
func TestInsertDuplicateNameReturnsEEXIST(t *testing.T) { t.Skip("05 §13.1 — duplicate name") }
func TestInsertGrowsToSecondBlock(t *testing.T) {
	t.Skip("05 §13.1 — first block full → blocks++, size+=4096")
}

// ---- delete ----

func TestDeleteCreatesTombstone(t *testing.T) {
	t.Skip("05 §13.1 — delete tombstones slot, target nlink--")
}
func TestDeleteMissingNameReturnsENOENT(t *testing.T) { t.Skip("05 §13.1") }
func TestUnlinkOnDirectoryReturnsEISDIR(t *testing.T) {
	t.Skip("05 §13.1 — directories must use rmdir")
}

// ---- mkdir / rmdir ----

func TestMkdirCreatesDotDotDotEntries(t *testing.T) {
	t.Skip("05 §13.1 — child has ./.., parent nlink+1, child nlink==2")
}
func TestRmdirEmpty(t *testing.T)                    { t.Skip("05 §13.1 — empty dir removed → parent nlink--") }
func TestRmdirNonEmptyReturnsENOTEMPTY(t *testing.T) { t.Skip("05 §13.1") }

// ---- rename ----

func TestRenameSameDir(t *testing.T)           { t.Skip("05 §13.1 — name change keeps inode") }
func TestRenameAcrossDirsRegular(t *testing.T) { t.Skip("05 §13.1 — file moved between dirs") }
func TestRenameAcrossDirsDirectoryUpdatesParentRef(t *testing.T) {
	t.Skip("05 §13.1 — moved dir's .. points at new parent")
}

// ---- invariant fuzz ----

func TestInvariantFuzzMixedOps(t *testing.T) {
	t.Skip("05 §13.1 — random mkdir/rmdir/touch/unlink/rename keeps §11 invariants")
}
