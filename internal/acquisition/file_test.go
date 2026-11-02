package acquisition

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"fluxd/internal/testutil"
)

func TestReplay(t *testing.T) {
	m, b := testutil.Fixture()
	p := filepath.Join(t.TempDir(), "raw")
	os.WriteFile(p, b, 0600)
	s, e := Open(p, m, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Metadata().ID != m.ID {
		t.Fatal("metadata")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.ReadContext(ctx, make([]byte, 7)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	var total int
	buf := make([]byte, 7)
	for {
		n, e := s.ReadContext(context.Background(), buf)
		total += n
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	if total != len(b) {
		t.Fatal(total)
	}
	if _, e = s.ReadContext(context.Background(), buf); e != io.EOF {
		t.Fatal(e)
	}
	os.WriteFile(p, b[:10], 0600)
	if _, e = Open(p, m, 0); e == nil {
		t.Fatal("truncated")
	}
	m.Width = 0
	if _, e = Open(p, m, 0); e == nil {
		t.Fatal("invalid")
	}
}
func TestRateCancellation(t *testing.T) {
	m, b := testutil.Fixture()
	p := filepath.Join(t.TempDir(), "raw")
	os.WriteFile(p, b, 0600)
	s, e := Open(p, m, 1)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.ReadContext(context.Background(), make([]byte, 2))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.ReadContext(ctx, make([]byte, 1)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = Open(p, m, -1); e == nil {
		t.Fatal("negative rate")
	}
	if _, e = Open(p+"missing", m, 0); e == nil {
		t.Fatal("missing")
	}
}
func BenchmarkReplay(b *testing.B) {
	m, data := testutil.Fixture()
	p := filepath.Join(b.TempDir(), "raw")
	os.WriteFile(p, data, 0600)
	buf := make([]byte, 128)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, e := Open(p, m, 0)
		if e != nil {
			b.Fatal(e)
		}
		if _, e = io.CopyBuffer(io.Discard, Reader{context.Background(), s}, buf); e != nil {
			b.Fatal(e)
		}
		s.Close()
	}
}
