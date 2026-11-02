package main

import (
	"fluxd/internal/metadata"
	"io"
	"testing"
)

func TestProcessorSplitSamples(t *testing.T) {
	for _, endian := range []string{"little", "big"} {
		p := processor{m: metadata.Record{StorageBitDepth: 16, SampleType: "int", ByteOrder: endian}, h: io.Discard, expected: 6}
		b := []byte{255, 255, 0, 128, 255, 127}
		if endian == "big" {
			b = []byte{255, 255, 128, 0, 127, 255}
		}
		for _, v := range b {
			if _, e := p.Write([]byte{v}); e != nil {
				t.Fatal(e)
			}
		}
		if p.samples != 3 || p.sum != -2 || p.min != -32768 || p.max != 32767 || p.odd {
			t.Fatalf("%+v", p)
		}
		if _, e := p.Write([]byte{0}); e == nil {
			t.Fatal("accepted excess")
		}
	}
}
