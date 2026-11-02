package ccsds123

import (
	"bufio"
	"fmt"
	"io"
)

// MSB-first bit concatenation and zero fill follow section 5.4.3.2.3.
type bitWriter struct {
	w     *bufio.Writer
	value byte
	bits  uint
	bytes int64
	err   error
}

func (w *bitWriter) put(v uint32, n uint) {
	for j := int(n) - 1; j >= 0; j-- {
		w.value = w.value<<1 | byte(v>>uint(j)&1)
		w.bits++
		if w.bits == 8 {
			if w.err == nil {
				w.err = w.w.WriteByte(w.value)
			}
			w.bytes++
			w.bits = 0
			w.value = 0
		}
	}
}
func (w *bitWriter) finish(word int) error {
	if w.bits > 0 {
		w.put(0, 8-w.bits)
	}
	for (HeaderBytes+w.bytes)%int64(word) != 0 {
		w.put(0, 8)
	}
	if w.err != nil {
		return w.err
	}
	return w.w.Flush()
}

type bitReader struct {
	r     *bufio.Reader
	value byte
	bits  uint
	bytes int64
	err   error
}

func (r *bitReader) get(n uint) uint32 {
	var v uint32
	for j := uint(0); j < n; j++ {
		if r.bits == 0 {
			if r.err != nil {
				return 0
			}
			r.value, r.err = r.r.ReadByte()
			if r.err != nil {
				return 0
			}
			r.bytes++
			r.bits = 8
		}
		r.bits--
		v = v<<1 | uint32(r.value>>r.bits&1)
	}
	return v
}
func (r *bitReader) finish(word int) error {
	if r.err != nil {
		return fmt.Errorf("read CCSDS residual: %w", r.err)
	}
	if r.bits > 0 && r.value&byte((1<<r.bits)-1) != 0 {
		return fmt.Errorf("%w: nonzero fill bits", ErrMalformed)
	}
	r.bits = 0
	for (HeaderBytes+r.bytes)%int64(word) != 0 {
		b, e := r.r.ReadByte()
		if e != nil {
			return fmt.Errorf("read CCSDS word fill: %w", e)
		}
		r.bytes++
		if b != 0 {
			return fmt.Errorf("%w: nonzero word padding", ErrMalformed)
		}
	}
	if _, e := r.r.ReadByte(); e != io.EOF {
		if e != nil {
			return e
		}
		return fmt.Errorf("%w: trailing bytes", ErrMalformed)
	}
	return nil
}
