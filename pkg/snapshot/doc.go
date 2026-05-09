// Package snapshot implements read-only point-in-time snapshots and the
// rollback flow.
//
// A snapshot copies the bitmaps and inode table verbatim into the
// snapshot_meta region (whole-copy model) and bumps the per-block refcount
// for every data and extent-index block. Active writes that touch a shared
// block trigger CoW (refcount > 1 means clone-on-write).
//
// Rollback is supported under three simplifying constraints (no open files,
// no concurrent ops, no other snapshots present); see docs/08-snapshot.md §9.
package snapshot
