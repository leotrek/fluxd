package transport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"testing"

	"fluxd/internal/compression"
	"fluxd/internal/storage"
	"fluxd/internal/testutil"
)

// BenchmarkHandlerPipe measures the real handler and file reads over net.Pipe.
// It deliberately does not claim Unix socket, container, or cold-storage throughput.
func BenchmarkHandlerPipe(b *testing.B) {
	for _, size := range []int{1 << 20, 8 << 20, 32 << 20} {
		for _, bands := range []int64{1, 4, 16} {
			b.Run(fmt.Sprintf("raw_%d/bands_%d", size, bands), func(b *testing.B) {
				m, _ := testutil.Fixture()
				m.Width = 256
				m.Height = int64(size) / (2 * 256 * bands)
				m.Bands = bands
				raw := make([]byte, size)
				for i := 0; i < size/2; i++ {
					v := uint16(i % 4096)
					raw[2*i] = byte(v)
					raw[2*i+1] = byte(v >> 8)
				}
				s, e := storage.Open(b.TempDir(), int64(size)*2)
				if e != nil {
					b.Fatal(e)
				}
				codec, _ := compression.Select("ccsds-123")
				if e = s.Put(context.Background(), m, bytes.NewReader(raw), codec, 64<<10, int64(size)); e != nil {
					b.Fatal(e)
				}
				stored, e := s.Get(m.ID)
				if e != nil {
					b.Fatal(e)
				}
				buffer := make([]byte, 64<<10)
				b.SetBytes(stored.CompressedBytes)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					server, client := net.Pipe()
					done := make(chan error, 1)
					go func() { defer server.Close(); done <- handle(context.Background(), server, s, len(buffer)) }()
					res, e := Call(client, Request{Op: "OpenAcquisition", ID: m.ID})
					if e != nil {
						b.Fatal(e)
					}
					h := sha256.New()
					n, e := compression.Copy(context.Background(), h, io.LimitReader(client, res.Bytes), buffer)
					if e != nil || n != stored.CompressedBytes || hex.EncodeToString(h.Sum(nil)) != stored.Checksum {
						b.Fatalf("transfer integrity: %d %v", n, e)
					}
					if _, e = Call(client, Request{Op: "ReleaseAcquisition", ID: m.ID}); e != nil {
						b.Fatal(e)
					}
					client.Close()
					<-done
				}
			})
		}
	}
}
