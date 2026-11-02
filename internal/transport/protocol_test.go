package transport

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	q := Request{Op: "GetStatus"}
	if e := WriteFrame(&b, q); e != nil {
		t.Fatal(e)
	}
	var got Request
	if e := ReadFrame(&b, &got); e != nil || got.Op != q.Op {
		t.Fatal(got, e)
	}
	for _, data := range [][]byte{{0, 0, 0, 0}, {255, 255, 255, 255}, {0, 0, 0, 9, '{'}, {0, 0, 0, 1, '!'}} {
		if e := ReadFrame(bytes.NewReader(data), &got); e == nil {
			t.Fatal(data)
		}
	}
	var unknown bytes.Buffer
	WriteFrame(&unknown, map[string]string{"unknown": "value"})
	if e := ReadFrame(&unknown, &got); e == nil {
		t.Fatal("unknown field")
	}
	if e := strict([]byte(`{} {}`), &got); e == nil {
		t.Fatal("trailing")
	}
	if e := WriteFrame(io.Discard, string(make([]byte, MaxFrame))); e == nil {
		t.Fatal("oversize")
	}
}
func FuzzFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Fuzz(func(t *testing.T, b []byte) { var q Request; ReadFrame(bytes.NewReader(b), &q) })
}
func TestRequestBound(t *testing.T) {
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint32(16385))
	var q Request
	if e := readFrame(&b, &q, 16384); e == nil {
		t.Fatal("request bound")
	}
}

type shortFrameWriter struct{ calls, shortOn int }

func (w *shortFrameWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.shortOn {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func TestFrameShortWrites(t *testing.T) {
	for _, shortOn := range []int{1, 2} {
		w := &shortFrameWriter{shortOn: shortOn}
		if e := WriteFrame(w, Request{Op: "GetStatus"}); e != io.ErrShortWrite {
			t.Fatalf("write %d: %v", shortOn, e)
		}
		if w.calls != shortOn {
			t.Fatal("continued writing after truncated frame")
		}
	}
}
