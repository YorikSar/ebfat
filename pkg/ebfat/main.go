package ebfat

import (
	"encoding/binary"
	"fmt"
	"io"
	//"log"
	"encoding/base32"
	"hash/fnv"
	"math/rand"
	"unicode/utf16"
)

type File struct {
	Name string
	Size int64
	io.Reader
}

type BootSectorHead struct {
	Jump    [3]byte
	OemName [8]byte
	// BPB
	BytesPerSector    uint16
	SectorsPerCluster uint8
	ReservedSectors   uint16
	NumberOfFATs      uint8
	RootDirEntries    uint16
	NumberOfSectors   uint16
	MediaDescriptor   uint8
	SectorsPerFAT     uint16
	_                 [12]byte // unused
	// Extended BPB
	PhysicalDriveNumber   uint8
	_                     byte // reserved
	ExtendedBootSignature uint8
	VolumeID              [4]byte
	PartitionLabel        [11]byte
	FileSystemType        [8]byte
}

var (
	InitBootSectorHead BootSectorHead = BootSectorHead{
		Jump: [3]byte{0xeb, 0xfe, 0x90}, // jmp $-2, nop

		BytesPerSector:    512,
		SectorsPerCluster: 1,
		ReservedSectors:   1,
		NumberOfFATs:      1,
		RootDirEntries:    16,   // 512 / 32 (size of entry) - minimum
		NumberOfSectors:   3,    // Boot, FAT, root dir - minimum
		MediaDescriptor:   0xf8, // Hard Disk
		SectorsPerFAT:     1,

		PhysicalDriveNumber:   0x80, // first fixed disk
		ExtendedBootSignature: 0x29, // magic for following fields
	}
)

func init() {
	copy(InitBootSectorHead.OemName[:], fmt.Sprintf("%-8s", "ebvirt"))
	copy(InitBootSectorHead.PartitionLabel[:], fmt.Sprintf("%-11s", "NO NAME"))
	copy(InitBootSectorHead.FileSystemType[:], fmt.Sprintf("%-8s", "FAT12"))
}

type DirEntry struct {
	FileNameExt    [11]byte
	Attributes     uint8 // always 0x20 - archive
	ExtendedAttrs  uint8 // always 0x00
	CreateTime10ms uint8
	CreateTime     uint16
	CreateDate     uint16
	AccessDate     uint16
	ExtendedAttrs1 uint16 // always 0x0000
	ModifyTime     uint16
	ModifyDate     uint16
	FirstCluster   uint16
	FileSize       uint32
}
type LFNEntry struct {
	SequenceNumber    uint8
	NamePart1         [5]uint16
	Attributes        uint8 // always 0x0f
	Type              uint8 // always 0x00
	ShortNameChecksum uint8
	NamePart2         [6]uint16
	FirstCluster      uint16 // always 0x0000
	NamePart3         [2]uint16
}

func CreateFat(files []File, out io.Writer, label string) (err error) {
	head := InitBootSectorHead

	_, _ = rand.Read(head.VolumeID[:])
	if label != "" {
		if len(label) > 11 {
			return fmt.Errorf("Label is too long")
		}
		copy(head.PartitionLabel[:], fmt.Sprintf("%-11s", label))
	}

	fileSectorNums := make([]uint16, len(files))
	totalSectorNum := uint16(0)
	for i, f := range files {
		fileSectorNums[i] = uint16((f.Size + 511) / 512)
		totalSectorNum += fileSectorNums[i]
	}
	if totalSectorNum > 512/3*2 { // limit FAT to one sector
		return fmt.Errorf("Files are too large, require %d > %d sectors", totalSectorNum, 512/3*2)
	}
	head.NumberOfSectors += uint16(totalSectorNum)

	fileNames := make([][]uint16, len(files))
	dirEntriesNum := uint16(0)
	for i, f := range files {
		fileNames[i] = utf16.Encode([]rune(f.Name))
		if len(fileNames[i]) > 255 {
			return fmt.Errorf("Length of filename %s is too big", f.Name)
		}
		dirEntriesNum += uint16(len(fileNames[i])+11)/13 + 1 // name is 0-terminated, plus one old-style entry
	}
	head.RootDirEntries = (dirEntriesNum + 15) & 0xfff0 // 16 per sector
	head.NumberOfSectors += head.RootDirEntries/16 - 1

	// Write first (boot) sector
	err = binary.Write(out, binary.LittleEndian, &head)
	if err != nil {
		return err
	}
	sz := binary.Size(&head)
	_, err = out.Write(make([]byte, 512-sz))
	if err != nil {
		return err
	}

	fatWriter := NewFat12Writer(out)
	fatWriter.Write(0xff8) // marker
	for i := uint16(0); i < head.RootDirEntries/16; i++ {
		fatWriter.Write(0xfff)
	}
	next := uint16(3)
	for _, n := range fileSectorNums {
		for ; n > 1; n-- {
			err = fatWriter.Write(next)
			if err != nil {
				return err
			}
			next += 1
		}
		err = fatWriter.Write(0xfff)
		if err != nil {
			return err
		}
	}
	fatWriter.Flush()
	fatSz := (1 + head.RootDirEntries/16 + totalSectorNum + 1) / 2 * 3 // 3 bytes for 2 clusters
	_, err = out.Write(make([]byte, 512-fatSz))
	if err != nil {
		return err
	}

	lfnEntry := LFNEntry{
		Attributes:   0x0f,
		Type:         0x00,
		FirstCluster: 0x0000,
	}
	dirEntry := DirEntry{
		Attributes:     0x20,
		ExtendedAttrs:  0x00,
		CreateTime10ms: 0x00,
		CreateTime:     0x0000, // 00:00:00
		CreateDate:     0x0021, // 1980-01-01
		AccessDate:     0x0021, // 1980-01-01
		ExtendedAttrs1: 0x0000,
		ModifyTime:     0x0000, // 00:00:00
		ModifyDate:     0x0021, // 1980-01-01
	}
	curCluster := 1 + head.RootDirEntries/16
	for i, f := range files {
		Get83FileName(f.Name, &dirEntry.FileNameExt)
		dirEntry.FirstCluster = curCluster
		curCluster += fileSectorNums[i]
		dirEntry.FileSize = uint32(f.Size)

		lfnEntry.ShortNameChecksum = FileNameExtChecksum(&dirEntry.FileNameExt)
		fileName := fileNames[i]
		fileName = append(fileName, 0) // 0-terminated
		padLen := (13 - len(fileName)%13) % 13
		for j := 0; j < padLen; j++ {
			fileName = append(fileName, 0xffff)
		}
		seqNum := uint8(len(fileName) / 13)
		for start := 0; start < len(fileName); start += 13 {
			chunk := fileName[start : start+13]
			if start == 0 {
				lfnEntry.SequenceNumber = 0x40 + seqNum
			} else {
				lfnEntry.SequenceNumber = seqNum
			}
			seqNum -= 1
			copy(lfnEntry.NamePart1[:], chunk[:5])
			copy(lfnEntry.NamePart2[:], chunk[5:11])
			copy(lfnEntry.NamePart3[:], chunk[11:13])
			err = binary.Write(out, binary.LittleEndian, &lfnEntry)
			if err != nil {
				return err
			}
		}
		err = binary.Write(out, binary.LittleEndian, &dirEntry)
		if err != nil {
			return err
		}
	}
	padLen := 512 - dirEntriesNum*32%512
	if padLen != 512 {
		_, err := out.Write(make([]byte, padLen))
		if err != nil {
			return err
		}
	}

	for _, f := range files {
		_, err = io.CopyN(out, f, f.Size)
		if err != nil {
			return err
		}
		_, err = f.Read(make([]byte, 1))
		if err != io.EOF {
			if err != nil {
				return err
			} else {
				return fmt.Errorf("File %s is larger that %d", f.Name, f.Size)
			}
		}
		padLen := 512 - f.Size%512
		if padLen != 512 {
			_, err := out.Write(make([]byte, padLen))
			if err != nil {
				return err
			}
		}
	}

	return nil
}

type Fat12Writer struct {
	w   io.Writer
	buf uint16
}

func NewFat12Writer(w io.Writer) *Fat12Writer {
	return &Fat12Writer{
		w:   w,
		buf: 0xffff,
	}
}
func (fw *Fat12Writer) Write(num uint16) error {
	if fw.buf == 0xffff {
		fw.buf = num
		return nil
	}
	b := [3]byte{
		byte(fw.buf & 0x0ff),
		byte(num&0x00f<<4 + fw.buf&0xf00>>8),
		byte(num & 0xff0 >> 4),
	}
	_, err := fw.w.Write(b[:])
	fw.buf = 0xffff
	return err
}

func (fw *Fat12Writer) Flush() error {
	if fw.buf != 0xffff {
		return fw.Write(0)
	}
	return nil
}

func Get83FileName(fileName string, target *[11]byte) {
	hash := fnv.New64a()
	hash.Write([]byte(fileName))
	hashSum := hash.Sum(nil)
	var buf [16]byte
	b32 := base32.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")
	b32.Encode(buf[:], hashSum)
	copy(target[:], buf[:11])
}

func FileNameExtChecksum(fileNameExt *[11]byte) uint8 {
	sum := uint8(0)
	for _, c := range fileNameExt {
		sum = ((sum & 1) << 7) + (sum >> 1) + c
	}
	return sum
}
