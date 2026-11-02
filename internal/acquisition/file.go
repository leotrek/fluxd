// Package acquisition provides bounded raw file replay.
package acquisition

import (
	"context"
	"fmt"
	"os"
	"time"

	"fluxd/internal/metadata"
)

// FileReplaySource owns an exact-size regular file and optionally rate limits reads.
type FileReplaySource struct {
	f     *os.File
	meta  metadata.Record
	rate  int64
	start time.Time
	read  int64
}

// Open validates the descriptor and physical input size before reading any samples.
func Open(path string, m metadata.Record, rate int64) (*FileReplaySource, error) {
	n, e := m.Size()
	if e != nil {
		return nil, e
	}
	if rate < 0 {
		return nil, fmt.Errorf("negative replay rate")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, fmt.Errorf("open replay: %w", e)
	}
	s, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, fmt.Errorf("inspect replay: %w", e)
	}
	if !s.Mode().IsRegular() || s.Size() != n {
		f.Close()
		return nil, fmt.Errorf("replay must be a regular file of exactly %d bytes", n)
	}
	return &FileReplaySource{f: f, meta: m, rate: rate, start: time.Now()}, nil
}

// Metadata returns the validated source descriptor.
func (s *FileReplaySource) Metadata() metadata.Record { return s.meta }

// ReadContext reads into caller-owned bounded storage and supports rate-wait cancellation.
func (s *FileReplaySource) ReadContext(ctx context.Context, p []byte) (int, error) {
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if s.rate > 0 {
		seconds := float64(s.read) / float64(s.rate)
		delay := time.Duration(seconds*float64(time.Second)) - time.Since(s.start)
		if delay > 0 {
			t := time.NewTimer(delay)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-t.C:
			}
		}
	}
	n, e := s.f.Read(p)
	s.read += int64(n)
	return n, e
}

// Close releases the source file.
func (s *FileReplaySource) Close() error { return s.f.Close() }

// Reader adapts context-aware sources to standard streaming readers.
type Reader struct {
	Ctx    context.Context
	Source interface {
		ReadContext(context.Context, []byte) (int, error)
	}
}

// Read forwards one call; the adapter and context live only for the active transfer.
func (r Reader) Read(p []byte) (int, error) { return r.Source.ReadContext(r.Ctx, p) }
