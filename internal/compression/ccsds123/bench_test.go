package ccsds123

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
)

func BenchmarkPredictor(b *testing.B) {
	c := DefaultConfig(Image{Width: 1024, Height: 128, Bands: 4, BitDepth: 16, Layout: "BIP"})
	workspace := make([]byte, 64<<10)
	p := newPredictor(c.Image, workspace)
	b.ReportAllocs()
	b.SetBytes(2)
	var sum int32
	b.ResetTimer()
	for j := 0; j < b.N; j++ {
		x := int64(j % 1024)
		double := p.double(0, 1, x)
		p.remember(0, x, int32(j&65535))
		sum += double
	}
	b.ReportMetric(1e9/(float64(b.Elapsed().Nanoseconds())/float64(b.N)), "samples/s")
	if sum == -1 {
		b.Fatal(sum)
	}
}
func BenchmarkEntropy(b *testing.B) {
	c := smallConfig()
	w := bitWriter{w: bufio.NewWriter(io.Discard)}
	s := initialState(c.Entropy)
	b.ReportAllocs()
	b.SetBytes(2)
	b.ResetTimer()
	for j := 0; j < b.N; j++ {
		v := uint32(j & 4095)
		if e := encodeResidual(&w, v, false, s, c); e != nil {
			b.Fatal(e)
		}
		s.update(v, c.Entropy)
	}
	if e := w.finish(1); e != nil {
		b.Fatal(e)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "samples/s")
}
func BenchmarkPipeline(b *testing.B) {
	for _, bands := range []int64{1, 16} {
		for _, depth := range []int{8, 12, 16} {
			for _, pattern := range []string{"gradient", "noise"} {
				c := DefaultConfig(Image{Width: 128, Height: 64, Bands: bands, BitDepth: depth, Layout: "BIP"})
				raw := rawDataset(c, pattern)
				workspace := make([]byte, 64<<10)
				f := RawFormat{16, "little"}
				var encoded bytes.Buffer
				if e := Encode(context.Background(), &encoded, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); e != nil {
					b.Fatal(e)
				}
				data := encoded.Bytes()
				label := fmt.Sprintf("bands%d/depth%d/%s", bands, depth, pattern)
				for _, decode := range []bool{false, true} {
					operation := "encode"
					if decode {
						operation = "decode"
					}
					b.Run(label+"/"+operation, func(b *testing.B) {
						b.SetBytes(int64(len(raw)))
						b.ReportAllocs()
						b.ResetTimer()
						for j := 0; j < b.N; j++ {
							if decode {
								if _, e := Decode(context.Background(), io.Discard, bytes.NewReader(data), DecodeOptions{Format: f, Limits: DefaultLimits()}, workspace); e != nil {
									b.Fatal(e)
								}
							} else if e := Encode(context.Background(), io.Discard, bytes.NewReader(raw), c, f, workspace, DefaultLimits()); e != nil {
								b.Fatal(e)
							}
						}
						b.ReportMetric(float64(len(raw))/float64(len(data)), "raw/encoded")
						samples, _ := c.Image.Samples()
						b.ReportMetric(float64(samples)*float64(b.N)/b.Elapsed().Seconds(), "samples/s")
					})
				}
			}
		}
	}
}
