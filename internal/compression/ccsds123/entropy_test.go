package ccsds123

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestCodewordsAndStatistics(t *testing.T) {
	c := DefaultConfig(Image{Width: 1, Height: 1, Bands: 1, BitDepth: 8, Layout: "BSQ"})
	s := initialState(c.Entropy)
	if s.acc != 23 || s.count != 2 || s.parameter(8) != 3 {
		t.Fatal(s)
	}
	for _, tc := range []struct {
		mapped uint32
		first  bool
		hex    []byte
	}{{0, false, []byte{0x80}}, {7, false, []byte{0xf0}}, {8, false, []byte{0x40}}, {255, true, []byte{0xff}}} {
		var b bytes.Buffer
		w := bitWriter{w: bufio.NewWriter(&b)}
		if e := encodeResidual(&w, tc.mapped, tc.first, s, c); e != nil {
			t.Fatal(e)
		}
		if e := w.finish(1); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(b.Bytes(), tc.hex) {
			t.Fatalf("%d: %x", tc.mapped, b.Bytes())
		}
		r := bitReader{r: bufio.NewReader(&b)}
		got, e := decodeResidual(&r, tc.first, s, c)
		if e != nil || got != tc.mapped {
			t.Fatal(got, e)
		}
	}
	s = entropyState{acc: 100, count: 63}
	s.update(9, c.Entropy)
	if s.acc != 55 || s.count != 32 {
		t.Fatal(s)
	}
	s.update(4, c.Entropy)
	if s.acc != 59 || s.count != 33 {
		t.Fatal(s)
	}
	var b bytes.Buffer
	w := bitWriter{w: bufio.NewWriter(&b)}
	s = entropyState{acc: 0, count: 2}
	encodeResidual(&w, 255, false, s, c)
	w.finish(1)
	if got := b.Bytes(); len(got) != 4 || got[0] != 0 || got[1] != 0 || got[2] != 0x3f || got[3] != 0xc0 {
		t.Fatalf("escape code: %x", got)
	}
	r := bitReader{r: bufio.NewReader(bytes.NewReader(b.Bytes()[:2]))}
	if _, e := decodeResidual(&r, false, s, c); !errors.Is(e, io.EOF) {
		t.Fatal(e)
	}
}
func TestBitOrderingConcatenationAndPadding(t *testing.T) {
	var out bytes.Buffer
	w := bitWriter{w: bufio.NewWriter(&out)}
	w.put(5, 3)
	w.put(3, 2)
	w.put(1, 1)
	if e := w.finish(4); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(out.Bytes(), []byte{0xbc}) {
		t.Fatalf("got %x", out.Bytes())
	}
	r := bitReader{r: bufio.NewReader(&out)}
	if r.get(3) != 5 || r.get(2) != 3 || r.get(1) != 1 {
		t.Fatal("bit ordering")
	}
	if e := r.finish(4); e != nil {
		t.Fatal(e)
	}
	r = bitReader{r: bufio.NewReader(bytes.NewReader([]byte{0xbf}))}
	r.get(6)
	if e := r.finish(4); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
}

func TestOutOfRangeResidualAndWordPadding(t *testing.T) {
	c := smallConfig()
	c.Image.BitDepth = 8
	state := entropyState{acc: 65535, count: 2}
	var encoded bytes.Buffer
	w := bitWriter{w: bufio.NewWriter(&encoded)}
	w.put(0, 17)
	w.put(1, 1)
	w.put(0, 6)
	w.finish(1)
	r := bitReader{r: bufio.NewReader(&encoded)}
	if _, e := decodeResidual(&r, false, state, c); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
	r = bitReader{r: bufio.NewReader(bytes.NewReader([]byte{0x80, 0x01}))}
	r.get(1)
	if e := r.finish(3); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
	r = bitReader{r: bufio.NewReader(bytes.NewReader([]byte{0x80}))}
	r.get(1)
	if e := r.finish(3); !errors.Is(e, io.EOF) {
		t.Fatal(e)
	}
}
