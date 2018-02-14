package ebfat

import (
	"io"
)

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
