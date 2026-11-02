// Package ccsds123 implements a bounded native CCSDS 123.0-B-1 lossless subset.
// Independent full-file interoperability remains a separate development validation step.
package ccsds123

import (
	"errors"
	"fmt"
)

// Standard identifies the stream revision.
const Standard = "CCSDS 123.0-B-1"

// HeaderBytes is the fixed header length for default initialization and no tables.
const HeaderBytes = 19

// Fixed fluxd profile choices within the ranges of sections 4.6-4.8 and 5.4.3.2.
const (
	defaultRegisterSize        = 64
	defaultWeightResolution    = 14
	defaultUpdateInterval      = 64
	defaultUnaryLimit          = 18
	defaultRescaleBits         = 6
	defaultCountExponent       = 1
	defaultAccumulatorConstant = 3
	entropyStateBytes          = 8 // two uint32 values: accumulator and counter
	sampleWordBytes            = 2 // previous samples are kept as uint16 containers
	ioBufferBytes              = 4096
)

// ErrUnsupported identifies a valid standard feature outside this implementation.
var ErrUnsupported = errors.New("unsupported CCSDS configuration")

// ErrMalformed identifies invalid stream syntax or configuration values.
var ErrMalformed = errors.New("malformed CCSDS stream or configuration")

// ErrLimit identifies a caller-specified resource ceiling.
var ErrLimit = errors.New("CCSDS resource limit exceeded")

// Image describes logical samples and their encoded order, not physical raw containers.
type Image struct {
	Width, Height, Bands int64
	BitDepth             int
	Signed               bool
	Layout               string
}

// PredictorConfig selects the section 4 predictor. Only reduced P=0 column sums are implemented.
type PredictorConfig struct {
	PredictionBands                int
	Full                           bool
	Column                         bool
	RegisterSize                   int
	WeightResolution               int
	UpdateInterval                 int
	InitialExponent, FinalExponent int
}

// EntropyConfig selects section 5.4.3.2 sample-adaptive code statistics.
type EntropyConfig struct {
	BlockAdaptive        bool
	UnaryLimit           int
	RescaleBits          int
	InitialCountExponent int
	AccumulatorConstant  int
}

// Config contains all implemented stream parameters. No compression state is shared.
type Config struct {
	Image           Image
	Predictor       PredictorConfig
	Entropy         EntropyConfig
	OutputWordBytes int
	UserData        byte
}

// RawFormat specifies container width and byte ordering absent from the CCSDS header.
// Samples are always read/written in the order specified by the stream's Image.Layout.
type RawFormat struct {
	StorageBits int
	ByteOrder   string
}

// Limits bounds reconstructed size, encoded size and workspace requirements before processing.
type Limits struct {
	MaxRawBytes     int64
	MaxEncodedBytes int64
	MaxWorkingBytes int64
}

// DefaultLimits returns device-conscious ceilings, not an allocation request.
func DefaultLimits() Limits {
	return Limits{MaxRawBytes: 1 << 30, MaxEncodedBytes: 4 << 30, MaxWorkingBytes: 4 << 20}
}

// DefaultConfig returns the existing fluxd reduced/P=0/column sample-adaptive profile.
func DefaultConfig(image Image) Config {
	return Config{Image: image, Predictor: PredictorConfig{Column: true, RegisterSize: defaultRegisterSize, WeightResolution: defaultWeightResolution, UpdateInterval: defaultUpdateInterval, InitialExponent: -1, FinalExponent: 3}, Entropy: EntropyConfig{UnaryLimit: defaultUnaryLimit, RescaleBits: defaultRescaleBits, InitialCountExponent: defaultCountExponent, AccumulatorConstant: defaultAccumulatorConstant}, OutputWordBytes: 1}
}

// Samples validates geometry/precision and calculates the logical sample count in int64.
func (i Image) Samples() (int64, error) {
	if i.Width < 1 || i.Width > 65536 || i.Height < 1 || i.Height > 65536 || i.Bands < 1 || i.Bands > 65536 {
		return 0, fmt.Errorf("%w: dimensions must be 1..65536", ErrMalformed)
	}
	switch i.BitDepth {
	case 8, 10, 12, 14, 16:
	default:
		return 0, fmt.Errorf("%w: supported precisions are 8/10/12/14/16", ErrUnsupported)
	}
	if i.Layout != "BSQ" && i.Layout != "BIL" && i.Layout != "BIP" {
		return 0, fmt.Errorf("%w: sample layout", ErrUnsupported)
	}
	return i.Width * i.Height * i.Bands, nil
}

// Validate rejects unsupported algorithms and invalid parameter ranges explicitly.
func (c Config) Validate() error {
	if _, e := c.Image.Samples(); e != nil {
		return e
	}
	p := c.Predictor
	if p.PredictionBands < 0 || p.PredictionBands > 15 {
		return fmt.Errorf("%w: prediction band count", ErrMalformed)
	}
	if p.PredictionBands != 0 || p.Full || !p.Column {
		return fmt.Errorf("%w: only reduced P=0 column prediction is implemented", ErrUnsupported)
	}
	if p.WeightResolution < 4 || p.WeightResolution > 19 || p.RegisterSize < max(32, c.Image.BitDepth+p.WeightResolution+2) || p.RegisterSize > 64 || p.UpdateInterval < 16 || p.UpdateInterval > 2048 || p.UpdateInterval&(p.UpdateInterval-1) != 0 || p.InitialExponent < -6 || p.FinalExponent > 9 || p.InitialExponent > p.FinalExponent {
		return fmt.Errorf("%w: predictor arithmetic parameters", ErrMalformed)
	}
	e := c.Entropy
	if e.BlockAdaptive {
		return fmt.Errorf("%w: block-adaptive entropy coding", ErrUnsupported)
	}
	if e.UnaryLimit < 8 || e.UnaryLimit > 32 || e.InitialCountExponent < 1 || e.InitialCountExponent > 8 || e.RescaleBits < max(4, e.InitialCountExponent+1) || e.RescaleBits > 9 || e.AccumulatorConstant < 0 || e.AccumulatorConstant > min(14, c.Image.BitDepth-2) {
		return fmt.Errorf("%w: sample-adaptive entropy parameters", ErrMalformed)
	}
	if c.OutputWordBytes < 1 || c.OutputWordBytes > 8 {
		return fmt.Errorf("%w: output word size", ErrMalformed)
	}
	return nil
}
func (f RawFormat) validate(i Image) error {
	if (f.StorageBits != 8 && f.StorageBits != 16) || f.StorageBits < i.BitDepth || (f.ByteOrder != "little" && f.ByteOrder != "big") {
		return fmt.Errorf("%w: raw sample containers", ErrMalformed)
	}
	return nil
}

// WorkingBytes returns the exact caller-owned row plus entropy-state workspace size.
func (i Image) WorkingBytes() (int64, error) {
	if _, e := i.Samples(); e != nil {
		return 0, e
	}
	bands := i.Bands
	if i.Layout == "BSQ" {
		bands = 1
	}
	return sampleWordBytes*i.Width*bands + entropyStateBytes*bands, nil
}
func (l Limits) validate(i Image, f RawFormat, workspace []byte) error {
	if l.MaxRawBytes < 1 || l.MaxEncodedBytes < HeaderBytes || l.MaxWorkingBytes < 1 {
		return fmt.Errorf("%w: invalid resource policy", ErrLimit)
	}
	n, e := i.Samples()
	if e != nil {
		return e
	}
	if e = f.validate(i); e != nil {
		return e
	}
	if n*int64(f.StorageBits/8) > l.MaxRawBytes {
		return fmt.Errorf("%w: raw bytes", ErrLimit)
	}
	needed, _ := i.WorkingBytes()
	if needed > l.MaxWorkingBytes || needed > int64(len(workspace)) {
		return fmt.Errorf("%w: need %d workspace bytes, supplied %d", ErrLimit, needed, len(workspace))
	}
	return nil
}
