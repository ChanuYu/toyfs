// Package extent implements the extent records and the extent tree used to
// map a file's logical blocks to physical disk blocks.
//
// An extent record is 16 B and represents a contiguous run of disk blocks.
// An inode holds up to 4 inline extents; once that capacity is exceeded the
// tree is promoted to depth 1 (a single index block with up to 255 leaves)
// and ultimately depth 2 (max tree depth).
//
// See docs/04-extent.md for record/index formats, search, append/split/promote,
// truncate, and the first-fit allocator.
package extent
