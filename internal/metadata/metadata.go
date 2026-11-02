// Package metadata defines acquisition descriptors and persisted records.
package metadata

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"
)

// Compression identifies the exact payload representation.
type Compression struct {
	Codec    string `json:"codec"`
	Mode     string `json:"mode"`
	Standard string `json:"standard,omitempty"`
	Profile  string `json:"profile,omitempty"`
}

// Record describes physical storage and effective sample precision.
type Record struct {
	SchemaVersion     int         `json:"schema_version"`
	ID                string      `json:"acquisition_id"`
	SensorType        string      `json:"sensor_type"`
	Timestamp         time.Time   `json:"timestamp"`
	Width             int64       `json:"width"`
	Height            int64       `json:"height"`
	Bands             int64       `json:"bands"`
	SampleBitDepth    int         `json:"sample_bit_depth"`
	StorageBitDepth   int         `json:"storage_bit_depth"`
	SampleType        string      `json:"sample_type"`
	ByteOrder         string      `json:"byte_order"`
	Layout            string      `json:"layout"`
	Compression       Compression `json:"compression"`
	UncompressedBytes int64       `json:"uncompressed_bytes"`
	CompressedBytes   int64       `json:"compressed_bytes"`
	Checksum          string      `json:"checksum"`
	RawChecksum       string      `json:"raw_checksum"`
	Status            string      `json:"status"`
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// ValidID accepts only safe single-component acquisition identifiers.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Size validates a descriptor and calculates its physical byte size without overflow.
func (m Record) Size() (int64, error) {
	if m.SchemaVersion != 1 || !ValidID(m.ID) || m.Timestamp.IsZero() || (m.SensorType != "optical" && m.SensorType != "thermal" && m.SensorType != "multispectral" && m.SensorType != "hyperspectral") {
		return 0, fmt.Errorf("invalid schema, acquisition ID, timestamp or sensor type")
	}
	if m.SampleBitDepth != 8 && m.SampleBitDepth != 10 && m.SampleBitDepth != 12 && m.SampleBitDepth != 14 && m.SampleBitDepth != 16 {
		return 0, fmt.Errorf("unsupported sample bit depth")
	}
	if (m.StorageBitDepth != 8 && m.StorageBitDepth != 16) || m.SampleBitDepth > m.StorageBitDepth {
		return 0, fmt.Errorf("invalid storage bit depth")
	}
	if m.SampleType != "uint" && m.SampleType != "int" {
		return 0, fmt.Errorf("invalid sample type")
	}
	if m.ByteOrder != "little" && m.ByteOrder != "big" {
		return 0, fmt.Errorf("invalid byte order")
	}
	if m.Layout != "BSQ" && m.Layout != "BIL" && m.Layout != "BIP" {
		return 0, fmt.Errorf("unsupported layout")
	}
	n := int64(m.StorageBitDepth / 8)
	for _, v := range []int64{m.Width, m.Height, m.Bands} {
		if v <= 0 || n > (1<<63-1)/v {
			return 0, fmt.Errorf("invalid or overflowing dimensions")
		}
		n *= v
	}
	return n, nil
}

// Parse strictly decodes one bounded JSON descriptor.
func Parse(r io.Reader) (Record, error) {
	var m Record
	const maxBytes = 64 << 10
	limited := &io.LimitedReader{R: r, N: maxBytes + 1}
	d := json.NewDecoder(limited)
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	var extra json.RawMessage
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("trailing metadata")
	}
	if limited.N == 0 {
		return m, fmt.Errorf("metadata exceeds %d bytes", maxBytes)
	}
	_, err := m.Size()
	return m, err
}
