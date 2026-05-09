package block

import (
	"os"
	"time"
)

type CachedBlock struct {
	BlockNum uint64
	Data     [4096]byte

	// ---- state ----
	Dirty         bool
	RefCount      int
	JournalLocked bool
	DirtyAt       time.Time

	// ---- frozen copy (Ref 07-journaling) ----
	FrozenData *[4096]byte

	// ---- LRU bookkeeping ----
	prev, next *CachedBlock // doubly linked list node
}

type BlockCache struct {
	Slots    map[uint64]*CachedBlock // BlockNum -> slot lookup
	LRUHead  *CachedBlock            // most recently used
	LRUTail  *CachedBlock            // least recently used
	Used     int                     // the number of slots currently used
	Capacity int                     // fixed at 256
	Device   *BlockDevice
}

type BlockDevice struct {
	File      *os.File // Disk Image File handle
	BlockSize int      // fixed at 4096
	NumBlocks uint64   // the total number of blocks in disk image
}
