// Package config loads strict, bounded JSON configuration.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Config contains startup-only settings; replay is optional.
type Config struct {
	ListenSocket         string `json:"listen_socket"`
	StateDir             string `json:"state_dir"`
	LogLevel             string `json:"log_level"`
	InputBufferBytes     int    `json:"input_buffer_bytes"`
	MaxMemoryBytes       int    `json:"max_memory_bytes"`
	MaxAcquisitionBytes  int64  `json:"max_acquisition_bytes"`
	MaxStorageBytes      int64  `json:"max_storage_bytes"`
	MaxClients           int    `json:"max_clients"`
	Backend              string `json:"backend"`
	ReplayFile           string `json:"replay_file"`
	MetadataFile         string `json:"metadata_file"`
	ReplayBytesPerSecond int64  `json:"replay_bytes_per_second"`
}

// Default returns unprivileged development settings.
func Default() Config {
	return Config{ListenSocket: "./fluxd.sock", StateDir: "./fluxd-state", LogLevel: "info", InputBufferBytes: 64 << 10, MaxMemoryBytes: 8 << 20, MaxAcquisitionBytes: 1 << 30, MaxStorageBytes: 4 << 30, MaxClients: 8, Backend: "ccsds-123"}
}

// Validate rejects unsupported options and bounds aggregate streaming buffers.
func (c Config) Validate() error {
	if c.ListenSocket == "" || c.StateDir == "" {
		return fmt.Errorf("listen_socket and state_dir must be nonempty")
	}
	if c.InputBufferBytes < 1024 || c.InputBufferBytes > 4<<20 {
		return fmt.Errorf("input_buffer_bytes must be 1024..4194304")
	}
	if c.MaxClients < 1 || c.MaxClients > 128 {
		return fmt.Errorf("max_clients must be 1..128")
	}
	required := int64(c.InputBufferBytes)*int64(c.MaxClients+1) + (1 << 20)
	if int64(c.MaxMemoryBytes) < required {
		return fmt.Errorf("max_memory_bytes must cover configured buffers and codec overhead: at least %d", required)
	}
	if c.MaxAcquisitionBytes < 1 || c.MaxStorageBytes < 1 {
		return fmt.Errorf("acquisition and storage byte limits must be positive")
	}
	if c.ReplayBytesPerSecond < 0 {
		return fmt.Errorf("replay_bytes_per_second must be nonnegative")
	}
	if c.Backend != "identity" && c.Backend != "deflate" && c.Backend != "ccsds-123" {
		return fmt.Errorf("unsupported backend %q", c.Backend)
	}
	if (c.ReplayFile == "") != (c.MetadataFile == "") {
		return fmt.Errorf("replay_file and metadata_file must be set together")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log_level")
	}
	return nil
}

// Load overlays defaults and rejects unknown fields and trailing JSON.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		f, e := os.Open(path)
		if e != nil {
			return c, e
		}
		defer f.Close()
		const maxBytes = 64 << 10
		limited := &io.LimitedReader{R: f, N: maxBytes + 1}
		d := json.NewDecoder(limited)
		d.DisallowUnknownFields()
		if e = d.Decode(&c); e != nil {
			return c, e
		}
		var x json.RawMessage
		if d.Decode(&x) != io.EOF {
			return c, fmt.Errorf("trailing configuration")
		}
		if limited.N == 0 {
			return c, fmt.Errorf("configuration exceeds %d bytes", maxBytes)
		}
	}
	return c, c.Validate()
}
