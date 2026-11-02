package transport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"testing"

	"fluxd/internal/compression"
	"fluxd/internal/metadata"
	"fluxd/internal/testutil"
	"fluxd/internal/transport"
)

func TestBoundedHash(t *testing.T) {
	s := &boundedHash{expected: 2, h: sha256.New()}
	if _, e := s.Write([]byte{1, 2}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write([]byte{3}); e == nil {
		t.Fatal("unbounded output")
	}
}

func TestConsumeStream(t *testing.T) {
	m, b := testutil.Fixture()
	for _, name := range []string{"identity", "deflate", "ccsds-123"} {
		for _, corrupt := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", name, corrupt), func(t *testing.T) {
				codec, _ := compression.Select(name)
				codec, _ = compression.Configure(codec, m)
				var encoded bytes.Buffer
				codec.Encode(context.Background(), &encoded, bytes.NewReader(b), make([]byte, 4096))
				data := encoded.Bytes()
				h := sha256.Sum256(data)
				raw := sha256.Sum256(b)
				m.Compression = metadata.Compression{Codec: name, Mode: "lossless"}
				m.CompressedBytes = int64(len(data))
				m.UncompressedBytes = int64(len(b))
				m.Checksum = hex.EncodeToString(h[:])
				m.RawChecksum = hex.EncodeToString(raw[:])
				if corrupt {
					m.RawChecksum = "bad"
				}
				server, client := net.Pipe()
				done := make(chan struct{})
				go func() {
					defer close(done)
					defer server.Close()
					var q transport.Request
					if transport.ReadFrame(server, &q) != nil {
						return
					}
					if e := transport.WriteFrame(server, transport.Response{Record: &m, Bytes: int64(len(data))}); e != nil {
						return
					}
					if _, e := server.Write(data); e != nil {
						return
					}
					if transport.ReadFrame(server, &q) != nil {
						return
					}
					transport.WriteFrame(server, transport.Response{Status: "released"})
				}()
				r, e := consumeConnection(context.Background(), client, m.ID)
				client.Close()
				<-done
				if corrupt {
					if e == nil {
						t.Fatal("corruption accepted")
					}
					return
				}
				if e != nil || r.Samples != 512 {
					t.Fatal(r, e)
				}
			})
		}
	}
}
