package ebfat

import (
	"encoding/binary"
	"fmt"
	"io"
	//"log"
	"math/rand"
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

func CreateFat(files []File, out io.Writer, label string) (err error) {
	head := InitBootSectorHead

	_, _ = rand.Read(head.VolumeID[:])
	if label != "" {
		if len(label) > 11 {
			return fmt.Errorf("Label is too long")
		}
		copy(head.PartitionLabel[:], fmt.Sprintf("%-11s", label))
	}

	fileSectorNums := make([]int64, len(files))
	totalSectorNum := int64(0)
	for i, f := range files {
		fileSectorNums[i] = (f.Size + 511) / 512
		totalSectorNum += fileSectorNums[i]
	}
	if totalSectorNum > 512/3*2 { // limit FAT to one sector
		return fmt.Errorf("Files are too large, require %d > %d sectors", totalSectorNum, 512/3*2)
	}
	head.NumberOfSectors += uint16(totalSectorNum)

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

	return nil
}
