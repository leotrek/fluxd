// Package testutil provides small deterministic raw datasets for tests.
package testutil

import (
	"time"

	"fluxd/internal/metadata"
)

// Fixture returns a nonconstant 12-bit four-band optical acquisition.
func Fixture() (metadata.Record, []byte) {
	m := metadata.Record{SchemaVersion: 1, ID: "test-0001", SensorType: "optical", Timestamp: time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC), Width: 16, Height: 8, Bands: 4, SampleBitDepth: 12, StorageBitDepth: 16, SampleType: "uint", ByteOrder: "little", Layout: "BIL"}
	b := make([]byte, 1024)
	for i := 0; i < len(b)/2; i++ {
		v := uint16((i*17 + i/16*31) % 4096)
		b[2*i] = byte(v)
		b[2*i+1] = byte(v >> 8)
	}
	return m, b
}
