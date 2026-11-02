package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"

	"fluxd/internal/compression"
	"fluxd/internal/storage"
	"fluxd/internal/testutil"
)

func TestPipeConsumers(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), 1<<20)
	m, b := testutil.Fixture()
	codec, _ := compression.Select("ccsds-123")
	if e := s.Put(context.Background(), m, bytes.NewReader(b), codec, 4096, 2048); e != nil {
		t.Fatal(e)
	}
	codec, _ = compression.Configure(codec, m)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server, client := net.Pipe()
			done := make(chan error, 1)
			go func() { defer server.Close(); done <- handle(context.Background(), server, s, 32) }()
			defer func() { client.Close(); <-done }()
			for _, q := range []Request{{Op: "GetStatus"}, {Op: "ListAcquisitions", Limit: 1}, {Op: "ListAcquisitions", Offset: 100}, {Op: "GetAcquisition", ID: m.ID}} {
				if _, e := Call(client, q); e != nil {
					t.Error(e)
					return
				}
			}
			for _, q := range []Request{{Op: "What"}, {Op: "GetAcquisition", ID: "missing"}, {Op: "GetAcquisition", ID: "../bad"}, {Op: "OpenAcquisition", ID: "missing"}, {Op: "ReleaseAcquisition", ID: m.ID}, {Op: "ListAcquisitions", Limit: 300}} {
				if _, e := Call(client, q); e == nil {
					t.Error("accepted", q)
					return
				}
			}
			for j := 0; j < 2; j++ {
				r, e := Call(client, Request{Op: "OpenAcquisition", ID: m.ID})
				if e != nil {
					t.Error(e)
					return
				}
				var out bytes.Buffer
				if e = codec.Decode(context.Background(), &out, io.LimitReader(client, r.Bytes), make([]byte, 4096)); e != nil {
					t.Error(e)
					return
				}
				if !bytes.Equal(out.Bytes(), b) {
					t.Error("payload mismatch")
					return
				}
				if _, e = Call(client, Request{Op: "OpenAcquisition", ID: m.ID}); e == nil {
					t.Error("double open")
					return
				}
				if _, e = Call(client, Request{Op: "ReleaseAcquisition", ID: m.ID}); e != nil {
					t.Error(e)
					return
				}
			}
		}()
	}
	wg.Wait()
}
