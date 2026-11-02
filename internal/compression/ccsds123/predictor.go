package ccsds123

import "encoding/binary"

// Reduced prediction with P=0 has an empty weight vector (sections 4.3, 4.6,
// 4.8). No weight initialization/update operation exists in this subset.
// Section 4.7 equation 32 simplifies to 2*previous+1 for column sums, since
// the empty inner product is zero and the validated R cannot overflow it.
type predictor struct {
	image          Image
	rows           []byte
	low, high, mid int32
}

func newPredictor(i Image, workspace []byte) predictor {
	bands := i.Bands
	if i.Layout == "BSQ" {
		bands = 1
	}
	rows := workspace[:int(2*i.Width*bands)]
	clear(rows)
	p := predictor{image: i, rows: rows, high: int32((1 << i.BitDepth) - 1), mid: int32(1 << (i.BitDepth - 1))}
	if i.Signed {
		p.low = -p.mid
		p.high = p.mid - 1
		p.mid = 0
	}
	return p
}
func (p *predictor) position(z, x int64) int {
	if p.image.Layout == "BSQ" {
		z = 0
	}
	return int((z*p.image.Width + x) * 2)
}
func (p *predictor) double(z, y, x int64) int32 {
	if y == 0 && x == 0 {
		return 2 * p.mid
	}
	previousX := x
	if y == 0 {
		previousX--
	}
	pos := p.position(z, previousX)
	previous := int32(binary.LittleEndian.Uint16(p.rows[pos : pos+2]))
	if p.image.Signed {
		previous = int32(int16(previous))
	}
	return 2*previous + 1
}
func (p *predictor) remember(z, x int64, value int32) {
	pos := p.position(z, x)
	binary.LittleEndian.PutUint16(p.rows[pos:pos+2], uint16(value))
}

// Section 4.9 equations 35-37 use parity of the double-resolution prediction.
func mapResidual(value, double, low, high int32) uint32 {
	predicted := double >> 1
	theta := min(predicted-low, high-predicted)
	difference := value - predicted
	magnitude := difference
	if magnitude < 0 {
		magnitude = -magnitude
	}
	if magnitude > theta {
		return uint32(magnitude + theta)
	}
	mapped := uint32(2 * magnitude)
	if (double&1 == 0 && difference < 0) || (double&1 != 0 && difference > 0) {
		mapped--
	}
	return mapped
}
func unmapResidual(mapped uint32, double, low, high int32) (int32, bool) {
	predicted := double >> 1
	theta := min(predicted-low, high-predicted)
	var difference int32
	if mapped > uint32(2*theta) {
		difference = int32(mapped) - theta
		if predicted-low >= high-predicted {
			difference = -difference
		}
	} else {
		difference = int32(mapped / 2)
		if mapped&1 != 0 {
			difference = -int32((mapped + 1) / 2)
		}
		if double&1 != 0 {
			difference = -difference
		}
	}
	value := predicted + difference
	return value, value >= low && value <= high
}
