package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"fluxd/internal/compression"
	"fluxd/internal/testutil"
)

func TestPersistenceRecovery(t *testing.T) {
	root := t.TempDir()
	s, e := Open(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	m, b := testutil.Fixture()
	c, _ := compression.Select("deflate")
	if e = s.Put(context.Background(), m, bytes.NewReader(b), c, 128, 2048); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(context.Background(), m, bytes.NewReader(b), c, 128, 2048); e == nil {
		t.Fatal("duplicate")
	}
	got, e := s.Get(m.ID)
	if e != nil || got.Status != "READY" || got.RawChecksum == "" {
		t.Fatal(got, e)
	}
	f, _, e := s.Reader(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = c.Decode(context.Background(), &out, f, make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if !bytes.Equal(b, out.Bytes()) {
		t.Fatal("corruption")
	}
	os.Mkdir(filepath.Join(root, "tmp", "interrupted"), 0700)
	s, e = Open(root, 1<<20)
	if e != nil || len(s.List()) != 1 {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "tmp"))
	if len(entries) != 0 {
		t.Fatal("recovery")
	}
	if _, e = s.Get("../bad"); e == nil {
		t.Fatal("unknown")
	}
	if _, _, e = s.Reader("missing"); e == nil {
		t.Fatal("unknown reader")
	}
	os.WriteFile(filepath.Join(root, "acquisitions", m.ID, "payload"), []byte("corrupt"), 0600)
	if _, e = Open(root, 1<<20); e == nil {
		t.Fatal("integrity")
	}
}
func TestFailures(t *testing.T) {
	m, b := testutil.Fixture()
	c, _ := compression.Select("identity")
	for _, tc := range []struct {
		name       string
		limit, max int64
		data       []byte
	}{{"capacity", 10, 2048, b}, {"max", 2048, 10, b}, {"truncated", 2048, 2048, b[:20]}, {"excess", 4096, 2048, append(append([]byte{}, b...), 0)}} {
		t.Run(tc.name, func(t *testing.T) {
			s, e := Open(t.TempDir(), tc.limit)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Put(context.Background(), m, bytes.NewReader(tc.data), c, 32, tc.max); e == nil {
				t.Fatal("accepted")
			}
			if len(s.List()) != 0 {
				t.Fatal("published failed acquisition")
			}
			a, _ := os.ReadDir(filepath.Join(s.root, "tmp"))
			if len(a) != 0 {
				t.Fatal("leaked transaction")
			}
		})
	}
	s, _ := Open(t.TempDir(), 2048)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := s.Put(ctx, m, bytes.NewReader(b), c, 32, 2048); e == nil {
		t.Fatal("cancellation")
	}
	if e := s.Put(context.Background(), m, bytes.NewReader(b), c, 0, 2048); e == nil {
		t.Fatal("buffer")
	}
	if _, e := Open(t.TempDir(), 0); e == nil {
		t.Fatal("limit")
	}
}
func TestAtomicVisibility(t *testing.T) {
	s, _ := Open(t.TempDir(), 2048)
	m, b := testutil.Fixture()
	c, _ := compression.Select("identity")
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Put(context.Background(), m, r, c, 32, 2048) }()
	if len(s.List()) != 0 {
		t.Fatal("incomplete visible")
	}
	if _, e := w.Write(b[:64]); e != nil {
		t.Fatal(e)
	}
	if len(s.List()) != 0 {
		t.Fatal("partially written acquisition visible")
	}
	if _, e := w.Write(b[64:]); e != nil {
		t.Fatal(e)
	}
	w.Close()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if len(s.List()) != 1 {
		t.Fatal("missing")
	}
	r.Close()
}

func TestInterruptedTransactionRecovery(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root, 4096)
	m, b := testutil.Fixture()
	c, _ := compression.Select("identity")
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Put(context.Background(), m, r, c, 32, 4096) }()
	if _, e := w.Write(b[:64]); e != nil {
		t.Fatal(e)
	}
	w.CloseWithError(io.ErrUnexpectedEOF)
	if e := <-done; e == nil {
		t.Fatal("source failure hidden")
	}
	r.Close()
	s, e := Open(root, 4096)
	if e != nil || len(s.List()) != 0 {
		t.Fatal(e)
	}
	if e = s.Put(context.Background(), m, bytes.NewReader(b), c, 32, 4096); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(root, "acquisitions", m.ID, "metadata.json"), []byte(`{"schema_version":999}`), 0600)
	if _, e = Open(root, 4096); e == nil {
		t.Fatal("invalid persisted metadata")
	}
}
func TestWriterBackpressureCancellation(t *testing.T) {
	s, _ := Open(t.TempDir(), 4096)
	s.writer <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	m, b := testutil.Fixture()
	c, _ := compression.Select("identity")
	done := make(chan error, 1)
	go func() { done <- s.Put(ctx, m, bytes.NewReader(b), c, 32, 4096) }()
	cancel()
	if e := <-done; e != context.Canceled {
		t.Fatal(e)
	}
	<-s.writer
	if e := s.Put(context.Background(), m, bytes.NewReader(b), c, 32, 4096); e != nil {
		t.Fatal(e)
	}
}

func TestCCSDSPersistence(t *testing.T) {
	m, b := testutil.Fixture()
	s, e := Open(t.TempDir(), 4096)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := compression.Select("ccsds-123")
	if e = s.Put(context.Background(), m, bytes.NewReader(b), c, 4096, 4096); e != nil {
		t.Fatal(e)
	}
	s, e = Open(s.root, 4096)
	if e != nil {
		t.Fatal(e)
	}
	f, m, e := s.Reader(m.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if m.Compression.Standard != "CCSDS 123.0-B-1" || m.Compression.Profile != compression.CCSDSProfile {
		t.Fatal(m.Compression)
	}
	c, e = compression.Configure(c, m)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = c.Decode(context.Background(), &out, f, make([]byte, 4096)); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(out.Bytes(), b) {
		t.Fatal("reconstruction")
	}
}
