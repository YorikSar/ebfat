package ebfat

import (
	"io"
)

type PaddedWriter struct {
	io.Writer
	padding int
	counter int
}

func NewPaddedWriter(w io.Writer, padding int) *PaddedWriter {
	return &PaddedWriter{
		Writer:  w,
		padding: padding,
		counter: 0,
	}
}

func (w *PaddedWriter) Write(buf []byte) (int, error) {
	n, err := w.Writer.Write(buf)
	w.counter = (w.counter + n) % w.padding
	return n, err
}

func (w *PaddedWriter) Pad() error {
	if w.counter == 0 {
		return nil
	}
	_, err := w.Writer.Write(make([]byte, w.padding-w.counter))
	w.counter = 0
	return err
}
