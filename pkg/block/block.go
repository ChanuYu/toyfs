package block

import (
	"os"
	"time"
)

type BlockErr int

const (
	ENOMEM BlockErr = iota
	EREAD
)

func (e BlockErr) Error() string {
	return "BlockErr"
}

// Get은 slot의 RefCount를 증가시키고 반환한다. 캐시에 없으면 디스크에서 로드.
// 호출자는 사용 후 반드시 Put을 호출해야 한다.
func (c *BlockCache) Get(blockNum uint64) (*CachedBlock, error) {
	cb, ok := c.Slots[blockNum]
	if ok {
		cb.RefCount++
		//move LRU
		if cb == c.LRUTail {
			cb.prev.next = cb.next
			c.LRUTail = cb.prev
		} else if cb != c.LRUHead {
			cb.prev.next = cb.next
			cb.next.prev = cb.prev
		}

		if cb != c.LRUHead {
			cb.prev = nil
			cb.next = c.LRUHead
			c.LRUHead.prev = cb
			c.LRUHead = cb
		}

		return cb, nil
	} // end of cache hit

	// cache miss
	if c.Used == c.Capacity {
		// evict tail node
		err := evict_one(c.LRUTail, &c.Used, c.Device)
		if err == ENOMEM {
			return nil, err
		}
		delete(c.Slots, blockNum)
		c.Used--
	}

	// alloc new cached block
	c.Used++
	r_data, err := c.Device.Read(blockNum)
	if err != nil {
		return nil, EREAD
	}
	new_cb := &CachedBlock{
		BlockNum:      blockNum,
		Dirty:         false,
		RefCount:      1,
		Data:          r_data,
		JournalLocked: false,
	}
	c.Slots[blockNum] = new_cb

	// insert to HEAD of LRU
	new_cb.prev = nil
	new_cb.next = c.LRUHead
	c.LRUHead.prev = new_cb
	c.LRUHead = new_cb

	return cb, nil
}

func evict_one(tail *CachedBlock, used *int, dev *BlockDevice) error {
	var cur = tail
	for cur != nil {
		if cur.RefCount > 0 || cur.JournalLocked {
			cur = cur.prev
			continue
		}

		if cur.Dirty {
			dev.Write(cur.BlockNum, cur.Data)
		}

		//Remove cur from LRU
		if cur.prev != nil {
			cur.prev.next = cur.next
		}
		if cur.next != nil {
			cur.next.prev = cur.prev
		}

		(*used)--
	}

	return ENOMEM
}

// Put은 RefCount를 감소시킨다. 0이 되면 eviction 후보로 들어갈 수 있다.
func (c *CachedBlock) Put() {
	c.RefCount--
}

// MarkDirty는 슬롯을 dirty로 마킹하고 DirtyAt을 갱신한다.
// caller는 이미 Get을 호출한 슬롯에 대해 호출.
func (b *CachedBlock) MarkDirty() {
	b.Dirty = true
	b.DirtyAt = time.Now()
}

// Flush는 특정 슬롯을 본위치에 즉시 write한다. journal에 묶여 있으면 에러.
func (c *BlockCache) Flush(b *CachedBlock) error {
	err := c.Device.Write(b.BlockNum, b.Data)
	if err != nil {
		return err
	}
	return nil
}

// FlushAll은 dirty이고 evict 가능한 모든 슬롯을 본위치에 write한다.
func (c *BlockCache) FlushAll() error {
	cur := c.LRUTail
	for cur != nil {
		if cur.Dirty && cur.RefCount == 0 && !cur.JournalLocked {
			// dirty and evictable cached block
			c.Flush(cur)
		}
		cur = cur.prev
	}

	return nil
}

// Sync는 디바이스 fsync 호출.
func (c *BlockCache) Sync() error {
	c.Device.Sync()
	return nil
}

// JournalLock/Unlock은 journal subsystem이 호출. commit 진행 중인 블록을 본위치 flush에서 제외한다.
func (c *BlockCache) JournalLock(b *CachedBlock) {
	b.JournalLocked = true
}
func (c *BlockCache) JournalUnlock(b *CachedBlock) {
	b.JournalLocked = false
}

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

func (d *BlockDevice) Read(blockNum uint64) ([4096]byte, error) {
	//var buf [4096]byte
	var buf [4096]byte
	offset := int64(blockNum * uint64(d.BlockSize))
	_, err := d.File.ReadAt(buf[:], offset)
	if err != nil && err.Error() != "EOF" {
		return buf, err
	}

	return buf, nil
}
func (d *BlockDevice) Write(blockNum uint64, data [4096]byte) error {
	offset := int64(blockNum * uint64(d.BlockSize))
	_, err := d.File.WriteAt(data[:], offset) // O_DIRECT 인 것으로 가정
	if err != nil {
		return err
	}
	return nil
}
func (d *BlockDevice) Sync() error {
	// os.File.Sync()
	return d.File.Sync()
}

type BlockDevice struct {
	File      *os.File // Disk Image File handle
	BlockSize int      // fixed at 4096
	NumBlocks uint64   // the total number of blocks in disk image
}

type IOMode int

const (
	IOCached IOMode = iota
	IODirect
)

func (e IOMode) Error() string {
	return "IOMode"
}

func (d *BlockDevice) ReadMode(blockNum uint64, mode IOMode) ([4096]byte, error) {
	// future feature
	return [4096]byte{}, nil
}
