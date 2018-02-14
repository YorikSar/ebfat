package ebfat

import (
	"encoding/base32"
	"hash/fnv"
)

type Dos83FileNameExt struct {
	FileNameExt [11]byte
}

func (df *Dos83FileNameExt) FillFromLongName(fileName string) {
	hash := fnv.New64a()
	hash.Write([]byte(fileName))
	hashSum := hash.Sum(nil)
	var buf [16]byte
	b32 := base32.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")
	b32.Encode(buf[:], hashSum)
	copy(df.FileNameExt[:], buf[:11])
}

func (df *Dos83FileNameExt) LFNChecksum() uint8 {
	sum := uint8(0)
	for _, c := range df.FileNameExt {
		sum = ((sum & 1) << 7) + (sum >> 1) + c
	}
	return sum
}
