// Package storage atomically publishes immutable acquisitions.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"fluxd/internal/compression"
	"fluxd/internal/metadata"
)

const maxAcquisitions = 10000

// ErrNotFound indicates an unavailable acquisition.
var ErrNotFound = errors.New("acquisition not found")

// Store keeps a bounded metadata index. Writes are serialized; reads are independent.
type Store struct {
	root    string
	limit   int64
	mu      sync.RWMutex
	records map[string]metadata.Record
	used    int64
	writer  chan struct{}
}

func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Open recovers transactions and verifies all published payload checksums before exposing them.
func Open(root string, limit int64) (*Store, error) {
	if limit < 1 {
		return nil, fmt.Errorf("invalid storage limit")
	}
	for _, d := range []string{root, filepath.Join(root, "acquisitions"), filepath.Join(root, "tmp")} {
		if e := os.MkdirAll(d, 0700); e != nil {
			return nil, e
		}
	}
	s := &Store{root: root, limit: limit, records: make(map[string]metadata.Record), writer: make(chan struct{}, 1)}
	entries, e := os.ReadDir(filepath.Join(root, "tmp"))
	if e != nil {
		return nil, e
	}
	for _, v := range entries {
		if e = os.RemoveAll(filepath.Join(root, "tmp", v.Name())); e != nil {
			return nil, e
		}
	}
	entries, e = os.ReadDir(filepath.Join(root, "acquisitions"))
	if e != nil {
		return nil, e
	}
	for _, v := range entries {
		// Finder adds this bookkeeping file when users browse the store.
		if v.Name() == ".DS_Store" && v.Type().IsRegular() {
			continue
		}
		if !v.IsDir() || !metadata.ValidID(v.Name()) {
			return nil, fmt.Errorf("invalid stored entry %q", v.Name())
		}
		f, e := os.Open(filepath.Join(root, "acquisitions", v.Name(), "metadata.json"))
		if e != nil {
			return nil, e
		}
		m, e := metadata.Parse(f)
		f.Close()
		if e != nil {
			return nil, fmt.Errorf("recover metadata %s: %w", v.Name(), e)
		}
		n, e := m.Size()
		if e != nil || m.ID != v.Name() || m.Status != "READY" || m.UncompressedBytes != n || m.CompressedBytes < 0 || len(m.Checksum) != 64 || len(m.RawChecksum) != 64 || m.Compression.Mode != "lossless" {
			return nil, fmt.Errorf("inconsistent persisted metadata")
		}
		if _, e = compression.Select(m.Compression.Codec); e != nil {
			return nil, e
		}
		if m.Compression.Codec == "ccsds-123" && (m.Compression.Standard != "CCSDS 123.0-B-1" || m.Compression.Profile != compression.CCSDSProfile) {
			return nil, fmt.Errorf("unsupported persisted CCSDS standard or profile")
		}
		f, e = os.Open(filepath.Join(root, "acquisitions", m.ID, "payload"))
		if e != nil {
			return nil, e
		}
		h := sha256.New()
		size, e := io.Copy(h, f)
		f.Close()
		if e != nil || size != m.CompressedBytes || hex.EncodeToString(h.Sum(nil)) != m.Checksum {
			return nil, fmt.Errorf("integrity failure: %s", m.ID)
		}
		if len(s.records) >= maxAcquisitions {
			return nil, fmt.Errorf("metadata index capacity exceeded")
		}
		s.records[m.ID] = m
		s.used += size
	}
	return s, nil
}

// List returns a stable metadata snapshot.
func (s *Store) List() []metadata.Record {
	s.mu.RLock()
	out := make([]metadata.Record, 0, len(s.records))
	for _, m := range s.records {
		out = append(out, m)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns only a completed, verified record.
func (s *Store) Get(id string) (metadata.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.records[id]
	if !ok {
		return m, ErrNotFound
	}
	return m, nil
}

// Reader opens immutable payload data; closing it releases ownership.
func (s *Store) Reader(id string) (*os.File, metadata.Record, error) {
	m, e := s.Get(id)
	if e != nil {
		return nil, m, e
	}
	f, e := os.Open(filepath.Join(s.root, "acquisitions", m.ID, "payload"))
	return f, m, e
}

type limitedWriter struct {
	w         io.Writer
	remaining int64
	n         int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("storage capacity exceeded")
	}
	n, e := w.w.Write(p)
	w.n += int64(n)
	w.remaining -= int64(n)
	return n, e
}

// Put owns one buffer and a private transaction until atomic publication. Failed writes are removed.
func (s *Store) Put(ctx context.Context, m metadata.Record, r io.Reader, c compression.Backend, buffer int, max int64) error {
	if buffer < 1 {
		return fmt.Errorf("invalid buffer")
	}
	select {
	case s.writer <- struct{}{}:
		defer func() { <-s.writer }()
	case <-ctx.Done():
		return ctx.Err()
	}
	n, e := m.Size()
	if e != nil {
		return e
	}
	if n == 1<<63-1 {
		return fmt.Errorf("acquisition too large")
	}
	if n > max {
		return fmt.Errorf("acquisition capacity exceeded")
	}
	c, e = compression.Configure(c, m)
	if e != nil {
		return e
	}
	s.mu.RLock()
	_, exists := s.records[m.ID]
	used := s.used
	count := len(s.records)
	s.mu.RUnlock()
	if exists {
		return fmt.Errorf("acquisition already exists")
	}
	if count >= maxAcquisitions {
		return fmt.Errorf("metadata index capacity exceeded")
	}
	dir, e := os.MkdirTemp(filepath.Join(s.root, "tmp"), "txn-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	f, e := os.OpenFile(filepath.Join(dir, "payload"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	var rgb *os.File
	if m.Source != nil && m.Source.Kind == "video" && m.Source.RGBPath != "" {
		rgb, e = os.OpenFile(filepath.Join(dir, "frame.rgb"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer rgb.Close()
		r = io.TeeReader(r, rgb)
	}
	h := sha256.New()
	raw := sha256.New()
	w := &limitedWriter{w: io.MultiWriter(f, h), remaining: s.limit - used}
	lr := &io.LimitedReader{R: io.TeeReader(r, raw), N: n + 1}
	if e = c.Encode(ctx, w, lr, make([]byte, buffer)); e != nil {
		return fmt.Errorf("encode: %w", e)
	}
	if n+1-lr.N != n {
		return fmt.Errorf("input size changed or truncated")
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if rgb != nil {
		if e = rgb.Sync(); e != nil {
			return e
		}
		if e = rgb.Close(); e != nil {
			return e
		}
	}
	m.UncompressedBytes = n
	m.CompressedBytes = w.n
	m.Checksum = hex.EncodeToString(h.Sum(nil))
	m.RawChecksum = hex.EncodeToString(raw.Sum(nil))
	m.Compression = metadata.Compression{Codec: c.ID(), Mode: "lossless"}
	if c.ID() == "ccsds-123" {
		m.Compression.Standard = "CCSDS 123.0-B-1"
		m.Compression.Profile = compression.CCSDSProfile
	}
	m.Status = "READY"
	mf, e := os.OpenFile(filepath.Join(dir, "metadata.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = json.NewEncoder(mf).Encode(m)
	if e == nil {
		e = mf.Sync()
	}
	ce := mf.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = syncDir(dir); e != nil {
		return e
	}
	if e = os.Rename(dir, filepath.Join(s.root, "acquisitions", m.ID)); e != nil {
		return e
	}
	if e = syncDir(filepath.Join(s.root, "acquisitions")); e != nil {
		return e
	}
	s.mu.Lock()
	s.records[m.ID] = m
	s.used += w.n
	s.mu.Unlock()
	return nil
}
