package ccsds123

import (
	"encoding/binary"
	"fmt"
)

const (
	// Section 5.4.3.2.2 equation 41 and 5.4.3.2.3 equation 44 bias constants.
	accumulatorBias      = 49
	accumulatorScaleBits = 7
)

type entropyState struct{ acc, count uint32 }

func initialState(e EntropyConfig) entropyState {
	count := uint32(1) << uint(e.InitialCountExponent)
	acc := ((3*(uint32(1)<<uint(e.AccumulatorConstant+6)) - accumulatorBias) * count) >> accumulatorScaleBits
	return entropyState{acc: acc, count: count}
}
func (s entropyState) parameter(depth int) uint {
	threshold := s.acc + (accumulatorBias * s.count >> accumulatorScaleBits)
	var k uint
	for k < uint(depth-2) && s.count<<(k+1) <= threshold {
		k++
	}
	return k
}
func (s *entropyState) update(mapped uint32, e EntropyConfig) {
	full := uint32(1)<<uint(e.RescaleBits) - 1
	if s.count < full {
		s.acc += mapped
		s.count++
	} else {
		s.acc = (s.acc + mapped + 1) >> 1
		s.count = (s.count + 1) >> 1
	}
}
func loadState(data []byte) entropyState {
	return entropyState{acc: binary.LittleEndian.Uint32(data[:4]), count: binary.LittleEndian.Uint32(data[4:8])}
}
func storeState(data []byte, s entropyState) {
	binary.LittleEndian.PutUint32(data[:4], s.acc)
	binary.LittleEndian.PutUint32(data[4:8], s.count)
}
func encodeResidual(w *bitWriter, mapped uint32, first bool, s entropyState, c Config) error {
	depth := uint(c.Image.BitDepth)
	if first {
		w.put(mapped, depth)
		return w.err
	}
	k := s.parameter(int(depth))
	q := mapped >> k
	limit := uint32(c.Entropy.UnaryLimit)
	if q < limit {
		w.put(0, uint(q))
		w.put(1, 1)
		w.put(mapped, k)
	} else {
		w.put(0, uint(limit))
		w.put(mapped, depth)
	}
	return w.err
}
func decodeResidual(r *bitReader, first bool, s entropyState, c Config) (uint32, error) {
	depth := uint(c.Image.BitDepth)
	if first {
		value := r.get(depth)
		return value, r.err
	}
	k := s.parameter(int(depth))
	var q uint32
	limit := uint32(c.Entropy.UnaryLimit)
	for q < limit {
		bit := r.get(1)
		if r.err != nil {
			return 0, fmt.Errorf("read unary residual: %w", r.err)
		}
		if bit != 0 {
			break
		}
		q++
	}
	var value uint32
	if q == limit {
		value = r.get(depth)
	} else {
		value = q<<k | r.get(k)
	}
	if r.err != nil {
		return 0, fmt.Errorf("read residual suffix: %w", r.err)
	}
	if value > uint32((1<<depth)-1) {
		return 0, fmt.Errorf("%w: residual exceeds sample precision", ErrMalformed)
	}
	return value, nil
}
