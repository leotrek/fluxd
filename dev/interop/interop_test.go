//go:build interop

// Package interop runs an independently supplied complete-file codec as a dev subprocess.
package interop

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fluxd/internal/compression"
	"fluxd/internal/metadata"
)

// Commands are argv arrays, never shell strings. The adapter must select the exact
// reduced/P=0/column/sample-adaptive profile and honor the metadata raw format.
type runner struct {
	Decode []string `json:"decode"`
	Encode []string `json:"encode"`
}

func TestIndependentFiles(t *testing.T) {
	path := os.Getenv("FLUXD_INTEROP_CONFIG")
	if path == "" {
		t.Skip("FLUXD_INTEROP_CONFIG unset: no independent validation performed")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var r runner
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	if len(r.Decode) == 0 {
		t.Fatal("independent decoder argv required")
	}
	for _, tc := range []struct {
		name           string
		bands          int64
		depth          int
		signed         bool
		layout, endian string
	}{
		{"optical", 4, 12, false, "BIL", "little"},
		{"thermal", 1, 14, true, "BSQ", "big"},
		{"hyperspectral", 64, 16, false, "BIP", "little"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			m := metadata.Record{SchemaVersion: 1, ID: tc.name, SensorType: tc.name, Timestamp: time.Unix(0, 0).UTC(), Width: 32, Height: 16, Bands: tc.bands, SampleBitDepth: tc.depth, StorageBitDepth: 16, SampleType: "uint", Layout: tc.layout, ByteOrder: tc.endian}
			if tc.signed {
				m.SampleType = "int"
			}
			raw := make([]byte, 32*16*tc.bands*2)
			var order binary.ByteOrder = binary.LittleEndian
			if tc.endian == "big" {
				order = binary.BigEndian
			}
			for i := 0; i < len(raw)/2; i++ {
				v := (i*17 + i/32*31) % (1 << tc.depth)
				if i%19 == 0 {
					v = 0
				}
				if i%23 == 0 {
					v = (1 << tc.depth) - 1
				}
				if tc.signed {
					v -= 1 << (tc.depth - 1)
				}
				order.PutUint16(raw[2*i:], uint16(v))
			}
			meta, _ := json.Marshal(m)
			metaPath := filepath.Join(dir, "metadata.json")
			if e = os.WriteFile(metaPath, meta, 0600); e != nil {
				t.Fatal(e)
			}
			rawPath := filepath.Join(dir, "original.raw")
			if e = os.WriteFile(rawPath, raw, 0600); e != nil {
				t.Fatal(e)
			}
			c, _ := compression.Select("ccsds-123")
			c, e = compression.Configure(c, m)
			if e != nil {
				t.Fatal(e)
			}
			var encoded bytes.Buffer
			if e = c.Encode(context.Background(), &encoded, bytes.NewReader(raw), make([]byte, 1<<20)); e != nil {
				t.Fatal(e)
			}
			native := filepath.Join(dir, "native.ccsds")
			if e = os.WriteFile(native, encoded.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
			invoke := func(t *testing.T, argv []string, input, output string) {
				t.Helper()
				args := make([]string, len(argv))
				for i, a := range argv {
					args[i] = strings.NewReplacer("{input}", input, "{output}", output, "{metadata}", metaPath).Replace(a)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, args[0], args[1:]...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%q: %v\n%s", args, err, out)
				}
			}
			t.Run("native_to_independent", func(t *testing.T) {
				output := filepath.Join(dir, "independent.raw")
				invoke(t, r.Decode, native, output)
				got, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, got) {
					t.Fatal("reconstructed samples differ")
				}
			})
			t.Run("independent_to_native", func(t *testing.T) {
				if len(r.Encode) == 0 {
					t.Skip("independent encoder not configured")
				}
				output := filepath.Join(dir, "independent.ccsds")
				invoke(t, r.Encode, rawPath, output)
				data, err := os.ReadFile(output)
				if err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				if err = c.Decode(context.Background(), &got, bytes.NewReader(data), make([]byte, 1<<20)); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, got.Bytes()) {
					t.Fatal("reconstructed samples differ")
				}
			})
		})
	}
}
