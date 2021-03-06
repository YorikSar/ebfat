package ebfat

import (
	"encoding/binary"
	"fmt"
	"io"
	//"log"
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
	FileNameExt    Dos83FileNameExt
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

	padder := NewPaddedWriter(out, 512)
	// Write first (boot) sector
	err = binary.Write(padder, binary.LittleEndian, &head)
	if err != nil {
		return err
	}
	err = padder.Pad()
	if err != nil {
		return err
	}

	fatWriter := NewFat12Writer(padder)
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
	err = padder.Pad()
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
		dirEntry.FileNameExt.FillFromLongName(f.Name)
		dirEntry.FirstCluster = curCluster
		curCluster += fileSectorNums[i]
		dirEntry.FileSize = uint32(f.Size)

		lfnEntry.ShortNameChecksum = dirEntry.FileNameExt.LFNChecksum()
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
			err = binary.Write(padder, binary.LittleEndian, &lfnEntry)
			if err != nil {
				return err
			}
		}
		err = binary.Write(padder, binary.LittleEndian, &dirEntry)
		if err != nil {
			return err
		}
	}
	err = padder.Pad()
	if err != nil {
		return err
	}

	for _, f := range files {
		_, err = io.CopyN(padder, f, f.Size)
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
		err = padder.Pad()
		if err != nil {
			return err
		}
	}

	return nil
}
