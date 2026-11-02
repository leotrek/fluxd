package ccsds123

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
)

func FuzzHeader(f *testing.F) {
	var b bytes.Buffer
	WriteHeader(&b, smallConfig())
	f.Add(b.Bytes())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { ReadHeader(bytes.NewReader(b)) })
}
func FuzzDecode(f *testing.F) {
	c := smallConfig()
	var b bytes.Buffer
	Encode(context.Background(), &b, bytes.NewReader(rawDataset(c, "noise")), c, RawFormat{16, "little"}, make([]byte, 4096), DefaultLimits())
	f.Add(b.Bytes())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		Decode(context.Background(), io.Discard, bytes.NewReader(b), DecodeOptions{Format: RawFormat{16, "little"}, Limits: Limits{MaxRawBytes: 8192, MaxEncodedBytes: 16384, MaxWorkingBytes: 4096}}, make([]byte, 4096))
	})
}
func FuzzEntropyDecode(f *testing.F) {
	f.Add([]byte{0x80}, byte(8), byte(18))
	f.Fuzz(func(t *testing.T, b []byte, depth, limit byte) {
		c := smallConfig()
		c.Image.BitDepth = []int{8, 10, 12, 14, 16}[int(depth)%5]
		c.Entropy.UnaryLimit = 8 + int(limit)%25
		r := bitReader{r: bufio.NewReader(bytes.NewReader(b))}
		decodeResidual(&r, false, initialState(c.Entropy), c)
	})
}
func FuzzConfig(f *testing.F) {
	b, _ := json.Marshal(smallConfig())
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > 65536 {
			return
		}
		var c Config
		if json.Unmarshal(b, &c) == nil {
			c.Validate()
		}
	})
}
func FuzzSizes(f *testing.F) {
	f.Add(int64(4), int64(3), int64(2), 8)
	f.Fuzz(func(t *testing.T, w, h, b int64, d int) {
		i := Image{Width: w, Height: h, Bands: b, BitDepth: d, Layout: "BIP"}
		n, e := i.Samples()
		if e == nil && n <= 0 {
			t.Fatal("nonpositive size")
		}
		i.WorkingBytes()
	})
}
