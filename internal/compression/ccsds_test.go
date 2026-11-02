package compression

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"fluxd/internal/compression/ccsds123"
	"fluxd/internal/testutil"
)

type referenceVector struct {
	Depth   int
	Storage int
	Signed  bool
	Layout  string
	Order   string
	Width   int64
	Height  int64
	Bands   int64
	Raw     string
	Encoded string
}

func referenceVectors(t *testing.T) []referenceVector {
	t.Helper()
	b, e := os.ReadFile("testdata/ccsds-profile-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Vectors []referenceVector }
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	if len(fixture.Vectors) != 60 {
		t.Fatal("missing reference vectors")
	}
	return fixture.Vectors
}
func TestCCSDSReferenceInteroperability(t *testing.T) {
	for _, v := range referenceVectors(t) {
		t.Run(fmt.Sprintf("%d/%v/%s/%s", v.Depth, v.Signed, v.Layout, v.Order), func(t *testing.T) {
			m, _ := testutil.Fixture()
			m.Width = v.Width
			m.Height = v.Height
			m.Bands = v.Bands
			m.SampleBitDepth = v.Depth
			m.StorageBitDepth = v.Storage
			m.SampleType = "uint"
			if v.Signed {
				m.SampleType = "int"
			}
			m.Layout = v.Layout
			m.ByteOrder = v.Order
			codec, _ := Select("ccsds-123")
			codec, e := Configure(codec, m)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := hex.DecodeString(v.Raw)
			if e != nil {
				t.Fatal(e)
			}
			encoded, e := hex.DecodeString(v.Encoded)
			if e != nil {
				t.Fatal(e)
			}
			var out bytes.Buffer
			if e = codec.Encode(context.Background(), &out, bytes.NewReader(raw), make([]byte, 1024)); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(out.Bytes(), encoded) {
				for i := range encoded {
					if i >= out.Len() || out.Bytes()[i] != encoded[i] {
						t.Fatalf("reference mismatch at byte %d: Go %x reference %x", i, out.Bytes(), encoded)
					}
				}
				t.Fatal("encoded lengths differ")
			}
			out.Reset()
			if e = codec.Decode(context.Background(), &out, bytes.NewReader(encoded), make([]byte, 1024)); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(out.Bytes(), raw) {
				t.Fatal("reference stream reconstructed incorrectly")
			}
		})
	}
}
func TestCCSDSErrors(t *testing.T) {
	m, raw := testutil.Fixture()
	c, _ := Select("ccsds-123")
	if e := c.Encode(context.Background(), io.Discard, bytes.NewReader(raw), make([]byte, 4096)); e == nil {
		t.Fatal("unbound codec")
	}
	c, e := Configure(c, m)
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{nil, make([]byte, 16)} {
		if e = c.Encode(context.Background(), io.Discard, bytes.NewReader(raw), b); e == nil {
			t.Fatal("working memory")
		}
	}
	var encoded bytes.Buffer
	if e = c.Encode(context.Background(), &encoded, bytes.NewReader(raw), make([]byte, 4096)); e != nil {
		t.Fatal(e)
	}
	for _, data := range [][]byte{encoded.Bytes()[:10], encoded.Bytes()[:encoded.Len()-1], append(append([]byte{}, encoded.Bytes()...), 0)} {
		if e = c.Decode(context.Background(), io.Discard, bytes.NewReader(data), make([]byte, 4096)); e == nil {
			t.Fatal("bad stream accepted")
		}
	}
	bad := append([]byte{}, encoded.Bytes()...)
	bad[12] ^= 4
	if e = c.Decode(context.Background(), io.Discard, bytes.NewReader(bad), make([]byte, 4096)); e == nil {
		t.Fatal("profile mismatch")
	}
	for _, data := range [][]byte{raw[:1], append(append([]byte{}, raw...), 0), bytes.Repeat([]byte{255}, len(raw))} {
		if e = c.Encode(context.Background(), io.Discard, bytes.NewReader(data), make([]byte, 4096)); e == nil {
			t.Fatal("invalid samples")
		}
	}
	if e = c.Encode(context.Background(), broken{}, bytes.NewReader(raw), make([]byte, 4096)); e == nil {
		t.Fatal("write error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = c.Encode(ctx, io.Discard, bytes.NewReader(raw), make([]byte, 4096)); e != context.Canceled {
		t.Fatal(e)
	}
	m.Width = 65537
	if _, e = Configure(c, m); e == nil {
		t.Fatal("dimensions")
	}
}
func FuzzCCSDSDecode(f *testing.F) {
	m, _ := testutil.Fixture()
	c, _ := Select("ccsds-123")
	c, _ = Configure(c, m)
	f.Add([]byte{})
	_, raw := testutil.Fixture()
	var encoded bytes.Buffer
	c.Encode(context.Background(), &encoded, bytes.NewReader(raw), make([]byte, 4096))
	f.Add(encoded.Bytes())
	f.Fuzz(func(t *testing.T, b []byte) {
		c.Decode(context.Background(), io.Discard, bytes.NewReader(b), make([]byte, 4096))
	})
}

func TestCCSDSNarrowAndContainerFormats(t *testing.T) {
	for _, layout := range []string{"BSQ", "BIL", "BIP"} {
		for _, signed := range []bool{false, true} {
			m, _ := testutil.Fixture()
			m.Width = 1
			m.Height = 4
			m.Bands = 1
			m.SampleBitDepth = 8
			m.StorageBitDepth = 16
			m.Layout = layout
			m.SampleType = "uint"
			raw := []byte{0, 0, 255, 0, 128, 0, 1, 0}
			if signed {
				m.SampleType = "int"
				raw = []byte{0, 0, 255, 255, 128, 255, 127, 0}
			}
			c, _ := Select("ccsds-123")
			c, e := Configure(c, m)
			if e != nil {
				t.Fatal(e)
			}
			var encoded, out bytes.Buffer
			if e = c.Encode(context.Background(), &encoded, bytes.NewReader(raw), make([]byte, 32)); e != nil {
				t.Fatal(e)
			}
			if e = c.Decode(context.Background(), &out, &encoded, make([]byte, 32)); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(out.Bytes(), raw) {
				t.Fatal("container mismatch")
			}
		}
	}
	m, _ := testutil.Fixture()
	m.Width = 1
	m.Height = 1
	m.Bands = 1
	m.SampleBitDepth = 10
	c, _ := Select("ccsds-123")
	c, _ = Configure(c, m)
	var encoded bytes.Buffer
	if e := c.Encode(context.Background(), &encoded, bytes.NewReader([]byte{0, 0}), make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	b := encoded.Bytes()
	b[len(b)-1] |= 1
	if e := c.Decode(context.Background(), io.Discard, bytes.NewReader(b), make([]byte, 32)); e == nil {
		t.Fatal("nonzero fill accepted")
	}
	m.Width = 65536
	m.Height = 65536
	m.Bands = 65536
	var header bytes.Buffer
	if e := ccsds123.WriteHeader(&header, ccsds123.DefaultConfig(image(m))); e != nil {
		t.Fatal(e)
	}
	h := header.Bytes()
	if !bytes.Equal(h[1:7], make([]byte, 6)) {
		t.Fatal("dimension modulo encoding")
	}
}
