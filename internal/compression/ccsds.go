package compression

import (
	"context"
	"io"

	"fluxd/internal/compression/ccsds123"
	"fluxd/internal/metadata"
)

// CCSDSProfile identifies the unchanged default acquisition encoding profile.
const CCSDSProfile = "reduced-p0-column-sample-adaptive-v1"

type ccsdsCodec struct{ meta metadata.Record }

func (c ccsdsCodec) ID() string { return "ccsds-123" }
func image(m metadata.Record) ccsds123.Image {
	return ccsds123.Image{Width: m.Width, Height: m.Height, Bands: m.Bands, BitDepth: m.SampleBitDepth, Signed: m.SampleType == "int", Layout: m.Layout}
}
func rawFormat(m metadata.Record) ccsds123.RawFormat {
	return ccsds123.RawFormat{StorageBits: m.StorageBitDepth, ByteOrder: m.ByteOrder}
}
func (c ccsdsCodec) Validate(m metadata.Record) error {
	if _, e := m.Size(); e != nil {
		return e
	}
	return ccsds123.DefaultConfig(image(m)).Validate()
}

// Configure validates and binds immutable acquisition metadata to a backend.
func Configure(c Backend, m metadata.Record) (Backend, error) {
	if e := c.Validate(m); e != nil {
		return nil, e
	}
	if _, ok := c.(ccsdsCodec); ok {
		return ccsdsCodec{meta: m}, nil
	}
	return c, nil
}
func (c ccsdsCodec) Encode(ctx context.Context, w io.Writer, r io.Reader, workspace []byte) error {
	if e := c.Validate(c.meta); e != nil {
		return e
	}
	return ccsds123.Encode(ctx, w, r, ccsds123.DefaultConfig(image(c.meta)), rawFormat(c.meta), workspace, backendLimits(c.meta, workspace))
}
func (c ccsdsCodec) Decode(ctx context.Context, w io.Writer, r io.Reader, workspace []byte) error {
	if e := c.Validate(c.meta); e != nil {
		return e
	}
	expected := image(c.meta)
	_, e := ccsds123.Decode(ctx, w, r, ccsds123.DecodeOptions{Format: rawFormat(c.meta), Limits: backendLimits(c.meta, workspace), ExpectedImage: &expected}, workspace)
	return e
}

// The caller validates metadata first. Bound raw output to this acquisition,
// and encoded input to the standard's worst case: Umax<=32 plus D<=16 bits
// per sample, a fixed header, and at most seven output-word fill bytes.
func backendLimits(m metadata.Record, workspace []byte) ccsds123.Limits {
	const maxCodewordBytes = 6
	const maxWordFillBytes = 7
	samples := m.Width * m.Height * m.Bands
	return ccsds123.Limits{MaxRawBytes: samples * int64(m.StorageBitDepth/8), MaxEncodedBytes: ccsds123.HeaderBytes + maxCodewordBytes*samples + maxWordFillBytes, MaxWorkingBytes: int64(len(workspace))}
}
