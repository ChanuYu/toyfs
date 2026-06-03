package superblock

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"

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

	// checksumOffset is the byte offset of the 8-byte integrity region
	// (checkSum + reservedCsumPad). The CRC is computed with these 8 bytes
	// zeroed
	checksumOffset = 32 + 64 + 64 + 32 + 16 + 16 + 64 // = 288

	magicStr = "TOYF"
	version1 = 1
)

// SnapshotSlot is one entry in the superblock's snapshot slot table (16 B).
type SnapshotSlot struct {
	snapshotID uint32 // 0 = empty slot
	flags      uint32
	createdAt  uint64
}

type Superblock struct {
	// identification (32 B)
	magic     [4]uint8
	version   uint32
	blockSize uint32
	flags     uint32
	uuid      [16]uint8

	// geometry (64 B)
	totalBlocks       uint64
	inodeCount        uint64
	blockBitmapStart  uint64
	blockBitmapBlocks uint64
	inodeBitmapStart  uint64
	inodeBitmapBlocks uint64
	inodeTableStart   uint64
	inodeTableBlocks  uint64

	// subsystem regions (64 B)
	journalStart        uint64
	journalBlocks       uint64
	snapshotMetaStart   uint64
	snapshotMetaBlocks  uint64
	refcountTableStart  uint64
	refcountTableBlocks uint64
	dataBlockStart      uint64
	dataBlockCount      uint64

	// runtime counters (32 B)
	freeBlocks uint64
	freeInodes uint64
	nextTxnId  uint64 // journal transaction id
	mountCount uint64 // cumulative count of the filesystem being mounted

	// root & snapshot pointer (16 B)
	rootInode        uint32 // reserved on 1
	activeSnapshotId uint32 // 0 = active
	reservedRootPad  uint64

	// timestamps (16 B)
	mkfsTime      uint64
	lastMountTime uint64

	// snapshot slot table (4 slots x 16 B = 64 B)
	snapshots [4]SnapshotSlot

	// integrity (8 B)
	checkSum        uint32 // CRC32 of all preceding bytes (exclue these bytes)
	reservedCsumPad uint32

	// padding to 4096 B
	pad [3800]uint8
}

type TOYFS_SB_FLAG uint32

const (
	TOYFS_SB_CLEAN                = 1 << 0 // unmount 시 0 으로 clear, mount 시 1로 set
	TOYFS_SB_HAS_JOURNAL          = 1 << 1 // journal 활성
	TOYFS_SB_HAS_SNAPSHOT         = 1 << 2 // snapshot 1개라도 사용 시 1
	TOYFS_SB_READ_ONLY            = 1 << 3 // mount read-only (snapshot mount 시 1)
	TOYFS_SB_ROLLBACK_IN_PROGRESS = 1 << 4 // rolback이 여러 trx 에 걸쳐서 진행 중
)

// IMSuperblock is the in-memory representation of the superblock
//
// It is a strict SUPERSET of the on-disk Superblock:
//   - every on-disk field is carried here (so the fs ops layer never has to
//     re-read block 1 to consult geometry, region offsets, or counters);
//   - the on-disk padding (pad[3800], reservedCsumPad) is dropped — dead bytes
//     have no reason to live in RAM;
//   - runtime-only fields are added below. None of them are serialized.
type IMSuperblock struct {
	// ---- on-disk fields (1:1 with Superblock) ----
	magic     [4]byte
	version   uint32
	blockSize uint32
	flags     uint32
	uuid      [16]byte

	totalBlocks       uint64
	inodeCount        uint64
	blockBitmapStart  uint64
	blockBitmapBlocks uint64
	inodeBitmapStart  uint64
	inodeBitmapBlocks uint64
	inodeTableStart   uint64
	inodeTableBlocks  uint64

	journalStart        uint64
	journalBlocks       uint64
	snapshotMetaStart   uint64
	snapshotMetaBlocks  uint64
	refcountTableStart  uint64
	refcountTableBlocks uint64
	dataBlockStart      uint64
	dataBlockCount      uint64

	freeBlocks uint64
	freeInodes uint64
	nextTxnID  uint64
	mountCount uint64

	rootInode        uint32
	activeSnapshotID uint32

	mkfsTime      uint64
	lastMountTime uint64

	snapshots [4]SnapshotSlot

	// checkSum holds the value read at Unmarshal time. It is NOT live state:
	// the moment any field above is mutated it is stale, so it is recomputed
	// in Marshal and validated in Verify — never trusted mid-session
	checkSum uint32

	// ---- in-memory only (never serialized) ----

	// dirty marks that this copy has diverged from disk and a Sync is owed.
	// Persisting it would be meaningless: once flushed it is clean by
	// definition.
	dirty bool

	// diskOffset is the primary superblock block number (PrimaryBlock = 1).
	diskOffset uint64

	// backupOffset is total_blocks-1; both copies are written on Sync/mkfs.
	backupOffset uint64

	// cache is the back-reference used to write the superblock back through
	// the block cache (see docs/01-disk-layout.md §6 dependency stack). This
	// is the field §5 elides with "...생략".
	cache *block.BlockCache
}

func (sb *IMSuperblock) marshal() [BlockSizeBytes]byte {
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
	copy(b[o:o+4], sb.magic[:])
	o += 4
	put(uint64(sb.version), 4)
	put(uint64(sb.blockSize), 4)
	put(uint64(sb.flags), 4)
	copy(b[o:o+16], sb.uuid[:])
	o += 16

	// geometry (64 B)
	put(sb.totalBlocks, 8)
	put(sb.inodeCount, 8)
	put(sb.blockBitmapStart, 8)
	put(sb.blockBitmapBlocks, 8)
	put(sb.inodeBitmapStart, 8)
	put(sb.inodeBitmapBlocks, 8)
	put(sb.inodeTableStart, 8)
	put(sb.inodeTableBlocks, 8)

	// subsystem regions (64 B)
	put(sb.journalStart, 8)
	put(sb.journalBlocks, 8)
	put(sb.snapshotMetaStart, 8)
	put(sb.snapshotMetaBlocks, 8)
	put(sb.refcountTableStart, 8)
	put(sb.refcountTableBlocks, 8)
	put(sb.dataBlockStart, 8)
	put(sb.dataBlockCount, 8)

	// runtime counters (32 B)
	put(sb.freeBlocks, 8)
	put(sb.freeInodes, 8)
	put(sb.nextTxnID, 8)
	put(sb.mountCount, 8)

	// root & snapshot pointer (16 B)
	put(uint64(sb.rootInode), 4)
	put(uint64(sb.activeSnapshotID), 4)
	put(0, 8) // reservedRootPad

	// timestamps (16 B)
	put(sb.mkfsTime, 8)
	put(sb.lastMountTime, 8)

	// snapshot slot table (64 B)
	for _, s := range sb.snapshots {
		put(uint64(s.snapshotID), 4)
		put(uint64(s.flags), 4)
		put(s.createdAt, 8)
	}

	sum := crc32.ChecksumIEEE(b[:])
	le.PutUint32(b[checksumOffset:], sum)
	sb.checkSum = sum

	return b
}

func (sb *IMSuperblock) unmarshal(b *[BlockSizeBytes]byte) {
	le := binary.LittleEndian
	o := 0

	getU32 := func() uint32 { v := le.Uint32(b[o:]); o += 4; return v }
	getU64 := func() uint64 { v := le.Uint64(b[o:]); o += 8; return v }

	copy(sb.magic[:], b[o:o+4])
	o += 4
	sb.version = getU32()
	sb.blockSize = getU32()
	sb.flags = getU32()
	copy(sb.uuid[:], b[o:o+16])
	o += 16

	sb.totalBlocks = getU64()
	sb.inodeCount = getU64()
	sb.blockBitmapStart = getU64()
	sb.blockBitmapBlocks = getU64()
	sb.inodeBitmapStart = getU64()
	sb.inodeBitmapBlocks = getU64()
	sb.inodeTableStart = getU64()
	sb.inodeTableBlocks = getU64()

	sb.journalStart = getU64()
	sb.journalBlocks = getU64()
	sb.snapshotMetaStart = getU64()
	sb.snapshotMetaBlocks = getU64()
	sb.refcountTableStart = getU64()
	sb.refcountTableBlocks = getU64()
	sb.dataBlockStart = getU64()
	sb.dataBlockCount = getU64()

	sb.freeBlocks = getU64()
	sb.freeInodes = getU64()
	sb.nextTxnID = getU64()
	sb.mountCount = getU64()

	sb.rootInode = getU32()
	sb.activeSnapshotID = getU32()
	_ = getU64() // reservedRootPad

	sb.mkfsTime = getU64()
	sb.lastMountTime = getU64()

	for i := range sb.snapshots {
		sb.snapshots[i].snapshotID = getU32()
		sb.snapshots[i].flags = getU32()
		sb.snapshots[i].createdAt = getU64()
	}

	sb.checkSum = le.Uint32(b[checksumOffset:])

	sb.dirty = false
	sb.diskOffset = PrimaryBlock
	if sb.totalBlocks > 0 {
		sb.backupOffset = sb.totalBlocks - 1
	}
}

func verify(b *[BlockSizeBytes]byte) error {
	le := binary.LittleEndian

	// magic number comparison
	if string(b[0:4]) != magicStr {
		return fmt.Errorf("superblock: bad magic %q\n", b[0:4])
	}

	// version comparison
	if got := le.Uint32(b[4:]); got != version1 {
		return fmt.Errorf("superblock: bad version %d\n", got)
	}

	// validate checksum
	stored := le.Uint32(b[checksumOffset:])
	var tmp [BlockSizeBytes]byte
	copy(tmp[:], b[:])
	le.PutUint32(tmp[checksumOffset:], 0)
	le.PutUint32(tmp[checksumOffset+4:], 0)

	if got := crc32.ChecksumIEEE(tmp[:]); got != stored {
		return fmt.Errorf("superblock: bad checksum (stored: %#x, computed: %#x)\n", stored, got)
	}

	return nil
}

func mkfs(path string) (*IMSuperblock, error) {

	// make device image and BlockDevice
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}

	if err = f.Truncate(BlockSizeBytes * BlockCounts); err != nil {
		f.Close()
		return nil, err
	}

	zeroFill := func(f *os.File, size int64) error {
		zero := make([]byte, BlockSizeBytes)
		var written int64
		for written < size {
			_, err := f.WriteAt(zero, written)
			if err != nil {
				return err
			}
			written += BlockSizeBytes
		}

		return f.Sync()
	}
	if err = zeroFill(f, BlockCounts*BlockSizeBytes); err != nil {
		f.Close()
		return nil, err
	}

	dev := &block.BlockDevice{
		File:      f,
		BlockSize: BlockSizeBytes,
		NumBlocks: BlockCounts,
	}

	return nil, nil
}

func mount(sb *IMSuperblock, path string) error {
	return nil
}

func unmount(sb *IMSuperblock) error {
	return nil
}
