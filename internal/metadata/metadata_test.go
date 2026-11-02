package metadata_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"fluxd/internal/metadata"
	"fluxd/internal/testutil"
)

func TestDescriptors(t *testing.T) {
	m, b := testutil.Fixture()
	n, e := m.Size()
	if e != nil || n != int64(len(b)) {
		t.Fatal(n, e)
	}
	for _, change := range []func(*metadata.Record){func(m *metadata.Record) { m.Width = 0 }, func(m *metadata.Record) { m.Height = -1 }, func(m *metadata.Record) { m.Bands = 1 << 62 }, func(m *metadata.Record) { m.SampleBitDepth = 9 }, func(m *metadata.Record) { m.StorageBitDepth = 8 }, func(m *metadata.Record) { m.Layout = "XYZ" }, func(m *metadata.Record) { m.SampleType = "float" }, func(m *metadata.Record) { m.ByteOrder = "native" }, func(m *metadata.Record) { m.ID = "../bad" }, func(m *metadata.Record) { m.SchemaVersion = 2 }} {
		x := m
		change(&x)
		if _, e = x.Size(); e == nil {
			t.Fatal(x)
		}
	}
	for _, layout := range []string{"BSQ", "BIL", "BIP"} {
		x := m
		x.Layout = layout
		if _, e = x.Size(); e != nil {
			t.Fatal(e)
		}
	}
	for _, depth := range []int{8, 10, 12, 14, 16} {
		x := m
		x.SampleBitDepth = depth
		if _, e = x.Size(); e != nil {
			t.Fatal(e)
		}
	}
	j, _ := json.Marshal(m)
	if _, e = metadata.Parse(bytes.NewReader(j)); e != nil {
		t.Fatal(e)
	}
	for _, b := range [][]byte{[]byte(`{}`), append(j, []byte(` {}`)...), []byte(`{"unknown":1}`)} {
		if _, e = metadata.Parse(bytes.NewReader(b)); e == nil {
			t.Fatal("accepted invalid metadata")
		}
	}
}
func FuzzMetadata(f *testing.F) {
	m, _ := testutil.Fixture()
	b, _ := json.Marshal(m)
	f.Add(b)
	f.Fuzz(func(t *testing.T, b []byte) { metadata.Parse(bytes.NewReader(b)) })
}
func FuzzIdentifier(f *testing.F) {
	f.Add("test-1")
	f.Add("../bad")
	f.Fuzz(func(t *testing.T, s string) { metadata.ValidID(s) })
}
func FuzzSize(f *testing.F) {
	f.Add(int64(16), int64(8), int64(4), 12)
	f.Fuzz(func(t *testing.T, w, h, b int64, d int) {
		m, _ := testutil.Fixture()
		m.Width = w
		m.Height = h
		m.Bands = b
		m.SampleBitDepth = d
		n, e := m.Size()
		if e == nil && n <= 0 {
			t.Fatal(n)
		}
	})
}

func TestMetadataSizeLimit(t *testing.T) {
	m, _ := testutil.Fixture()
	encoded, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	for _, size := range []int{64 << 10, (64 << 10) + 1} {
		data := append(append([]byte{}, encoded...), bytes.Repeat([]byte{' '}, size-len(encoded))...)
		_, e := metadata.Parse(bytes.NewReader(data))
		if (e != nil) != (size > 64<<10) {
			t.Fatalf("size %d: %v", size, e)
		}
	}
}
