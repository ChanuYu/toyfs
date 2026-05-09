// Package fs is the operations layer: it wires the lower-level packages
// (block, superblock, inode, extent, dentry, journal, snapshot) into the
// POSIX-like file operations exposed to the FUSE adapter and tests.
//
// Each operation (Lookup, Create, Mkdir, Unlink, Rename, Read, Write,
// Truncate, Chmod, Chown, Stat, Readdir, Link, Symlink, Readlink) opens
// a transaction, mutates the relevant in-memory structures, and commits.
//
// See docs/09-links-permissions.md for permission checks and link semantics,
// and docs/11-roadmap.md §2.G for the implementation plan.
package fs
