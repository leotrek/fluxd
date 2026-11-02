package transport

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"fluxd/internal/compression"
	"fluxd/internal/metadata"
	"fluxd/internal/storage"
)

// Serve owns the listener until cancellation, caps clients and waits for connection cleanup.
func Serve(ctx context.Context, path string, s *storage.Store, clients, buffer int, log *slog.Logger) error {
	if clients < 1 || buffer < 1 {
		return fmt.Errorf("invalid server bounds")
	}
	if _, e := os.Lstat(path); e == nil {
		return fmt.Errorf("socket path already exists; remove stale socket after verifying no daemon is running")
	} else if !os.IsNotExist(e) {
		return e
	}
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return e
	}
	defer l.Close()
	if e = os.Chmod(path, 0600); e != nil {
		return e
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, clients)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	defer func() { cancel(); wg.Wait() }()
	for {
		conn, e := l.AcceptUnix()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			conn.Close()
			return nil
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			if e := handle(ctx, conn, s, buffer); e != nil && ctx.Err() == nil {
				log.Debug("consumer connection ended", "error", e)
			}
		}()
	}
}
func handle(ctx context.Context, c net.Conn, s *storage.Store, buffer int) error {
	opened := ""
	for {
		if e := c.SetDeadline(time.Now().Add(30 * time.Second)); e != nil {
			return e
		}
		var q Request
		if e := readFrame(c, &q, 16<<10); e != nil {
			return e
		}
		res := Response{}
		if q.ID != "" && !metadata.ValidID(q.ID) {
			res.Error = "invalid_id"
		} else {
			switch q.Op {
			case "GetStatus":
				res.Status = "ready"
			case "ListAcquisitions":
				if q.Offset < 0 || q.Limit < 0 || q.Limit > 256 {
					res.Error = "invalid_pagination"
					break
				}
				all := s.List()
				limit := q.Limit
				if limit == 0 {
					limit = 100
				}
				if q.Offset < len(all) {
					end := q.Offset + limit
					if end > len(all) {
						end = len(all)
					}
					res.Records = all[q.Offset:end]
				}
			case "GetAcquisition":
				m, e := s.Get(q.ID)
				if e != nil {
					res.Error = "not_found"
				} else {
					res.Record = &m
				}
			case "OpenAcquisition":
				if opened != "" {
					res.Error = "release_required"
					break
				}
				f, m, e := s.Reader(q.ID)
				if e != nil {
					res.Error = "not_found"
					break
				}
				res.Record = &m
				res.Bytes = m.CompressedBytes
				if e = WriteFrame(c, res); e != nil {
					f.Close()
					return e
				}
				_, e = compression.Copy(ctx, c, io.LimitReader(f, m.CompressedBytes), make([]byte, buffer))
				ce := f.Close()
				if e != nil {
					return e
				}
				if ce != nil {
					return ce
				}
				opened = q.ID
				continue
			case "ReleaseAcquisition":
				if opened == "" || opened != q.ID {
					res.Error = "not_open"
				} else {
					opened = ""
					res.Status = "released"
				}
			default:
				res.Error = "unknown_operation"
			}
		}
		if e := WriteFrame(c, res); e != nil {
			return e
		}
	}
}

// Dial connects to a local socket with a timeout.
func Dial(path string) (net.Conn, error) { return net.DialTimeout("unix", path, 5*time.Second) }

// Call performs a control exchange. OpenAcquisition leaves its binary body unread.
func Call(c net.Conn, q Request) (Response, error) {
	var r Response
	if e := c.SetDeadline(time.Now().Add(30 * time.Second)); e != nil {
		return r, e
	}
	if e := WriteFrame(c, q); e != nil {
		return r, e
	}
	if e := ReadFrame(c, &r); e != nil {
		return r, e
	}
	if r.Error != "" {
		return r, fmt.Errorf("API: %s", r.Error)
	}
	return r, nil
}
