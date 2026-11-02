// Package transport implements framed local control and binary data streams.
package transport

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"fluxd/internal/metadata"
)

// MaxFrame bounds every JSON message before allocation.
const MaxFrame = 4 << 20

// Request is one control operation. Limit and Offset paginate lists.
type Request struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// Response precedes a binary body only for successful OpenAcquisition requests.
type Response struct {
	Error   string            `json:"error,omitempty"`
	Record  *metadata.Record  `json:"record,omitempty"`
	Records []metadata.Record `json:"records,omitempty"`
	Status  string            `json:"status,omitempty"`
	Bytes   int64             `json:"bytes,omitempty"`
}

// WriteFrame emits a bounded network-order length and strict JSON body.
func WriteFrame(w io.Writer, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > MaxFrame {
		return fmt.Errorf("frame too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	n, e := w.Write(header[:])
	if e != nil {
		return e
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	n, e = w.Write(b)
	if e == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return e
}

// ReadFrame checks the length before allocating and rejects unknown fields.
func ReadFrame(r io.Reader, v any) error { return readFrame(r, v, MaxFrame) }

func readFrame(r io.Reader, v any, limit uint32) error {
	var h [4]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > limit {
		return fmt.Errorf("invalid frame length")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(r, b); e != nil {
		return e
	}
	return strict(b, v)
}

func strict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var x json.RawMessage
	if d.Decode(&x) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
