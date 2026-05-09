// Package block implements the block device abstraction and the buffer cache.
//
// All disk read/write goes through this layer. The cache provides:
//   - LRU eviction with reference counting (Get/Put pattern).
//   - Write-back policy with dirty tracking.
//   - Journal pinning (JournalLocked) to enforce ordered-mode invariants.
//   - Frozen copies for committing transactions.
//
// See docs/06-block-cache.md for design details.
package block
