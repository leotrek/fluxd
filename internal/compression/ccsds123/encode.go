package ccsds123

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// Encode writes one complete B-1 stream incrementally. Workspace is exclusively
// owned for the call; its contents are overwritten. Input must contain exactly
// the declared sample count in the configured encoding order.
func Encode(ctx context.Context, w io.Writer, r io.Reader, c Config, f RawFormat, workspace []byte, limits Limits) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if e := limits.validate(c.Image, f, workspace); e != nil {
		return e
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	input := bufio.NewReaderSize(contextReader{ctx, r}, ioBufferBytes)
	output := bufio.NewWriterSize(&boundedWriter{w: w, remaining: limits.MaxEncodedBytes}, ioBufferBytes)
	if e := WriteHeader(output, c); e != nil {
		return e
	}
	bits := bitWriter{w: output}
	p := newPredictor(c.Image, workspace)
	sample := func(z, y, x int64) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		value, e := readRaw(input, f, c.Image.Signed)
		if e != nil {
			return fmt.Errorf("read raw sample: %w", e)
		}
		if value < p.low || value > p.high {
			return fmt.Errorf("%w: sample outside declared precision", ErrMalformed)
		}
		first := y == 0 && x == 0
		data := stateBytes(workspace, c.Image, z)
		s := loadState(data)
		if first {
			s = initialState(c.Entropy)
		}
		double := p.double(z, y, x)
		mapped := mapResidual(value, double, p.low, p.high)
		if e = encodeResidual(&bits, mapped, first, s, c); e != nil {
			return fmt.Errorf("write residual: %w", e)
		}
		p.remember(z, x, value)
		if !first {
			s.update(mapped, c.Entropy)
		}
		storeState(data, s)
		return nil
	}
	if e := walk(c.Image, sample); e != nil {
		return e
	}
	if e := requireEOF(input); e != nil {
		return e
	}
	return bits.finish(c.OutputWordBytes)
}
