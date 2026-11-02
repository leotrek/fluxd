package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	if _, e := Load(""); e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{`{"listen_socket":""}`, `{"state_dir":""}`, `{"max_acquisition_bytes":0}`, `{"max_storage_bytes":0}`, `{"backend":"unknown"}`, `{"unknown":1}`, `{} {}`, `{"input_buffer_bytes":0}`, `{"max_clients":999}`, `{"replay_file":"x"}`, `{"log_level":"trace"}`, `{"max_memory_bytes":1}`, `{"replay_bytes_per_second":-1}`} {
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, []byte(s), 0600)
		if _, e := Load(p); e == nil {
			t.Fatal(s)
		}
	}
	if _, e := Load("/does/not/exist"); e == nil {
		t.Fatal("missing")
	}
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"backend":"identity"}`), 0600)
	c, e := Load(p)
	if e != nil || c.Backend != "identity" {
		t.Fatal(c, e)
	}
}

func TestConfigSizeLimit(t *testing.T) {
	for _, size := range []int{64 << 10, (64 << 10) + 1} {
		p := filepath.Join(t.TempDir(), "config.json")
		data := append([]byte("{}"), bytes.Repeat([]byte{' '}, size-2)...)
		if e := os.WriteFile(p, data, 0600); e != nil {
			t.Fatal(e)
		}
		_, e := Load(p)
		if (e != nil) != (size > 64<<10) {
			t.Fatalf("size %d: %v", size, e)
		}
	}
}
