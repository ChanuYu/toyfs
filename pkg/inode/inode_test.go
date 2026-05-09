package inode

import "testing"

// Skeleton tests derived from docs/03-inode.md §8.1.

// ---- serialization ----

func TestMarshalUnmarshalRegularFile(t *testing.T) { t.Skip("03 §8.1 — round-trip for S_IFREG") }
func TestMarshalUnmarshalDirectory(t *testing.T)   { t.Skip("03 §8.1 — round-trip for S_IFDIR") }
func TestMarshalUnmarshalSymlinkInline(t *testing.T) {
	t.Skip("03 §8.1 — round-trip for fast symlink (size<=60)")
}
func TestMarshalUnmarshalSymlinkSlow(t *testing.T) {
	t.Skip("03 §8.1 — round-trip for slow symlink (size>60)")
}

// ---- CRC ----

func TestVerifyAcceptsCleanInode(t *testing.T) { t.Skip("03 §8.1 — Verify OK") }
func TestVerifyDetectsCorruption(t *testing.T) { t.Skip("03 §8.1 — flip byte → Verify fails") }

// ---- mode interpretation ----

func TestIsRegular(t *testing.T) {
	t.Skip("03 §8.1 — S_IFREG | 0644 → IsRegular & Permission==0644")
}
func TestIsDir(t *testing.T)     { t.Skip("03 §8.1 — S_IFDIR | 0755") }
func TestIsSymlink(t *testing.T) { t.Skip("03 §8.1 — S_IFLNK | 0777") }

// ---- nlink semantics ----

func TestMkdirIncrementsParentNlink(t *testing.T) {
	t.Skip("03 §8.1 — child dir adds .. → parent nlink+1")
}
func TestRmdirDecrementsParentNlink(t *testing.T) {
	t.Skip("03 §8.1 — symmetric to TestMkdirIncrementsParentNlink")
}
func TestHardlinkAdjustsNlink(t *testing.T) { t.Skip("03 §8.1 — link/unlink adjust target.nlink") }

// ---- inline symlink boundary ----

func TestInlineSymlinkAtBoundary60(t *testing.T) {
	t.Skip("03 §8.1 — exactly 60 bytes → inline, blocks==0")
}
func TestSlowSymlinkOver60(t *testing.T) { t.Skip("03 §8.1 — 61+ bytes → extent, blocks==1") }

// ---- permission check ----

func TestRootBypassesMode0000(t *testing.T) { t.Skip("03 §8.1 — uid==0 always passes") }
func TestOwnerUsesOwnerBits(t *testing.T) {
	t.Skip("03 §8.1 — caller.uid==inode.uid → owner triplet")
}
func TestNonOwnerNonGroupUsesOtherBits(t *testing.T) {
	t.Skip("03 §8.1 — fall through to other bits")
}

// ---- lifecycle ----

func TestAllocSetsBitmap(t *testing.T)  { t.Skip("03 §8.1 — alloc → bit set in inode_bitmap") }
func TestFreeClearsBitmap(t *testing.T) { t.Skip("03 §8.1 — free → bit cleared, free_inodes++") }
