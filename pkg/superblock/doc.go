// Package superblock defines the on-disk superblock format and the
// mkfs/mount/unmount lifecycle.
//
// The superblock is the entry point for all filesystem metadata: it records
// the geometry of every region (bitmaps, inode table, journal, snapshot meta,
// refcount table, data blocks) plus runtime counters and the snapshot slot
// table.
//
// See docs/02-superblock.md for the full struct layout and field semantics.
package superblock
