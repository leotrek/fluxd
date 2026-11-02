// Package daemon coordinates startup, replay and graceful shutdown.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"fluxd/internal/acquisition"
	"fluxd/internal/compression"
	"fluxd/internal/config"
	"fluxd/internal/metadata"
	"fluxd/internal/storage"
	"fluxd/internal/transport"
)

// Run holds exclusive state ownership and serves until cancellation or an acquisition failure.
func Run(ctx context.Context, c config.Config, log *slog.Logger) error {
	return run(ctx, c, log, transport.Serve)
}
func run(ctx context.Context, c config.Config, log *slog.Logger, serve func(context.Context, string, *storage.Store, int, int, *slog.Logger) error) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if e := os.MkdirAll(c.StateDir, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(c.StateDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		return fmt.Errorf("state directory already in use: %w", e)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, e := storage.Open(c.StateDir, c.MaxStorageBytes)
	if e != nil {
		return fmt.Errorf("recover store: %w", e)
	}
	log.Info("storage recovered", "acquisitions", len(s.List()))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var replayErr error
	if c.ReplayFile != "" {
		f, e := os.Open(c.MetadataFile)
		if e != nil {
			return e
		}
		m, e := metadata.Parse(f)
		f.Close()
		if e != nil {
			return e
		}
		if _, e = s.Get(m.ID); e == nil {
			log.Info("replay already persisted", "id", m.ID)
		} else {
			source, e := acquisition.Open(c.ReplayFile, m, c.ReplayBytesPerSecond)
			if e != nil {
				return e
			}
			codec, e := compression.Select(c.Backend)
			if e != nil {
				source.Close()
				return e
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer source.Close()
				log.Info("acquisition started", "id", m.ID, "state", "ACQUIRING")
				log.Info("compression started", "id", m.ID, "codec", codec.ID(), "state", "COMPRESSING")
				replayErr = s.Put(ctx, m, acquisition.Reader{Ctx: ctx, Source: source}, codec, c.InputBufferBytes, c.MaxAcquisitionBytes)
				if replayErr != nil {
					log.Error("acquisition failed", "id", m.ID, "state", "FAILED", "error", replayErr)
					cancel()
				} else {
					log.Info("acquisition completed", "id", m.ID, "state", "READY")
				}
			}()
		}
	}
	log.Info("daemon starting", "socket", c.ListenSocket)
	e = serve(ctx, c.ListenSocket, s, c.MaxClients, c.InputBufferBytes, log)
	cancel()
	wg.Wait()
	log.Info("daemon stopped")
	if replayErr != nil {
		return replayErr
	}
	return e
}
