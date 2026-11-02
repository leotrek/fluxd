package ccsds123

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

type boundedReader struct {
	ctx       context.Context
	r         io.Reader
	remaining int64
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	if int64(len(p))-1 > r.remaining {
		p = p[:int(r.remaining+1)]
	}
	n, e := r.r.Read(p)
	if int64(n) > r.remaining {
		return 0, fmt.Errorf("%w: encoded input bytes", ErrLimit)
	}
	r.remaining -= int64(n)
	return n, e
}

type boundedWriter struct {
	w         io.Writer
	remaining int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("%w: encoded output bytes", ErrLimit)
	}
	n, e := w.w.Write(p)
	w.remaining -= int64(n)
	if e == nil && n != len(p) {
		e = io.ErrShortWrite
	}
	return n, e
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func readRaw(r *bufio.Reader, f RawFormat, signed bool) (int32, error) {
	first, e := r.ReadByte()
	if e != nil {
		return 0, e
	}
	if f.StorageBits == 8 {
		if signed {
			return int32(int8(first)), nil
		}
		return int32(first), nil
	}
	second, e := r.ReadByte()
	if e != nil {
		if e == io.EOF {
			e = io.ErrUnexpectedEOF
		}
		return 0, e
	}
	value := uint16(first) | uint16(second)<<8
	if f.ByteOrder == "big" {
		value = uint16(first)<<8 | uint16(second)
	}
	if signed {
		return int32(int16(value)), nil
	}
	return int32(value), nil
}
func writeRaw(w *bufio.Writer, f RawFormat, value int32) error {
	first, second := byte(value), byte(uint16(value)>>8)
	if f.StorageBits == 16 && f.ByteOrder == "big" {
		first, second = second, first
	}
	if e := w.WriteByte(first); e != nil {
		return e
	}
	if f.StorageBits == 16 {
		return w.WriteByte(second)
	}
	return nil
}

// walk applies the exact nesting in section 5.4.2 without allocating coordinates.
func walk(i Image, sample func(z, y, x int64) error) error {
	outer, middle, inner := i.Bands, i.Height, i.Width
	switch i.Layout {
	case "BIL":
		outer, middle, inner = i.Height, i.Bands, i.Width
	case "BIP":
		outer, middle, inner = i.Height, i.Width, i.Bands
	}
	for a := int64(0); a < outer; a++ {
		for b := int64(0); b < middle; b++ {
			for c := int64(0); c < inner; c++ {
				z, y, x := a, b, c
				if i.Layout == "BIL" {
					z, y = b, a
				} else if i.Layout == "BIP" {
					z, y, x = c, a, b
				}
				if e := sample(z, y, x); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func stateBytes(workspace []byte, i Image, z int64) []byte {
	bands := i.Bands
	if i.Layout == "BSQ" {
		bands = 1
		z = 0
	}
	offset := int(2*i.Width*bands + 8*z)
	return workspace[offset : offset+8]
}
func requireEOF(r io.Reader) error {
	var b [1]byte
	n, e := io.ReadFull(r, b[:])
	if e == io.EOF && n == 0 {
		return nil
	}
	if e != nil {
		return e
	}
	return fmt.Errorf("%w: extra raw samples", ErrMalformed)
}
