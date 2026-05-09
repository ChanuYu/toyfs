// Package dentry implements directory entries (264 B) and directory operations.
//
// A directory is itself a regular file with mode S_IFDIR whose data area
// contains a packed array of dentry slots (15 per 4 KiB block, with 136 B
// of slack). The "." and ".." entries are stored explicitly. Deletions use
// inode==0 tombstones; new inserts reuse empty slots before appending.
//
// See docs/05-dentry-directory.md for the on-disk format, lookup/insert/
// delete/rename algorithms, and invariants.
package dentry
