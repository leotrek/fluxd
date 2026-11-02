// Command fluxbench measures a sustained, disk-backed pipeline without large RAM fixtures.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"fluxd/internal/acquisition"
	"fluxd/internal/compression"
	"fluxd/internal/metadata"
	"fluxd/internal/storage"
	"fluxd/internal/transport"
)

type report struct {
	Bands                  int64   `json:"bands"`
	Width                  int64   `json:"width"`
	Height                 int64   `json:"height"`
	Pattern                string  `json:"pattern"`
	Transport              string  `json:"transport"`
	Cache                  string  `json:"cache"`
	BufferBytes            int     `json:"buffer_bytes"`
	WallSeconds            float64 `json:"wall_seconds"`
	CPUPercent             float64 `json:"cpu_percent"`
	PipelineLatencySeconds float64 `json:"pipeline_latency_seconds"`
	LogicalReadBytes       int64   `json:"logical_payload_read_bytes"`
	LogicalWriteBytes      int64   `json:"logical_payload_write_bytes"`
	Verified               bool    `json:"verified"`

	Platform               string  `json:"platform"`
	Codec                  string  `json:"codec"`
	Bytes                  int64   `json:"bytes"`
	CompressedBytes        int64   `json:"compressed_bytes"`
	Ratio                  float64 `json:"ratio"`
	SourceSeconds          float64 `json:"source_seconds"`
	CompressionSeconds     float64 `json:"compression_seconds"`
	StoragePipelineSeconds float64 `json:"storage_pipeline_seconds"`
	TransportSeconds       float64 `json:"transport_seconds,omitempty"`
	DecompressionSeconds   float64 `json:"decompression_seconds"`
	SourceMBPS             float64 `json:"source_mb_s"`
	CompressionMBPS        float64 `json:"compression_mb_s"`
	TransportMBPS          float64 `json:"transport_mb_s,omitempty"`
	CPUSeconds             float64 `json:"cpu_seconds"`
	PeakRSSBytes           int64   `json:"peak_rss_bytes"`
	TotalAllocBytes        uint64  `json:"total_alloc_bytes"`
	Allocations            uint64  `json:"allocations"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	size := flag.Int64("bytes", 64<<20, "raw bytes (multiple of 2*bands)")
	bands := flag.Int64("bands", 4, "spectral bands")
	iterations := flag.Int("iterations", 3, "repeated acquisitions")
	codecName := flag.String("codec", "ccsds-123", "ccsds-123, identity or deflate")
	transportMode := flag.String("transport", "unix", "unix or file (file bypasses socket; no transport measurement)")
	bufferSize := flag.Int("buffer", 64<<10, "caller workspace and transfer buffer bytes")
	pattern := flag.String("pattern", "gradient", "gradient or noise")
	flag.Parse()
	if (*transportMode != "unix" && *transportMode != "file") || *bufferSize < 1 || *bufferSize > 64<<20 || *bands < 1 || *bands > 1024 || *size < 2*(*bands) || *size%(2*(*bands)) != 0 || *iterations < 1 || *iterations > 1000 || (*pattern != "gradient" && *pattern != "noise") {
		return fmt.Errorf("invalid benchmark parameters")
	}
	m := metadata.Record{SchemaVersion: 1, ID: "bench-0", SensorType: "hyperspectral", Timestamp: time.Now().UTC(), Width: *size / (2 * (*bands)), Height: 1, Bands: *bands, SampleBitDepth: 16, StorageBitDepth: 16, SampleType: "uint", ByteOrder: "little", Layout: "BIP"}
	if *codecName == "ccsds-123" {
		samples := m.Width
		width := min(samples, int64(4096))
		for width > 1 && samples%width != 0 {
			width--
		}
		m.Width = width
		m.Height = samples / width
	}
	codec, e := compression.Select(*codecName)
	if e == nil {
		codec, e = compression.Configure(codec, m)
	}
	if e != nil {
		return e
	}
	if *size > (1<<62)/int64(*iterations) {
		return fmt.Errorf("benchmark capacity overflow")
	}
	dir, e := os.MkdirTemp("", "fb-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "raw")
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	buf := make([]byte, *bufferSize)
	rawHash := sha256.New()
	var seed uint32 = 42
	for remaining := *size; remaining > 0; {
		n := int64(len(buf))
		if remaining < n {
			n = remaining
		}
		for i := int64(0); i < n; i++ {
			if *pattern == "noise" {
				seed = 1664525*seed + 1013904223
				buf[i] = byte(seed >> 24)
			} else {
				buf[i] = byte(((*size - remaining + i) / 2) % 4096 >> ((i % 2) * 8))
			}
		}
		rawHash.Write(buf[:n])
		if _, e = f.Write(buf[:n]); e != nil {
			f.Close()
			return e
		}
		remaining -= n
	}
	if e = f.Close(); e != nil {
		return e
	}
	s, e := storage.Open(filepath.Join(dir, "store"), (*size+(*size/10)+1024)*int64(*iterations))
	if e != nil {
		return e
	}
	expectedRawHash := hex.EncodeToString(rawHash.Sum(nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socket := filepath.Join(dir, "s")
	done := make(chan error, 1)
	if *transportMode == "unix" {
		go func() {
			done <- transport.Serve(ctx, socket, s, 4, len(buf), slog.New(slog.NewTextHandler(io.Discard, nil)))
		}()
		serveFinished := false
		defer func() {
			cancel()
			if !serveFinished {
				<-done
			}
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, e := transport.Dial(socket)
			if e == nil {
				c.Close()
				break
			}
			select {
			case err := <-done:
				serveFinished = true
				return fmt.Errorf("start socket server: %w", err)
			default:
			}
			if time.Now().After(deadline) {
				return e
			}
			runtime.Gosched()
		}
	}
	enc := json.NewEncoder(os.Stdout)
	for iteration := 0; iteration < *iterations; iteration++ {
		m.ID = fmt.Sprintf("bench-%d", iteration)
		iterationStart := time.Now()
		r := report{Bands: *bands, Width: m.Width, Height: m.Height, Pattern: *pattern, Transport: *transportMode, Cache: "warm/uncontrolled", BufferBytes: *bufferSize, Platform: runtime.GOOS + "/" + runtime.GOARCH, Codec: codec.ID(), Bytes: *size}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var ru0, ru1 syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &ru0)
		source, e := acquisition.Open(path, m, 0)
		if e != nil {
			return e
		}
		start := time.Now()
		_, e = compression.Copy(ctx, io.Discard, acquisition.Reader{Ctx: ctx, Source: source}, buf)
		source.Close()
		if e != nil {
			return e
		}
		r.SourceSeconds = time.Since(start).Seconds()
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		compressed, e := os.Create(filepath.Join(dir, "encoded"))
		if e != nil {
			in.Close()
			return e
		}
		start = time.Now()
		e = codec.Encode(ctx, compressed, in, buf)
		in.Close()
		if e != nil {
			compressed.Close()
			return e
		}
		if e = compressed.Sync(); e != nil {
			compressed.Close()
			return e
		}
		r.CompressionSeconds = time.Since(start).Seconds()
		compressed.Close()
		in, e = os.Open(path)
		if e != nil {
			return e
		}
		pipelineStart := time.Now()
		start = time.Now()
		e = s.Put(ctx, m, in, codec, len(buf), *size)
		in.Close()
		if e != nil {
			return e
		}
		r.StoragePipelineSeconds = time.Since(start).Seconds()
		stored, e := s.Get(m.ID)
		if e != nil {
			return e
		}
		r.CompressedBytes = stored.CompressedBytes
		r.Ratio = float64(*size) / float64(stored.CompressedBytes)
		if *transportMode == "unix" {
			conn, e := transport.Dial(socket)
			if e != nil {
				return e
			}
			start = time.Now()
			opened, e := transport.Call(conn, transport.Request{Op: "OpenAcquisition", ID: m.ID})
			if e != nil {
				conn.Close()
				return e
			}
			encodedHash := sha256.New()
			var transferred int64
			transferred, e = compression.Copy(ctx, encodedHash, io.LimitReader(conn, opened.Bytes), buf)
			if e == nil && (transferred != stored.CompressedBytes || hex.EncodeToString(encodedHash.Sum(nil)) != stored.Checksum) {
				e = fmt.Errorf("transfer integrity failure")
			}
			conn.Close()
			if e != nil {
				return e
			}
			r.TransportSeconds = time.Since(start).Seconds()
		}
		compressed, _, e = s.Reader(m.ID)
		if e != nil {
			return e
		}
		h := sha256.New()
		encodedHash := sha256.New()
		start = time.Now()
		counter := &countWriter{w: h}
		e = codec.Decode(ctx, counter, io.TeeReader(compressed, encodedHash), buf)
		compressed.Close()
		if e != nil {
			return e
		}
		if counter.n != *size || hex.EncodeToString(h.Sum(nil)) != expectedRawHash || stored.RawChecksum != expectedRawHash || hex.EncodeToString(encodedHash.Sum(nil)) != stored.Checksum {
			return fmt.Errorf("reconstruction integrity failure")
		}
		r.Verified = true
		r.PipelineLatencySeconds = time.Since(pipelineStart).Seconds()
		r.LogicalReadBytes = 3**size + stored.CompressedBytes
		if *transportMode == "unix" {
			r.LogicalReadBytes += stored.CompressedBytes
		}
		r.LogicalWriteBytes = 2 * stored.CompressedBytes
		r.WallSeconds = time.Since(iterationStart).Seconds()
		r.DecompressionSeconds = time.Since(start).Seconds()
		syscall.Getrusage(syscall.RUSAGE_SELF, &ru1)
		r.CPUSeconds = float64(ru1.Utime.Sec-ru0.Utime.Sec+ru1.Stime.Sec-ru0.Stime.Sec) + float64(ru1.Utime.Usec-ru0.Utime.Usec+ru1.Stime.Usec-ru0.Stime.Usec)/1e6
		r.CPUPercent = 100 * r.CPUSeconds / r.WallSeconds
		r.PeakRSSBytes = int64(ru1.Maxrss)
		if runtime.GOOS == "linux" {
			r.PeakRSSBytes *= 1024
		}
		runtime.ReadMemStats(&after)
		r.TotalAllocBytes = after.TotalAlloc - before.TotalAlloc
		r.Allocations = after.Mallocs - before.Mallocs
		r.SourceMBPS = float64(*size) / 1e6 / r.SourceSeconds
		r.CompressionMBPS = float64(*size) / 1e6 / r.CompressionSeconds
		if r.TransportSeconds > 0 {
			r.TransportMBPS = float64(stored.CompressedBytes) / 1e6 / r.TransportSeconds
		}
		if e = enc.Encode(r); e != nil {
			return e
		}
	}
	return nil
}

// countWriter verifies physical reconstructed length without retaining the image.
type countWriter struct {
	w io.Writer
	n int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, e := w.w.Write(p)
	w.n += int64(n)
	return n, e
}
