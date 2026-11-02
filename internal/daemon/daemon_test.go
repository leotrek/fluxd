package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"fluxd/internal/config"
	"fluxd/internal/storage"
	"fluxd/internal/testutil"
)

func TestLifecycle(t *testing.T) {
	root := t.TempDir()
	c := config.Default()
	c.StateDir = filepath.Join(root, "state")
	c.ListenSocket = filepath.Join(root, "s")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := Run(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil))); e != nil {
		if errors.Is(e, syscall.EPERM) {
			t.Skip("sandbox prohibits Unix socket bind: ", e)
		}
		t.Fatal(e)
	}
	f, e := os.OpenFile(filepath.Join(c.StateDir, ".lock"), os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if e = Run(ctx, c, slog.Default()); e == nil {
		t.Fatal("lock ignored")
	}
}

func TestReplayPipeline(t *testing.T) {
	root := t.TempDir()
	m, b := testutil.Fixture()
	c := config.Default()
	c.StateDir = filepath.Join(root, "state")
	c.ReplayFile = filepath.Join(root, "raw")
	c.MetadataFile = filepath.Join(root, "meta")
	os.WriteFile(c.ReplayFile, b, 0600)
	j, _ := json.Marshal(m)
	os.WriteFile(c.MetadataFile, j, 0600)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		serve := func(ctx context.Context, _ string, s *storage.Store, _ int, _ int, _ *slog.Logger) error {
			for {
				if len(s.List()) == 1 {
					cancel()
					return nil
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				runtime.Gosched()
			}
		}
		e := run(ctx, c, log, serve)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
	}
	os.WriteFile(c.ReplayFile, b[:10], 0600)
	m.ID = "another"
	j, _ = json.Marshal(m)
	os.WriteFile(c.MetadataFile, j, 0600)
	if e := run(context.Background(), c, log, func(context.Context, string, *storage.Store, int, int, *slog.Logger) error { return nil }); e == nil {
		t.Fatal("truncation accepted")
	}
}
func TestLock(t *testing.T) {
	c := config.Default()
	c.StateDir = t.TempDir()
	f, e := os.OpenFile(filepath.Join(c.StateDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if e = Run(context.Background(), c, slog.Default()); e == nil {
		t.Fatal("lock ignored")
	}
}

func TestCompressionFailure(t *testing.T) {
	root := t.TempDir()
	m, b := testutil.Fixture()
	c := config.Default()
	c.StateDir = filepath.Join(root, "state")
	c.MaxStorageBytes = 1
	c.ReplayFile = filepath.Join(root, "raw")
	c.MetadataFile = filepath.Join(root, "meta")
	os.WriteFile(c.ReplayFile, b, 0600)
	j, _ := json.Marshal(m)
	os.WriteFile(c.MetadataFile, j, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := run(ctx, c, slog.New(slog.NewTextHandler(io.Discard, nil)), func(ctx context.Context, _ string, s *storage.Store, _ int, _ int, _ *slog.Logger) error {
		<-ctx.Done()
		if len(s.List()) != 0 {
			t.Error("failed record published")
		}
		return nil
	})
	if e == nil {
		t.Fatal("failure hidden")
	}
	s, e := storage.Open(c.StateDir, 4096)
	if e != nil || len(s.List()) != 0 {
		t.Fatal(e)
	}
}
