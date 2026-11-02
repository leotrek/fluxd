// Command consumer is a development-only OCI validation client, not a daemon component.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"fluxd/internal/compression"
	"fluxd/internal/metadata"
	"fluxd/internal/transport"
)

type processor struct {
	m                      metadata.Record
	h                      io.Writer
	n, expected, failAfter int64
	samples, sum, min, max int64
	pending                byte
	odd                    bool
}

func (p *processor) Write(b []byte) (int, error) {
	if int64(len(b)) > p.expected-p.n {
		return 0, fmt.Errorf("decoded output exceeds metadata")
	}
	if p.failAfter > 0 && p.n+int64(len(b)) >= p.failAfter {
		return 0, fmt.Errorf("injected consumer failure")
	}
	if _, e := p.h.Write(b); e != nil {
		return 0, e
	}
	for _, v := range b {
		if p.m.StorageBitDepth == 16 && !p.odd {
			p.pending = v
			p.odd = true
			continue
		}
		sample := int64(v)
		if p.m.StorageBitDepth == 16 {
			pair := []byte{p.pending, v}
			if p.m.ByteOrder == "little" {
				sample = int64(binary.LittleEndian.Uint16(pair))
			} else {
				sample = int64(binary.BigEndian.Uint16(pair))
			}
			p.odd = false
		}
		if p.m.SampleType == "int" {
			if p.m.StorageBitDepth == 16 {
				sample = int64(int16(sample))
			} else {
				sample = int64(int8(sample))
			}
		}
		if p.samples == 0 || sample < p.min {
			p.min = sample
		}
		if p.samples == 0 || sample > p.max {
			p.max = sample
		}
		p.sum += sample
		p.samples++
	}
	p.n += int64(len(b))
	return len(b), nil
}
func run() error {
	socket := flag.String("socket", "/fluxd/fluxd.sock", "Unix socket")
	id := flag.String("id", "optical-0001", "READY acquisition")
	failAfter := flag.Int64("fail-after", 0, "inject failure during decode (development only)")
	flag.Parse()
	start := time.Now()
	conn, e := transport.Dial(*socket)
	if e != nil {
		return e
	}
	defer conn.Close()
	res, e := transport.Call(conn, transport.Request{Op: "OpenAcquisition", ID: *id})
	if e != nil {
		return e
	}
	if res.Record == nil || res.Record.ID != *id || res.Record.Status != "READY" || res.Bytes < 0 || res.Bytes != res.Record.CompressedBytes {
		return fmt.Errorf("invalid open response")
	}
	m := *res.Record
	n, e := m.Size()
	if e != nil {
		return e
	}
	if n != m.UncompressedBytes || n > 1<<30 {
		return fmt.Errorf("invalid or excessive raw size")
	}
	if m.Compression.Mode != "lossless" || (m.Compression.Codec == "ccsds-123" && (m.Compression.Standard != "CCSDS 123.0-B-1" || m.Compression.Profile != compression.CCSDSProfile)) {
		return fmt.Errorf("unsupported compression profile")
	}
	codec, e := compression.Select(m.Compression.Codec)
	if e != nil {
		return e
	}
	codec, e = compression.Configure(codec, m)
	if e != nil {
		return e
	}
	rawHash, encodedHash := sha256.New(), sha256.New()
	lr := &io.LimitedReader{R: conn, N: res.Bytes}
	p := &processor{m: m, h: rawHash, expected: n, failAfter: *failAfter}
	decodeStart := time.Now()
	if e = codec.Decode(context.Background(), p, io.TeeReader(lr, encodedHash), make([]byte, 1<<20)); e != nil {
		return e
	}
	processingSeconds := time.Since(decodeStart).Seconds()
	if lr.N != 0 || p.n != n || p.odd || hex.EncodeToString(rawHash.Sum(nil)) != m.RawChecksum || hex.EncodeToString(encodedHash.Sum(nil)) != m.Checksum {
		return fmt.Errorf("integrity failure")
	}
	if _, e = transport.Call(conn, transport.Request{Op: "ReleaseAcquisition", ID: *id}); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"id": *id, "verified": true, "samples": p.samples, "sum": p.sum, "min": p.min, "max": p.max, "decode_verify_process_seconds": processingSeconds, "consumer_seconds": time.Since(start).Seconds()})
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
