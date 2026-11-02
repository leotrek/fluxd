package compression

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"fluxd/internal/testutil"
)

type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestCodecs(t *testing.T) {
	m, data := testutil.Fixture()
	if _, e := Select("unknown"); e == nil {
		t.Fatal("unavailable codec")
	}
	for _, name := range []string{"identity", "deflate"} {
		c, _ := Select(name)
		if c.ID() != name || c.Validate(m) != nil {
			t.Fatal(name)
		}
		for _, input := range [][]byte{nil, data} {
			var encoded, out, again bytes.Buffer
			buf := make([]byte, 17)
			if e := c.Encode(context.Background(), &encoded, bytes.NewReader(input), buf); e != nil {
				t.Fatal(e)
			}
			if e := c.Encode(context.Background(), &again, bytes.NewReader(input), buf); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(encoded.Bytes(), again.Bytes()) {
				t.Fatal("not deterministic")
			}
			if e := c.Decode(context.Background(), &out, &encoded, buf); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(input, out.Bytes()) {
				t.Fatal("roundtrip")
			}
		}
		if e := c.Encode(context.Background(), broken{}, bytes.NewReader(data), make([]byte, 17)); e == nil {
			t.Fatal("write failure")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := c.Encode(ctx, io.Discard, bytes.NewReader(data), make([]byte, 17)); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	if _, e := Copy(context.Background(), io.Discard, bytes.NewReader(data), nil); e == nil {
		t.Fatal("zero buffer")
	}
	c, _ := Select("deflate")
	if e := c.Decode(context.Background(), io.Discard, bytes.NewReader([]byte("bad")), make([]byte, 4)); e == nil {
		t.Fatal("bad deflate")
	}
}
func BenchmarkEncode(b *testing.B) {
	m, data := testutil.Fixture()
	for _, name := range []string{"identity", "deflate", "ccsds-123"} {
		b.Run(name, func(b *testing.B) {
			c, _ := Select(name)
			c, _ = Configure(c, m)
			buf := make([]byte, 64<<10)
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if e := c.Encode(context.Background(), io.Discard, bytes.NewReader(data), buf); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}
