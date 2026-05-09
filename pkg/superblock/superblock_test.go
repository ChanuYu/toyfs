package superblock

import "testing"

// Skeleton tests derived from docs/02-superblock.md §8.1.

// ---- serialization ----

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	t.Skip("02 §8.1 — populate fields, marshal+unmarshal yields equal struct")
}

// ---- CRC verification ----

func TestVerifyAcceptsCleanSuperblock(t *testing.T) {
	t.Skip("02 §8.1 — Verify() OK on intact image")
}
func TestVerifyDetectsPayloadCorruption(t *testing.T) {
	t.Skip("02 §8.1 — flip 1 byte → Verify fails")
}
func TestVerifyDetectsChecksumCorruption(t *testing.T) {
	t.Skip("02 §8.1 — flip checksum field → Verify fails")
}

// ---- magic / version ----

func TestMountRejectsWrongMagic(t *testing.T) {
	t.Skip("02 §8.1 — non-TOYF magic → mount refused")
}
func TestMountRejectsUnsupportedVersion(t *testing.T) {
	t.Skip("02 §8.1 — version != 1 → mount refused")
}

// ---- geometry invariants ----

func TestMkfsRejectsOverlappingRegions(t *testing.T) {
	t.Skip("02 §8.1 — overlapping start+blocks → mkfs error")
}
func TestMkfsRejectsDataBeyondImage(t *testing.T) {
	t.Skip("02 §8.1 — data_block_start+count > total_blocks-1 → error")
}

// ---- flags ----

func TestFlagsSetClear(t *testing.T) {
	t.Skip("02 §8.1 — TOYFS_SB_CLEAN/HAS_JOURNAL/HAS_SNAPSHOT/READ_ONLY toggle correctly")
}
