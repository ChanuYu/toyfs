// Package inode implements the on-disk inode (256 B) and its in-memory view.
//
// Each inode stores the type/permission bits, ownership, link count, sizes,
// timestamps, the extent-tree header, snapshot bookkeeping (refcount,
// generation), and an inline-data area used for fast symlinks.
//
// See docs/03-inode.md for the on-disk layout, lifecycle, permission check,
// and the full-inode CoW model.
package inode
