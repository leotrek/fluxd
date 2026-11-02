// Package compression contains bounded CPU streaming lossless codecs.
package compression

import (
	"compress/zlib"
	"context"
	"fmt"
	"io"

	"fluxd/internal/metadata"
)

// Backend writes one complete independently decodable acquisition per Encode call.
type Backend interface {
	ID() string
	Validate(metadata.Record) error
	Encode(context.Context, io.Writer, io.Reader, []byte) error
	Decode(context.Context, io.Writer, io.Reader, []byte) error
}
type codec struct{ name string }

// Select chooses a codec. Bind CCSDS with Configure before encoding or decoding.
func Select(name string) (Backend, error) {
	if name == "ccsds-123" {
		return ccsdsCodec{}, nil
	}
	if name != "identity" && name != "deflate" {
		return nil, fmt.Errorf("unsupported codec %q", name)
	}
	return codec{name}, nil
}
func (c codec) ID() string                       { return c.name }
func (c codec) Validate(m metadata.Record) error { _, e := m.Size(); return e }

type reader struct {
	ctx context.Context
	r   io.Reader
}

func (r reader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

// Copy streams with an explicitly owned buffer and cancellation between reads.
func Copy(ctx context.Context, w io.Writer, r io.Reader, b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("empty copy buffer")
	}
	return io.CopyBuffer(struct{ io.Writer }{w}, reader{ctx, r}, b)
}
func (c codec) Encode(ctx context.Context, w io.Writer, r io.Reader, b []byte) error {
	if c.name == "identity" {
		_, e := Copy(ctx, w, r, b)
		return e
	}
	z := zlib.NewWriter(w)
	_, e := Copy(ctx, z, r, b)
	ce := z.Close()
	if e != nil {
		return e
	}
	return ce
}
func (c codec) Decode(ctx context.Context, w io.Writer, r io.Reader, b []byte) error {
	if c.name == "identity" {
		_, e := Copy(ctx, w, r, b)
		return e
	}
	z, e := zlib.NewReader(r)
	if e != nil {
		return e
	}
	defer z.Close()
	_, e = Copy(ctx, w, z, b)
	return e
}
