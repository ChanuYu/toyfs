package superblock

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"time"

	"github.com/ChanuYu/toyfs/pkg/block"
)

const (
	// BlockSizeBytes is the fixed on-disk block size; a superblock occupies
	// exactly one block
	BlockSizeBytes = 4096
	BlockCounts    = 16384
	// PrimaryBlock is the fixed location of the primary superblock
	// The backup lives at total_blocks-1.
	PrimaryBlock = 1

	InodeBlockSize = 256

	// header 1 + block bitmap 1 + inode bitmap 1 + inode table 256
	SnapshotMetaBlocksPerSlot = 1 + 1 + 1 + 256 // 259
	SnaptshotMetaSlots        = 4

	RefcountTableBlocks = 8

	// checksumOffset is the byte offset of the 8-byte integrity region
	// (checkSum + reservedCsumPad). The CRC is computed with these 8 bytes
	// zeroed
	ChecksumOffset = 32 + 64 + 64 + 32 + 16 + 16 + 64 // = 288

	MagicStr = "TOYF"
	Version1 = 1
)

// SnapshotSlot is one entry in the superblock's snapshot slot table (16 B).
type SnapshotSlot struct {
	SnapshotID uint32 // 0 = empty slot
	Flags      uint32
	CreatedAt  uint64
}

type TOYFS_SB_FLAG uint32

const (
	TOYFS_SB_CLEAN                = 1 << 0 // unmount 시 0 으로 clear, mount 시 1로 set
	TOYFS_SB_HAS_JOURNAL          = 1 << 1 // journal 활성
	TOYFS_SB_HAS_SNAPSHOT         = 1 << 2 // snapshot 1개라도 사용 시 1
	TOYFS_SB_READ_ONLY            = 1 << 3 // mount read-only (snapshot mount 시 1)
	TOYFS_SB_ROLLBACK_IN_PROGRESS = 1 << 4 // rolback이 여러 trx 에 걸쳐서 진행 중
)

// Superblock is the in-memory representation of the superblock.
//
// The on-disk byte layout is not modeled as a separate Go struct:
// Marshal/Unmarshal convert directly between this type and a 4096-byte block
type Superblock struct {
	// ---- on-disk fields ----
	// identification (32 B)
	Magic     [4]byte
	Version   uint32
	BlockSize uint32
	Flags     uint32
	UUID      [16]byte

	// geometry (64 B)
	TotalBlocks       uint64
	InodeCount        uint64
	BlockBitmapStart  uint64
	BlockBitmapBlocks uint64
	InodeBitmapStart  uint64
	InodeBitmapBlocks uint64
	InodeTableStart   uint64
	InodeTableBlocks  uint64

	// subsystem regions (64 B)
	JournalStart        uint64
	JournalBlocks       uint64
	SnapshotMetaStart   uint64
	SnapshotMetaBlocks  uint64
	RefcountTableStart  uint64
	RefcountTableBlocks uint64
	DataBlockStart      uint64
	DataBlockCount      uint64

	// runtime counters (32 B)
	FreeBlocks uint64
	FreeInodes uint64
	NextTxnID  uint64 // journal transaction id
	MountCount uint64 // cumulative count of the filesystem being mounted

	// root & snapshot pointer (16 B)
	RootInode        uint32 // reserved on 1
	ActiveSnapshotID uint32 // 0 = active

	// timestamps (16 B)
	MkfsTime      uint64
	LastMountTime uint64

	// snapshot slot table (4 slots x 16 B = 64 B)
	Snapshots [4]SnapshotSlot

	// CheckSum holds the value read at Unmarshal time. It is NOT live state:
	// the moment any field above is mutated it is stale, so it is recomputed
	// in Marshal and validated in Verify — never trusted mid-session
	CheckSum uint32

	// ---- in-memory only (never serialized) ----

	// Dirty marks that this copy has diverged from disk and a Sync is owed.
	// Persisting it would be meaningless: once flushed it is clean by
	// definition.
	Dirty bool

	// DiskOffset is the primary superblock block number (PrimaryBlock = 1).
	DiskOffset uint64

	// BackupOffset is total_blocks-1; both copies are written on Sync/mkfs.
	BackupOffset uint64

	// cache is the back-reference used to write the superblock back through
	// the block cache (see docs/01-disk-layout.md §6 dependency stack). This
	// is the field §5 elides with "...생략".
	cache *block.BlockCache
}

func (sb *Superblock) Marshal() [BlockSizeBytes]byte {
	var b [BlockSizeBytes]byte
	le := binary.LittleEndian
	o := 0

	put := func(v uint64, n int) {
		switch n {
		case 4:
			le.PutUint32(b[o:], uint32(v))
		case 8:
			le.PutUint64(b[o:], v)
		}
		o += n
	}

	// identification (32 B)
	copy(b[o:o+4], sb.Magic[:])
	o += 4
	put(uint64(sb.Version), 4)
	put(uint64(sb.BlockSize), 4)
	put(uint64(sb.Flags), 4)
	copy(b[o:o+16], sb.UUID[:])
	o += 16

	// geometry (64 B)
	put(sb.TotalBlocks, 8)
	put(sb.InodeCount, 8)
	put(sb.BlockBitmapStart, 8)
	put(sb.BlockBitmapBlocks, 8)
	put(sb.InodeBitmapStart, 8)
	put(sb.InodeBitmapBlocks, 8)
	put(sb.InodeTableStart, 8)
	put(sb.InodeTableBlocks, 8)

	// subsystem regions (64 B)
	put(sb.JournalStart, 8)
	put(sb.JournalBlocks, 8)
	put(sb.SnapshotMetaStart, 8)
	put(sb.SnapshotMetaBlocks, 8)
	put(sb.RefcountTableStart, 8)
	put(sb.RefcountTableBlocks, 8)
	put(sb.DataBlockStart, 8)
	put(sb.DataBlockCount, 8)

	// runtime counters (32 B)
	put(sb.FreeBlocks, 8)
	put(sb.FreeInodes, 8)
	put(sb.NextTxnID, 8)
	put(sb.MountCount, 8)

	// root & snapshot pointer (16 B)
	put(uint64(sb.RootInode), 4)
	put(uint64(sb.ActiveSnapshotID), 4)
	put(0, 8) // reservedRootPad

	// timestamps (16 B)
	put(sb.MkfsTime, 8)
	put(sb.LastMountTime, 8)

	// snapshot slot table (64 B)
	for _, s := range sb.Snapshots {
		put(uint64(s.SnapshotID), 4)
		put(uint64(s.Flags), 4)
		put(s.CreatedAt, 8)
	}

	sum := crc32.ChecksumIEEE(b[:])
	le.PutUint32(b[ChecksumOffset:], sum)
	sb.CheckSum = sum

	return b
}

func (sb *Superblock) Unmarshal(b *[BlockSizeBytes]byte) {
	le := binary.LittleEndian
	o := 0

	getU32 := func() uint32 { v := le.Uint32(b[o:]); o += 4; return v }
	getU64 := func() uint64 { v := le.Uint64(b[o:]); o += 8; return v }

	copy(sb.Magic[:], b[o:o+4])
	o += 4
	sb.Version = getU32()
	sb.BlockSize = getU32()
	sb.Flags = getU32()
	copy(sb.UUID[:], b[o:o+16])
	o += 16

	sb.TotalBlocks = getU64()
	sb.InodeCount = getU64()
	sb.BlockBitmapStart = getU64()
	sb.BlockBitmapBlocks = getU64()
	sb.InodeBitmapStart = getU64()
	sb.InodeBitmapBlocks = getU64()
	sb.InodeTableStart = getU64()
	sb.InodeTableBlocks = getU64()

	sb.JournalStart = getU64()
	sb.JournalBlocks = getU64()
	sb.SnapshotMetaStart = getU64()
	sb.SnapshotMetaBlocks = getU64()
	sb.RefcountTableStart = getU64()
	sb.RefcountTableBlocks = getU64()
	sb.DataBlockStart = getU64()
	sb.DataBlockCount = getU64()

	sb.FreeBlocks = getU64()
	sb.FreeInodes = getU64()
	sb.NextTxnID = getU64()
	sb.MountCount = getU64()

	sb.RootInode = getU32()
	sb.ActiveSnapshotID = getU32()
	_ = getU64() // reservedRootPad

	sb.MkfsTime = getU64()
	sb.LastMountTime = getU64()

	for i := range sb.Snapshots {
		sb.Snapshots[i].SnapshotID = getU32()
		sb.Snapshots[i].Flags = getU32()
		sb.Snapshots[i].CreatedAt = getU64()
	}

	sb.CheckSum = le.Uint32(b[ChecksumOffset:])

	sb.Dirty = false
	sb.DiskOffset = PrimaryBlock
	if sb.TotalBlocks > 0 {
		sb.BackupOffset = sb.TotalBlocks - 1
	}
}

func Verify(b *[BlockSizeBytes]byte) error {
	le := binary.LittleEndian

	// magic number comparison
	if string(b[0:4]) != MagicStr {
		return fmt.Errorf("superblock: bad magic %q\n", b[0:4])
	}

	// version comparison
	if got := le.Uint32(b[4:]); got != Version1 {
		return fmt.Errorf("superblock: bad version %d\n", got)
	}

	// validate checksum
	stored := le.Uint32(b[ChecksumOffset:])
	var tmp [BlockSizeBytes]byte
	copy(tmp[:], b[:])
	le.PutUint32(tmp[ChecksumOffset:], 0)
	le.PutUint32(tmp[ChecksumOffset+4:], 0)

	if got := crc32.ChecksumIEEE(tmp[:]); got != stored {
		return fmt.Errorf("superblock: bad checksum (stored: %#x, computed: %#x)\n", stored, got)
	}

	return nil
}

type ToyfsOps struct {
	NumInode      uint64
	JournalSizeMB uint64 // default: 1
}

func (sb *Superblock) BitmapSet(bitmap string, offset uint64) {
	// inode / data block 구분
	var targetStart uint64
	switch bitmap {
	case "inode":
		targetStart = sb.InodeBitmapStart
	case "data":
		targetStart = sb.BlockBitmapStart
	}

	// 오프셋으로 타겟 블록 계산 및 읽기
	bc, err := sb.cache.Device.Read(targetStart)
	if err != nil {
		fmt.Printf("BitmapSet: Could not get %s bitmap block\n", bitmap)
		return
	}

	// 0번 inode x, root inode 1부터 시작
	nBytes := (offset - 1) / 8
	innerOffset := (offset - 1) % 8
	mask := byte(1 << innerOffset)
	if (*bc)[nBytes]&mask != 0 {
		fmt.Printf("BitmapSet: %s Block %d is already set\n", bitmap, offset)
		return
	}
	(*bc)[nBytes] |= 1 << innerOffset // byte sequence 내에서 오프셋 계산해서 set

	sb.cache.Device.Write(targetStart, bc)
}

// Mkfs formats dev as a fresh toyfs filesystem and returns the resulting
// in-memory superblock. The caller owns dev's lifecycle: it opens/sizes the
// image (or supplies an in-memory mock) before calling, and fsyncs/closes
// after. Mkfs writes directly through dev (no block cache) since it is a
// one-shot format.
func Mkfs(dev *block.BlockDevice, toyfsOps *ToyfsOps) (*Superblock, error) {
	// §6.1.2 — compute geometry (all *Start / *Blocks) from dev.NumBlocks.
	// See docs/01-disk-layout.md §4 / §7 for the size-derivation rules.
	// TODO: derive blockBitmap, inodeBitmap, inodeTable, journal,
	//       snapshotMeta, refcountTable, dataBlock regions.
	sb := &Superblock{}
	copy(sb.Magic[:], MagicStr)
	sb.Version = Version1
	sb.BlockSize = BlockSizeBytes
	sb.Flags = TOYFS_SB_CLEAN | TOYFS_SB_HAS_JOURNAL // §6.1.8

	// Make random bytes and fill UUID with it
	rand.Read(sb.UUID[:])
	// TODO: sb.TotalBlocks / region fields / counters / RootInode = 1 / timestamps
	sb.TotalBlocks = BlockCounts
	sb.InodeCount = toyfsOps.NumInode
	sb.BlockBitmapStart = PrimaryBlock + 1
	sb.BlockBitmapBlocks = 1
	sb.InodeBitmapStart = sb.BlockBitmapStart + sb.BlockBitmapBlocks
	sb.InodeBitmapBlocks = 1
	sb.InodeTableStart = sb.InodeBitmapStart + sb.InodeBitmapBlocks
	sb.InodeTableBlocks = sb.InodeCount / (BlockSizeBytes / InodeBlockSize)

	sb.JournalStart = sb.InodeTableStart + sb.InodeTableBlocks
	sb.JournalBlocks = toyfsOps.JournalSizeMB * (1024 * 1024) / BlockSizeBytes
	sb.SnapshotMetaStart = sb.JournalStart + sb.JournalBlocks
	sb.SnapshotMetaBlocks = SnapshotMetaBlocksPerSlot * SnaptshotMetaSlots
	sb.RefcountTableStart = sb.SnapshotMetaStart + sb.SnapshotMetaBlocks
	sb.RefcountTableBlocks = RefcountTableBlocks
	sb.DataBlockStart = sb.RefcountTableStart + sb.RefcountTableBlocks
	sb.DataBlockCount = sb.TotalBlocks - (1 + sb.BlockBitmapBlocks + sb.InodeBitmapBlocks + sb.InodeTableBlocks +
		sb.JournalBlocks + sb.SnapshotMetaBlocks + sb.RefcountTableBlocks)

	sb.FreeBlocks = sb.DataBlockCount
	sb.FreeInodes = sb.InodeTableBlocks - 1 // reserved for root inode

	sb.RootInode = 1 // reserved value
	sb.MkfsTime = uint64(time.Now().Unix())

	// §6.1.3 — initialize each region (bitmaps, inode table, refcount table).
	// Per docs/01-disk-layout.md §3.3, only data blocks are bitmap-tracked.

	// §6.1.4 — create root inode (#1) as a directory with "." / ".." dentries.
	// TODO (depends on pkg/inode, pkg/dentry)
	sb.BitmapSet("inode", 1) // set root inode

	// §6.1.5 — initialize the journal superblock (first block of journal area).
	// TODO (depends on pkg/journal)

	// Marshal (computes CRC) and write to primary + backup.
	blk := sb.Marshal()
	if err := dev.Write(PrimaryBlock, &blk); err != nil {
		return nil, fmt.Errorf("mkfs: write primary superblock: %w", err)
	}
	if err := dev.Write(sb.BackupOffset, &blk); err != nil {
		return nil, fmt.Errorf("mkfs: write backup superblock: %w", err)
	}

	// fsync.
	if err := dev.Sync(); err != nil {
		return nil, fmt.Errorf("mkfs: sync: %w", err)
	}

	return sb, nil
}

func Mount(sb *Superblock, path string) error {
	return nil
}

func Unmount(sb *Superblock) error {
	return nil
}
