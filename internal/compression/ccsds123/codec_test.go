package ccsds123

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"testing"
)

func smallConfig() Config {
	return DefaultConfig(Image{Width: 4, Height: 3, Bands: 2, BitDepth: 12, Layout: "BIL"})
}
func TestHeaderExpectedBytes(t *testing.T) {
	c := smallConfig()
	var out bytes.Buffer
	if e := WriteHeader(&out, c); e != nil {
		t.Fatal(e)
	}
	expected, _ := hex.DecodeString("0000040003000218000108000280a259009226")
	if !bytes.Equal(out.Bytes(), expected) {
		t.Fatalf("got %x want %x", out.Bytes(), expected)
	}
	got, e := ReadHeader(&out)
	if e != nil || got != c {
		t.Fatal(got, e)
	}
	for word := 1; word <= 8; word++ {
		c.OutputWordBytes = word
		c.UserData = 0xa5
		c.Image.Width = 65536
		c.Image.Height = 65536
		c.Image.Bands = 65536
		c.Image.Layout = "BIP"
		c.Entropy = EntropyConfig{UnaryLimit: 32, RescaleBits: 9, InitialCountExponent: 8, AccumulatorConstant: 10}
		out.Reset()
		if e = WriteHeader(&out, c); e != nil {
			t.Fatal(e)
		}
		got, e = ReadHeader(&out)
		if e != nil || got != c {
			t.Fatal(got, c, e)
		}
	}
}
func TestHeaderRejections(t *testing.T) {
	var base bytes.Buffer
	WriteHeader(&base, smallConfig())
	for _, tc := range []struct {
		index int
		mask  byte
		kind  error
	}{{7, 0x20, ErrMalformed}, {10, 0x80, ErrMalformed}, {11, 1, ErrMalformed}, {12, 0x80, ErrMalformed}, {13, 0x40, ErrMalformed}, {16, 0x80, ErrMalformed}, {12, 4, ErrUnsupported}, {12, 2, ErrUnsupported}, {13, 0x80, ErrUnsupported}, {10, 4, ErrUnsupported}, {16, 0x40, ErrUnsupported}, {18, 1, ErrUnsupported}} {
		b := append([]byte{}, base.Bytes()...)
		b[tc.index] ^= tc.mask
		if _, e := ReadHeader(bytes.NewReader(b)); !errors.Is(e, tc.kind) {
			t.Fatal(tc, e)
		}
	}
	for n := 0; n < HeaderBytes; n++ {
		if _, e := ReadHeader(bytes.NewReader(base.Bytes()[:n])); e == nil {
			t.Fatal("truncated header", n)
		}
	}
	b := append([]byte{}, base.Bytes()...)
	b[8] = 0
	b[9] = 3
	if _, e := ReadHeader(bytes.NewReader(b)); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
	b[9] = 2
	b[5] = 0
	b[6] = 3
	if _, e := ReadHeader(bytes.NewReader(b)); !errors.Is(e, ErrUnsupported) {
		t.Fatal(e)
	}
	b = append([]byte{}, base.Bytes()...)
	b[7] |= 1
	if _, e := ReadHeader(bytes.NewReader(b)); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
}
func TestConfigValidation(t *testing.T) {
	base := smallConfig()
	for _, change := range []func(*Config){func(c *Config) { c.Image.Width = 0 }, func(c *Config) { c.Image.Height = 65537 }, func(c *Config) { c.Image.BitDepth = 9 }, func(c *Config) { c.Image.Layout = "XYZ" }, func(c *Config) { c.Predictor.PredictionBands = 16 }, func(c *Config) { c.Predictor.PredictionBands = 1 }, func(c *Config) { c.Predictor.Full = true }, func(c *Config) { c.Predictor.Column = false }, func(c *Config) { c.Predictor.WeightResolution = 3 }, func(c *Config) { c.Predictor.RegisterSize = 31 }, func(c *Config) { c.Predictor.UpdateInterval = 17 }, func(c *Config) { c.Predictor.FinalExponent = -2 }, func(c *Config) { c.Entropy.BlockAdaptive = true }, func(c *Config) { c.Entropy.UnaryLimit = 7 }, func(c *Config) { c.Entropy.RescaleBits = 10 }, func(c *Config) { c.Entropy.InitialCountExponent = 0 }, func(c *Config) { c.Entropy.AccumulatorConstant = 15 }, func(c *Config) { c.OutputWordBytes = 0 }} {
		c := base
		change(&c)
		if e := c.Validate(); e == nil {
			t.Fatal(c)
		}
	}
}
func TestResidualExpectedAndReversibility(t *testing.T) {
	for _, tc := range []struct {
		value, double int32
		want          uint32
	}{{128, 256, 0}, {129, 256, 2}, {127, 256, 1}, {129, 257, 1}, {127, 257, 2}, {0, 256, 255}, {255, 256, 254}, {255, 511, 0}, {0, 1, 0}} {
		if got := mapResidual(tc.value, tc.double, 0, 255); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, signed := range []bool{false, true} {
		lo, hi := int32(0), int32(255)
		if signed {
			lo, hi = -128, 127
		}
		for predicted := lo; predicted <= hi; predicted++ {
			for parity := int32(0); parity < 2; parity++ {
				double := 2*predicted + parity
				for value := lo; value <= hi; value++ {
					mapped := mapResidual(value, double, lo, hi)
					got, ok := unmapResidual(mapped, double, lo, hi)
					if !ok || got != value || mapped > 255 {
						t.Fatal(value, double, mapped, got)
					}
				}
			}
		}
	}
	for _, value := range []int32{-32768, -1, 0, 32767} {
		mapped := mapResidual(value, 0, -32768, 32767)
		got, ok := unmapResidual(mapped, 0, -32768, 32767)
		if !ok || got != value {
			t.Fatal(value, got)
		}
	}
}
func TestPredictorBoundaries(t *testing.T) {
	for _, layout := range []string{"BSQ", "BIL", "BIP"} {
		i := Image{Width: 2, Height: 2, Bands: 2, BitDepth: 8, Layout: layout}
		p := newPredictor(i, make([]byte, 128))
		if p.double(0, 0, 0) != 256 {
			t.Fatal("midpoint")
		}
		p.remember(0, 0, 10)
		if p.double(0, 0, 1) != 21 {
			t.Fatal("west")
		}
		p.remember(0, 1, 20)
		if p.double(0, 1, 0) != 21 || p.double(0, 1, 1) != 41 {
			t.Fatal("north")
		}
		if p.double(1, 0, 0) != 256 {
			t.Fatal("band initialization")
		}
	}
	i := Image{Width: 1, Height: 2, Bands: 1, BitDepth: 16, Signed: true, Layout: "BSQ"}
	p := newPredictor(i, make([]byte, 10))
	if p.double(0, 0, 0) != 0 {
		t.Fatal("signed midpoint")
	}
	p.remember(0, 0, -32768)
	if p.double(0, 1, 0) != -65535 {
		t.Fatal("signed north")
	}
}
func rawDataset(c Config, pattern string) []byte {
	n, _ := c.Image.Samples()
	raw := make([]byte, n*2)
	rng := rand.New(rand.NewSource(7))
	for j := int64(0); j < n; j++ {
		value := int32((j * 17) % (1 << c.Image.BitDepth))
		switch pattern {
		case "constant":
			value = 0
		case "maximum":
			value = int32((1 << c.Image.BitDepth) - 1)
		case "noise":
			value = int32(rng.Intn(1 << c.Image.BitDepth))
		}
		if c.Image.Signed {
			value -= 1 << (c.Image.BitDepth - 1)
		}
		binary.LittleEndian.PutUint16(raw[j*2:j*2+2], uint16(value))
	}
	return raw
}
func TestRoundTripsAndHeaderDrivenDecode(t *testing.T) {
	for _, layout := range []string{"BSQ", "BIL", "BIP"} {
		for _, bands := range []int64{1, 4, 32} {
			for _, depth := range []int{8, 12, 16} {
				for _, signed := range []bool{false, true} {
					for _, pattern := range []string{"constant", "maximum", "gradient", "noise"} {
						c := DefaultConfig(Image{Width: 17, Height: 5, Bands: bands, BitDepth: depth, Signed: signed, Layout: layout})
						raw := rawDataset(c, pattern)
						f := RawFormat{StorageBits: 16, ByteOrder: "little"}
						workspace := make([]byte, 64<<10)
						var encoded, out bytes.Buffer
						if e := Encode(context.Background(), &encoded, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); e != nil {
							t.Fatal(e)
						}
						got, e := Decode(context.Background(), &out, &encoded, DecodeOptions{Format: f, Limits: DefaultLimits()}, workspace)
						if e != nil || !equivalentImage(got.Image, c.Image) || !bytes.Equal(out.Bytes(), raw) {
							t.Fatal(layout, bands, depth, signed, pattern, got, e)
						}
					}
				}
			}
		}
	}
}
func TestEntropyVariantsAndWordFill(t *testing.T) {
	for _, u := range []int{8, 18, 32} {
		for _, count := range []int{1, 3, 8} {
			for word := 1; word <= 8; word++ {
				c := smallConfig()
				c.Entropy = EntropyConfig{UnaryLimit: u, InitialCountExponent: count, RescaleBits: max(4, count+1), AccumulatorConstant: 0}
				c.OutputWordBytes = word
				c.UserData = 42
				raw := rawDataset(c, "noise")
				var encoded, out bytes.Buffer
				f := RawFormat{16, "little"}
				if e := Encode(context.Background(), &encoded, bytes.NewReader(raw), c, f, make([]byte, 1024), DefaultLimits()); e != nil {
					t.Fatal(e)
				}
				if encoded.Len()%word != 0 {
					t.Fatal("word padding")
				}
				got, e := Decode(context.Background(), &out, bytes.NewReader(encoded.Bytes()), DecodeOptions{Format: f, Limits: DefaultLimits()}, make([]byte, 1024))
				if e != nil || got != c || !bytes.Equal(out.Bytes(), raw) {
					t.Fatal(got, c, e)
				}
			}
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestDecodeErrorsAndLimits(t *testing.T) {
	c := smallConfig()
	raw := rawDataset(c, "gradient")
	f := RawFormat{16, "little"}
	var encoded bytes.Buffer
	workspace := make([]byte, 1024)
	Encode(context.Background(), &encoded, bytes.NewReader(raw), c, f, workspace, DefaultLimits())
	b := encoded.Bytes()
	for cut := 0; cut < len(b); cut++ {
		if _, e := Decode(context.Background(), io.Discard, bytes.NewReader(b[:cut]), DecodeOptions{Format: f, Limits: DefaultLimits()}, workspace); e == nil {
			t.Fatal("truncation accepted", cut)
		}
	}
	for _, limits := range []Limits{{MaxRawBytes: 1, MaxEncodedBytes: 4096, MaxWorkingBytes: 1024}, {MaxRawBytes: 4096, MaxEncodedBytes: 1, MaxWorkingBytes: 1024}, {MaxRawBytes: 4096, MaxEncodedBytes: 4096, MaxWorkingBytes: 1}, {MaxRawBytes: 4096, MaxEncodedBytes: HeaderBytes, MaxWorkingBytes: 1024}} {
		if _, e := Decode(context.Background(), io.Discard, bytes.NewReader(b), DecodeOptions{Format: f, Limits: limits}, workspace); !errors.Is(e, ErrLimit) {
			t.Fatal(e)
		}
	}
	expected := c.Image
	expected.Width++
	if _, e := Decode(context.Background(), io.Discard, bytes.NewReader(b), DecodeOptions{Format: f, Limits: DefaultLimits(), ExpectedImage: &expected}, workspace); !errors.Is(e, ErrMalformed) {
		t.Fatal(e)
	}
	if _, e := Decode(context.Background(), failedWriter{}, bytes.NewReader(b), DecodeOptions{Format: f, Limits: DefaultLimits()}, workspace); !errors.Is(e, io.ErrClosedPipe) {
		t.Fatal(e)
	}
	if e := Encode(context.Background(), failedWriter{}, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); !errors.Is(e, io.ErrClosedPipe) {
		t.Fatal(e)
	}
	if e := Encode(context.Background(), io.Discard, bytes.NewReader(raw[:1]), c, f, workspace, DefaultLimits()); e == nil {
		t.Fatal("raw truncation")
	}
	if e := Encode(context.Background(), io.Discard, bytes.NewReader(append(raw, 0)), c, f, workspace, DefaultLimits()); e == nil {
		t.Fatal("raw excess")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Encode(ctx, io.Discard, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := Decode(ctx, io.Discard, bytes.NewReader(b), DecodeOptions{Format: f, Limits: DefaultLimits()}, workspace); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestIndependentReferenceStreams(t *testing.T) {
	data, e := os.ReadFile("../testdata/ccsds-profile-vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Vectors []struct {
			Raw, Encoded, Order string
			Storage             int
		}
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, v := range fixture.Vectors {
		encoded, _ := hex.DecodeString(v.Encoded)
		raw, _ := hex.DecodeString(v.Raw)
		var out bytes.Buffer
		if _, e := Decode(context.Background(), &out, bytes.NewReader(encoded), DecodeOptions{Format: RawFormat{StorageBits: v.Storage, ByteOrder: v.Order}, Limits: DefaultLimits()}, make([]byte, 1024)); e != nil || !bytes.Equal(out.Bytes(), raw) {
			t.Fatal(e)
		}
	}
}

func TestAllocationsIndependentOfAcquisitionLength(t *testing.T) {
	f := RawFormat{16, "little"}
	var counts []float64
	for _, height := range []int64{1, 1024} {
		c := DefaultConfig(Image{Width: 32, Height: height, Bands: 1, BitDepth: 12, Layout: "BSQ"})
		raw := rawDataset(c, "gradient")
		workspace := make([]byte, 1024)
		allocs := testing.AllocsPerRun(3, func() {
			if e := Encode(context.Background(), io.Discard, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); e != nil {
				t.Fatal(e)
			}
		})
		counts = append(counts, allocs)
	}
	if counts[1] > counts[0]+2 {
		t.Fatalf("allocations scale with samples: short %v long %v", counts[0], counts[1])
	}
}
