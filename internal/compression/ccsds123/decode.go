package ccsds123

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// DecodeOptions contains external raw-format and resource policy, not encoder state.
// ExpectedImage optionally asserts persisted metadata before any decoded output.
type DecodeOptions struct {
	Format        RawFormat
	Limits        Limits
	ExpectedImage *Image
}

func equivalentImage(a, b Image) bool {
	if a.Width != b.Width || a.Height != b.Height || a.Bands != b.Bands || a.BitDepth != b.BitDepth || a.Signed != b.Signed {
		return false
	}
	return a.Layout == b.Layout || (a.Bands == 1 && a.Layout != "BSQ" && b.Layout != "BSQ")
}

// Decode parses the stream header and reconstructs the encoded sample order.
// It requires no encoder configuration. Only raw container width/endian and
// resource limits are supplied because the standard does not encode those.
func Decode(ctx context.Context, w io.Writer, r io.Reader, options DecodeOptions, workspace []byte) (Config, error) {
	var zero Config
	if options.Limits.MaxEncodedBytes < HeaderBytes {
		return zero, fmt.Errorf("%w: encoded byte limit", ErrLimit)
	}
	input := bufio.NewReaderSize(&boundedReader{ctx: ctx, r: r, remaining: options.Limits.MaxEncodedBytes}, ioBufferBytes)
	c, e := ReadHeader(input)
	if e != nil {
		return zero, e
	}
	if e = options.Limits.validate(c.Image, options.Format, workspace); e != nil {
		return c, e
	}
	if options.ExpectedImage != nil && !equivalentImage(c.Image, *options.ExpectedImage) {
		return c, fmt.Errorf("%w: header does not match expected acquisition descriptor", ErrMalformed)
	}
	output := bufio.NewWriterSize(w, ioBufferBytes)
	bits := bitReader{r: input}
	p := newPredictor(c.Image, workspace)
	sample := func(z, y, x int64) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		first := y == 0 && x == 0
		data := stateBytes(workspace, c.Image, z)
		state := loadState(data)
		if first {
			state = initialState(c.Entropy)
		}
		mapped, e := decodeResidual(&bits, first, state, c)
		if e != nil {
			return fmt.Errorf("decode residual: %w", e)
		}
		value, valid := unmapResidual(mapped, p.double(z, y, x), p.low, p.high)
		if !valid {
			return fmt.Errorf("%w: reconstructed sample outside range", ErrMalformed)
		}
		if e = writeRaw(output, options.Format, value); e != nil {
			return fmt.Errorf("write reconstructed sample: %w", e)
		}
		p.remember(z, x, value)
		if !first {
			state.update(mapped, c.Entropy)
		}
		storeState(data, state)
		return nil
	}
	if e = walk(c.Image, sample); e != nil {
		return c, e
	}
	if e = bits.finish(c.OutputWordBytes); e != nil {
		return c, e
	}
	if e = output.Flush(); e != nil {
		return c, e
	}
	return c, nil
}
