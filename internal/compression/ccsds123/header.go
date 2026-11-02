package ccsds123

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

// WriteHeader encodes tables 5-1/5-2/5-3; all reserved fields are zero.
func WriteHeader(w io.Writer, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	var h [HeaderBytes]byte
	i, p, e := c.Image, c.Predictor, c.Entropy
	h[0] = c.UserData
	binary.BigEndian.PutUint16(h[1:3], uint16(i.Width))
	binary.BigEndian.PutUint16(h[3:5], uint16(i.Height))
	binary.BigEndian.PutUint16(h[5:7], uint16(i.Bands))
	h[7] = byte(i.BitDepth%16) << 1
	if i.Signed {
		h[7] |= 128
	}
	if i.Layout == "BSQ" {
		h[7] |= 1
	} else {
		depth := int64(1)
		if i.Layout == "BIP" {
			depth = i.Bands
		}
		binary.BigEndian.PutUint16(h[8:10], uint16(depth))
	}
	h[10] = byte(c.OutputWordBytes%8) << 3
	h[12] = byte(p.PredictionBands)<<2 | 2
	h[13] = 128 | byte(p.RegisterSize%64)
	h[14] = byte(p.WeightResolution-4)<<4 | byte(bits.TrailingZeros(uint(p.UpdateInterval))-4)
	h[15] = byte(p.InitialExponent+6)<<4 | byte(p.FinalExponent+6)
	h[17] = byte(e.UnaryLimit%32)<<3 | byte(e.RescaleBits-4)
	h[18] = byte(e.InitialCountExponent%8)<<5 | byte(e.AccumulatorConstant)<<1
	n, err := w.Write(h[:])
	if err == nil && n != len(h) {
		err = io.ErrShortWrite
	}
	return err
}
func dimension(b []byte) int64 {
	n := int64(binary.BigEndian.Uint16(b))
	if n == 0 {
		return 65536
	}
	return n
}

// ReadHeader derives supported geometry, prediction and entropy parameters from the stream.
// It does not require acquisition metadata or the encoder's configuration.
func ReadHeader(r io.Reader) (Config, error) {
	var h [HeaderBytes]byte
	var c Config
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return c, fmt.Errorf("read CCSDS header: %w", e)
	}
	if h[7]&0x60 != 0 || h[10]&0xc3 != 0 || h[11] != 0 || h[12]&0xc1 != 0 || h[13]&0x40 != 0 || h[16]&0x80 != 0 {
		return c, fmt.Errorf("%w: nonzero reserved header bits", ErrMalformed)
	}
	if h[16]&0x7f != 0 || h[18]&1 != 0 || h[18]>>1&15 == 15 {
		return c, fmt.Errorf("%w: custom initialization or parameter tables", ErrUnsupported)
	}
	i := Image{Width: dimension(h[1:3]), Height: dimension(h[3:5]), Bands: dimension(h[5:7]), BitDepth: int(h[7] >> 1 & 15), Signed: h[7]&128 != 0}
	if i.BitDepth == 0 {
		i.BitDepth = 16
	}
	depth := int64(binary.BigEndian.Uint16(h[8:10]))
	if h[7]&1 != 0 {
		if depth != 0 {
			return c, fmt.Errorf("%w: BSQ interleaving depth must be zero", ErrMalformed)
		}
		i.Layout = "BSQ"
	} else {
		depth = dimension(h[8:10])
		switch {
		case depth == 1:
			i.Layout = "BIL"
		case depth == i.Bands:
			i.Layout = "BIP"
		case depth > i.Bands:
			return c, fmt.Errorf("%w: interleaving depth exceeds band count", ErrMalformed)
		default:
			return c, fmt.Errorf("%w: intermediate BI interleaving depth", ErrUnsupported)
		}
	}
	c = Config{Image: i, UserData: h[0], OutputWordBytes: int(h[10] >> 3 & 7), Predictor: PredictorConfig{PredictionBands: int(h[12] >> 2 & 15), Full: h[12]&2 == 0, Column: h[13]&128 != 0, RegisterSize: int(h[13] & 63), WeightResolution: int(h[14]>>4) + 4, UpdateInterval: 1 << uint((h[14]&15)+4), InitialExponent: int(h[15]>>4) - 6, FinalExponent: int(h[15]&15) - 6}, Entropy: EntropyConfig{BlockAdaptive: h[10]&4 != 0, UnaryLimit: int(h[17] >> 3), RescaleBits: int(h[17]&7) + 4, InitialCountExponent: int(h[18] >> 5), AccumulatorConstant: int(h[18] >> 1 & 15)}}
	if c.OutputWordBytes == 0 {
		c.OutputWordBytes = 8
	}
	if c.Predictor.RegisterSize == 0 {
		c.Predictor.RegisterSize = 64
	}
	if c.Entropy.UnaryLimit == 0 {
		c.Entropy.UnaryLimit = 32
	}
	if c.Entropy.InitialCountExponent == 0 {
		c.Entropy.InitialCountExponent = 8
	}
	if e := c.Validate(); e != nil {
		return c, e
	}
	return c, nil
}
