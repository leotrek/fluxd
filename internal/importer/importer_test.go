package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fluxd/internal/compression"
	"fluxd/internal/storage"
)

func options(t *testing.T, kind, path string) Options {
	t.Helper()
	return Options{Kind: kind, Input: path, StateDir: filepath.Join(t.TempDir(), "state"), Backend: "ccsds-123", PixelFormat: "rgb8", MaxAcquisitionBytes: 1 << 20, MaxStorageBytes: 8 << 20}
}

func writePNG(t *testing.T, img image.Image) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func decoded(t *testing.T, s *storage.Store, id string) []byte {
	t.Helper()
	f, m, err := s.Reader(id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := compression.Select(m.Compression.Codec)
	if err != nil {
		t.Fatal(err)
	}
	c, err = compression.Configure(c, m)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := c.Decode(context.Background(), &out, f, make([]byte, 64<<10)); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestImageRoundTripAndDeduplication(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{10, 20, 30, 40})
	img.SetNRGBA(1, 0, color.NRGBA{50, 60, 70, 255})
	for _, tc := range []struct {
		format string
		want   []byte
	}{
		{"rgb8", []byte{10, 20, 30, 50, 60, 70}}, {"rgba8", []byte{10, 20, 30, 40, 50, 60, 70, 255}},
	} {
		t.Run(tc.format, func(t *testing.T) {
			o := options(t, "image", writePNG(t, img))
			o.PixelFormat = tc.format
			if err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			if err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			s, err := storage.Open(o.StateDir, o.MaxStorageBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.List()) != 1 {
				t.Fatal("duplicate import created another acquisition")
			}
			m := s.List()[0]
			if m.Source == nil || m.Source.PixelFormat != tc.format {
				t.Fatal("missing provenance")
			}
			if got := decoded(t, s, m.ID); !bytes.Equal(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			img.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 255})
			f, err := os.Create(o.Input)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, img)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			s, err = storage.Open(o.StateDir, o.MaxStorageBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.List()) != 2 {
				t.Fatal("modified input was skipped")
			}
			img.SetNRGBA(0, 0, color.NRGBA{10, 20, 30, 40})
		})
	}
}

func TestGray16PreservesPrecision(t *testing.T) {
	img := image.NewGray16(image.Rect(0, 0, 2, 1))
	img.SetGray16(0, 0, color.Gray16{Y: 0x1234})
	img.SetGray16(1, 0, color.Gray16{Y: 0xabcd})
	o := options(t, "image", writePNG(t, img))
	o.PixelFormat = "gray16"
	if err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(o.StateDir, o.MaxStorageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded(t, s, s.List()[0].ID); !bytes.Equal(got, []byte{0x34, 0x12, 0xcd, 0xab}) {
		t.Fatalf("16-bit precision lost: %x", got)
	}
}

func TestRawBatchAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "thermal.raw")
	m := descriptor("thermal-test", time.Now().UTC(), 2, 1, 1, 16)
	m.SensorType = "thermal"
	m.SampleBitDepth = 12
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thermal.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{1, 0, 2, 0}, 0600); err != nil {
		t.Fatal(err)
	}
	o := options(t, "raw", dir)
	if err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(o.StateDir, o.MaxStorageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded(t, s, m.ID); !bytes.Equal(got, []byte{1, 0, 2, 0}) {
		t.Fatal(got)
	}
	if err := os.WriteFile(path, []byte{1}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), o); err == nil {
		t.Fatal("accepted invalid raw size")
	}
	img := image.NewGray(image.Rect(0, 0, 20, 20))
	small := options(t, "image", writePNG(t, img))
	small.MaxAcquisitionBytes = 10
	if err := Run(context.Background(), small); err == nil {
		t.Fatal("accepted oversized image")
	}
}

func TestDirectorySelectionCancellationAndLock(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "x.png"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	o := options(t, "image", dir)
	if _, err := inputs(o); err == nil {
		t.Fatal("unexpected recursive traversal")
	}
	o.Recursive = true
	paths, err := inputs(o)
	if err != nil || len(paths) != 1 {
		t.Fatalf("%v %v", paths, err)
	}
	o.Input = writePNG(t, image.NewGray(image.Rect(0, 0, 1, 1)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, o); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(o.StateDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("lock not enforced: %v", err)
	}
}

func TestVideoFFmpegRoundTrip(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip("FFmpeg/FFprobe not installed")
		}
	}
	for _, tc := range []struct{ ext, codec string }{{"mkv", "ffv1"}, {"mp4", "libx264"}, {"h264", "libx264"}} {
		t.Run(tc.ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clip."+tc.ext)
			cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=16x16:r=2", "-frames:v", "3", "-c:v", tc.codec, path)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("video fixture: %v %s", err, out)
			}
			o := options(t, "video", path)
			o.MaxFrames = 2
			if err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			s, err := storage.Open(o.StateDir, o.MaxStorageBytes)
			if err != nil {
				t.Fatal(err)
			}
			records := s.List()
			if len(records) != 2 {
				t.Fatalf("frame limit: got %d", len(records))
			}
			for i, m := range records {
				if m.Source == nil || m.Source.FrameIndex == nil || *m.Source.FrameIndex != int64(i) {
					t.Fatal("frame provenance")
				}
				got := decoded(t, s, m.ID)
				rgb, err := os.ReadFile(m.Source.RGBPath)
				if err != nil || !bytes.Equal(rgb, got) {
					t.Fatalf("saved RGB differs from decoded payload: %v", err)
				}
				if m.Source.Path != path {
					t.Fatal("original video path missing")
				}
				if err := os.Remove(m.Source.RGBPath); err != nil {
					t.Fatal(err)
				}

				if len(got) != 16*16*3 || got[0] < 240 || got[1] > 10 || got[2] > 10 {
					t.Fatalf("unexpected RGB frame: %v", got[:3])
				}
			}
			o.MaxFrames = 0
			if err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			s, err = storage.Open(o.StateDir, o.MaxStorageBytes)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.List()) != 3 {
				t.Fatal("resume did not import remaining frame")
			}
			for _, m := range s.List() {
				rgb, err := os.ReadFile(m.Source.RGBPath)
				if err != nil || !bytes.Equal(rgb, decoded(t, s, m.ID)) {
					t.Fatalf("RGB backfill failed: %v", err)
				}
			}

		})
	}
}
