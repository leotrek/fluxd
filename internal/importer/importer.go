// Package importer adapts media and raw files to the existing acquisition store.
package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"fluxd/internal/acquisition"
	"fluxd/internal/compression"
	"fluxd/internal/metadata"
	"fluxd/internal/storage"
)

// Options describes one offline batch. The state directory must not be in use.
type Options struct {
	Kind, Input, StateDir, Backend, PixelFormat string
	Recursive                                   bool
	MaxAcquisitionBytes, MaxStorageBytes        int64
	MaxFrames                                   int
	Output                                      io.Writer
}

// Run imports a file or a sorted directory. Completed acquisitions survive errors.
func Run(ctx context.Context, o Options) error {
	if o.Kind != "image" && o.Kind != "video" && o.Kind != "raw" {
		return fmt.Errorf("kind must be image, video or raw")
	}
	if o.Input == "" || o.StateDir == "" || o.MaxAcquisitionBytes < 1 || o.MaxStorageBytes < 1 || o.MaxFrames < 0 {
		return fmt.Errorf("input, state-dir and positive byte limits are required; max-frames must be nonnegative")
	}
	if o.PixelFormat != "rgb8" && o.PixelFormat != "rgba8" && o.PixelFormat != "gray8" && o.PixelFormat != "gray16" {
		return fmt.Errorf("pixel-format must be rgb8, rgba8, gray8 or gray16")
	}
	if o.Kind == "video" && o.PixelFormat != "rgb8" {
		return fmt.Errorf("video currently requires pixel-format rgb8")
	}
	codec, err := compression.Select(o.Backend)
	if err != nil {
		return err
	}
	files, err := inputs(o)
	if err != nil {
		return err
	}
	if o.Kind == "video" {
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if _, err := exec.LookPath(name); err != nil {
				return fmt.Errorf("video import requires %s on PATH: %w", name, err)
			}
		}
	}
	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(o.StateDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("state directory already in use: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, err := storage.Open(o.StateDir, o.MaxStorageBytes)
	if err != nil {
		return err
	}
	if o.Output == nil {
		o.Output = io.Discard
	}
	put := func(m metadata.Record, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if stored, err := s.Get(m.ID); err == nil {
			if m.Source != nil && m.Source.RGBPath != "" {
				if err := retainRGB(o.StateDir, stored, m.Source.RGBPath, r); err != nil {
					return err
				}
				stored.Source.RGBPath = m.Source.RGBPath
				return json.NewEncoder(o.Output).Encode(stored)
			}
			_, err = fmt.Fprintf(o.Output, "skipped %s (already stored)\n", m.ID)
			return err
		}
		if err := s.Put(ctx, m, r, codec, 64<<10, o.MaxAcquisitionBytes); err != nil {
			return err
		}
		stored, err := s.Get(m.ID)
		if err != nil {
			return err
		}
		return json.NewEncoder(o.Output).Encode(struct {
			ID         string           `json:"id"`
			Path       string           `json:"path"`
			Raw        int64            `json:"uncompressed_bytes"`
			Compressed int64            `json:"compressed_bytes"`
			Source     *metadata.Source `json:"source,omitempty"`
		}{m.ID, filepath.Join(o.StateDir, "acquisitions", m.ID), stored.UncompressedBytes, stored.CompressedBytes, stored.Source})
	}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		switch o.Kind {
		case "image":
			err = importImage(ctx, path, o, put)
		case "video":
			err = importVideo(ctx, path, o, put)
		case "raw":
			err = importRaw(ctx, path, put)
		}
		if err != nil {
			return fmt.Errorf("import %s: %w", path, err)
		}
	}
	return nil
}

func inputs(o Options) ([]string, error) {
	info, err := os.Stat(o.Input)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("input must be a regular file or directory")
		}
		return []string{o.Input}, nil
	}
	var paths []string
	err = filepath.WalkDir(o.Input, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != o.Input && !o.Recursive {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if o.Kind == "image" && (ext == ".png" || ext == ".jpg" || ext == ".jpeg") || o.Kind == "raw" && ext == ".raw" || o.Kind == "video" && (ext == ".h264" || ext == ".264" || ext == ".mp4" || ext == ".mkv" || ext == ".mov" || ext == ".avi" || ext == ".hevc" || ext == ".h265") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no supported %s files in %s", o.Kind, o.Input)
	}
	return paths, nil
}

// IDs include content and conversion settings, avoiding stale results after edits.
func sourceID(ctx context.Context, path, kind, format string) (string, time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", time.Time{}, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "fluximport-v1\x00%s\x00%s\x00", kind, format)
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", time.Time{}, err
		}
		n, err := f.Read(buffer)
		h.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", time.Time{}, err
		}
	}
	return kind + "-" + hex.EncodeToString(h.Sum(nil)), info.ModTime().UTC(), nil
}

func descriptor(id string, timestamp time.Time, width, height, bands int64, bits int) metadata.Record {
	return metadata.Record{SchemaVersion: 1, ID: id, SensorType: "optical", Timestamp: timestamp, Width: width, Height: height, Bands: bands, SampleBitDepth: bits, StorageBitDepth: bits, SampleType: "uint", ByteOrder: "little", Layout: "BIP"}
}

func importRaw(ctx context.Context, path string, put func(metadata.Record, io.Reader) error) error {
	f, err := os.Open(strings.TrimSuffix(path, filepath.Ext(path)) + ".json")
	if err != nil {
		return err
	}
	m, err := metadata.Parse(f)
	f.Close()
	if err != nil {
		return err
	}
	source, err := acquisition.Open(path, m, 0)
	if err != nil {
		return err
	}
	defer source.Close()
	return put(m, acquisition.Reader{Ctx: ctx, Source: source})
}

func importImage(ctx context.Context, path string, o Options, put func(metadata.Record, io.Reader) error) error {
	id, timestamp, err := sourceID(ctx, path, "image", o.PixelFormat)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	bands, bits := int64(3), 8
	switch o.PixelFormat {
	case "rgba8":
		bands = 4
	case "gray8":
		bands = 1
	case "gray16":
		bands, bits = 1, 16
	}
	m := descriptor(id, timestamp, int64(cfg.Width), int64(cfg.Height), bands, bits)
	m.Source = &metadata.Source{Kind: "image", Path: path, PixelFormat: o.PixelFormat}
	n, err := m.Size()
	if err != nil {
		return err
	}
	if n > o.MaxAcquisitionBytes {
		return fmt.Errorf("decoded image exceeds max-acquisition-bytes")
	}
	if n > int64(int(^uint(0)>>1)) {
		return fmt.Errorf("decoded image exceeds platform allocation limit")
	}
	// PNG decoding may allocate a 16-bit RGBA image in addition to the output.
	if m.Width > o.MaxAcquisitionBytes/8/m.Height {
		return fmt.Errorf("image decoder workspace exceeds max-acquisition-bytes; increase the limit")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	if img.Bounds().Dx() != cfg.Width || img.Bounds().Dy() != cfg.Height {
		return fmt.Errorf("image dimensions changed during decode")
	}
	data := make([]byte, int(n))
	offset := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			c := img.At(x, y)
			switch o.PixelFormat {
			case "gray8":
				data[offset] = color.GrayModel.Convert(c).(color.Gray).Y
				offset++
			case "gray16":
				binary.LittleEndian.PutUint16(data[offset:], color.Gray16Model.Convert(c).(color.Gray16).Y)
				offset += 2
			default:
				v := color.NRGBAModel.Convert(c).(color.NRGBA)
				data[offset], data[offset+1], data[offset+2] = v.R, v.G, v.B
				offset += 3
				if bands == 4 {
					data[offset] = v.A
					offset++
				}
			}
		}
	}
	return put(m, bytes.NewReader(data))
}

func importVideo(ctx context.Context, path string, o Options, put func(metadata.Record, io.Reader) error) error {
	id, timestamp, err := sourceID(ctx, path, "video", o.PixelFormat)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	probe := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", abs)
	var probeOut bytes.Buffer
	probe.Stdout = &limitedLog{buffer: &probeOut}
	probe.Stderr = io.Discard
	if err := probe.Run(); err != nil {
		return fmt.Errorf("ffprobe failed: %w", err)
	}
	var result struct {
		Streams []struct{ Width, Height int64 } `json:"streams"`
	}
	if err := json.Unmarshal(probeOut.Bytes(), &result); err != nil {
		return err
	}
	if len(result.Streams) != 1 {
		return fmt.Errorf("video has no usable first video stream")
	}
	m := descriptor(id, timestamp, result.Streams[0].Width, result.Streams[0].Height, 3, 8)
	n, err := m.Size()
	if err != nil {
		return err
	}
	if n > o.MaxAcquisitionBytes {
		return fmt.Errorf("decoded frame exceeds max-acquisition-bytes")
	}
	if n > int64(int(^uint(0)>>1)) {
		return fmt.Errorf("decoded frame exceeds platform allocation limit")
	}
	args := []string{"-nostdin", "-v", "error", "-noautorotate", "-i", abs, "-map", "0:v:0", "-an", "-sn", "-dn", "-fps_mode", "passthrough"}
	if o.MaxFrames > 0 {
		args = append(args, "-frames:v", fmt.Sprint(o.MaxFrames))
	}
	args = append(args, "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &limitedLog{buffer: &stderr}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	finished := false
	defer func() {
		if !finished {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	frame := make([]byte, int(n))
	count := 0
	for {
		_, err := io.ReadFull(stdout, frame)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("incomplete decoded frame: %w", err)
		}
		m.ID = fmt.Sprintf("%s-frame-%09d", id, count)
		index := int64(count)
		rgbPath, err := filepath.Abs(filepath.Join(o.StateDir, "acquisitions", m.ID, "frame.rgb"))
		if err != nil {
			return err
		}
		m.Source = &metadata.Source{Kind: "video", Path: abs, PixelFormat: "rgb8", FrameIndex: &index, RGBPath: rgbPath}
		if err := put(m, bytes.NewReader(frame)); err != nil {
			return err
		}
		count++
	}
	err = cmd.Wait()
	finished = true
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w: %s", err, stderr.String())
	}
	if count == 0 {
		return fmt.Errorf("video produced no frames")
	}
	return nil
}

// Drain subprocess logs while retaining only a bounded diagnostic prefix.
type limitedLog struct{ buffer *bytes.Buffer }

func (w *limitedLog) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 64*1024 - w.buffer.Len()
	if remaining > 0 {
		if remaining < len(p) {
			p = p[:remaining]
		}
		w.buffer.Write(p)
	}
	return n, nil
}

// retainRGB backfills examination files for previously imported video frames.
func retainRGB(root string, stored metadata.Record, rgbPath string, r io.Reader) error {
	dir := filepath.Join(root, "acquisitions", stored.ID)
	f, err := os.CreateTemp(dir, ".rgb-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, stored.UncompressedBytes+1))
	if err != nil {
		return err
	}
	if n != stored.UncompressedBytes || hex.EncodeToString(h.Sum(nil)) != stored.RawChecksum {
		return fmt.Errorf("RGB frame differs from stored samples")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, "frame.rgb")); err != nil {
		return err
	}
	stored.Source.RGBPath = rgbPath
	mf, err := os.CreateTemp(dir, ".metadata-")
	if err != nil {
		return err
	}
	defer os.Remove(mf.Name())
	defer mf.Close()
	if err := json.NewEncoder(mf).Encode(stored); err != nil {
		return err
	}
	if err := mf.Sync(); err != nil {
		return err
	}
	if err := mf.Close(); err != nil {
		return err
	}
	return os.Rename(mf.Name(), filepath.Join(dir, "metadata.json"))
}
