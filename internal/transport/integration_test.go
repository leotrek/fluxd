package transport_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"fluxd/internal/acquisition"
	"fluxd/internal/compression"
	"fluxd/internal/storage"
	"fluxd/internal/testutil"
	"fluxd/internal/transport"
)

func server(t *testing.T, s *storage.Store) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	root, e := os.MkdirTemp("", "fd-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "s")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- transport.Serve(ctx, socket, s, 8, 1024, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
			cancel()
			if errors.Is(err, syscall.EPERM) {
				t.Skip("sandbox prohibits Unix socket bind: ", err)
			}
			t.Fatal(err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("server did not start", e)
		}
		runtime.Gosched()
	}
	t.Cleanup(cancel)
	return socket, cancel, done
}
func TestEndToEnd(t *testing.T) {
	root := t.TempDir()
	s, e := storage.Open(root, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	m, b := testutil.Fixture()
	c, _ := compression.Select("ccsds-123")
	rawPath := filepath.Join(t.TempDir(), "raw")
	if e = os.WriteFile(rawPath, b, 0600); e != nil {
		t.Fatal(e)
	}
	source, e := acquisition.Open(rawPath, m, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	if e = s.Put(context.Background(), m, acquisition.Reader{Ctx: context.Background(), Source: source}, c, 4096, 2048); e != nil {
		t.Fatal(e)
	}
	for restart := 0; restart < 2; restart++ {
		socket, cancel, done := server(t, s)
		info, e := os.Stat(socket)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, e)
		}
		conn, e := transport.Dial(socket)
		if e != nil {
			t.Fatal(e)
		}
		for _, q := range []transport.Request{{Op: "GetStatus"}, {Op: "ListAcquisitions"}, {Op: "GetAcquisition", ID: m.ID}} {
			if _, e = transport.Call(conn, q); e != nil {
				t.Fatal(e)
			}
		}
		for _, q := range []transport.Request{{Op: "Unknown"}, {Op: "GetAcquisition", ID: "missing"}, {Op: "GetAcquisition", ID: "../bad"}, {Op: "ReleaseAcquisition", ID: m.ID}, {Op: "ListAcquisitions", Limit: 999}} {
			if _, e = transport.Call(conn, q); e == nil {
				t.Fatal(q)
			}
		}
		conn.Close()
		var wg sync.WaitGroup
		for i := 0; i < 6; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, e := consumeAcquisition(context.Background(), socket, m.ID)
				if e != nil {
					t.Error(e)
					return
				}
				if r.Samples != 512 || r.RawChecksum == "" {
					t.Error(r)
				}
			}()
		}
		wg.Wait()
		conn, _ = transport.Dial(socket)
		transport.Call(conn, transport.Request{Op: "OpenAcquisition", ID: m.ID})
		conn.Close()
		cancel()
		if e = <-done; e != nil {
			t.Fatal(e)
		}
		s, e = storage.Open(root, 1<<20)
		if e != nil {
			t.Fatal(e)
		}
	}
}
func TestStaleSocketAndShutdown(t *testing.T) {
	s, _ := storage.Open(t.TempDir(), 1024)
	socket, cancel, done := server(t, s)
	ctx, c := context.WithCancel(context.Background())
	defer c()
	if e := transport.Serve(ctx, socket, s, 1, 1024, slog.Default()); e == nil {
		t.Fatal("duplicate socket")
	}
	conn, e := net.Dial("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.Write([]byte{255, 255, 255, 255})
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = conn.Read(make([]byte, 1)); e == nil {
		t.Fatal("oversized message accepted")
	}
	idle, e := net.Dial("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	defer idle.Close()
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown blocked")
	}
}
