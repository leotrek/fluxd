// Reconstruction client used only by transport tests.
package transport_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net"

	"fluxd/internal/compression"
	"fluxd/internal/transport"
)

// Result contains only byte-reconstruction evidence for integration tests.
type consumption struct {
	ID          string
	Samples     int64
	RawChecksum string
}
type boundedHash struct {
	n        int64
	expected int64
	h        hash.Hash
}

func (s *boundedHash) Write(p []byte) (int, error) {
	if int64(len(p)) > s.expected-s.n {
		return 0, fmt.Errorf("decoded payload exceeds metadata size")
	}
	s.n += int64(len(p))
	return s.h.Write(p)
}

// Run consumes exactly one acquisition and checks encoded and reconstructed SHA-256.
func consumeAcquisition(ctx context.Context, socket, id string) (consumption, error) {
	var out consumption
	c, e := transport.Dial(socket)
	if e != nil {
		return out, e
	}
	defer c.Close()
	return consumeConnection(ctx, c, id)
}
func consumeConnection(ctx context.Context, c net.Conn, id string) (consumption, error) {
	var out consumption
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	res, e := transport.Call(c, transport.Request{Op: "OpenAcquisition", ID: id})
	if e != nil {
		return out, e
	}
	if res.Record == nil || res.Bytes < 0 || res.Bytes != res.Record.CompressedBytes {
		return out, fmt.Errorf("invalid open response")
	}
	m := *res.Record
	n, e := m.Size()
	if e != nil {
		return out, e
	}
	if m.UncompressedBytes != n {
		return out, fmt.Errorf("inconsistent raw size")
	}
	codec, e := compression.Select(m.Compression.Codec)
	if e == nil {
		codec, e = compression.Configure(codec, m)
	}
	if e != nil {
		return out, e
	}
	h := sha256.New()
	lr := &io.LimitedReader{R: c, N: res.Bytes}
	s := &boundedHash{expected: n, h: sha256.New()}
	if e = codec.Decode(ctx, s, io.TeeReader(lr, h), make([]byte, 64<<10)); e != nil {
		return out, e
	}
	if lr.N != 0 || s.n != n || hex.EncodeToString(h.Sum(nil)) != m.Checksum || hex.EncodeToString(s.h.Sum(nil)) != m.RawChecksum {
		return out, fmt.Errorf("payload integrity failure")
	}
	if _, e = transport.Call(c, transport.Request{Op: "ReleaseAcquisition", ID: id}); e != nil {
		return out, e
	}
	return consumption{ID: id, Samples: n / int64(m.StorageBitDepth/8), RawChecksum: m.RawChecksum}, nil
}
