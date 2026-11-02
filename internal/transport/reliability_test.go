package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"fluxd/internal/compression"
	"fluxd/internal/storage"
	"fluxd/internal/testutil"
)

func TestSlowDisconnectAndRepeatedReaders(t *testing.T) {
	s, e := storage.Open(t.TempDir(), 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	m, raw := testutil.Fixture()
	codec, _ := compression.Select("ccsds-123")
	if e = s.Put(context.Background(), m, bytes.NewReader(raw), codec, 4096, 2048); e != nil {
		t.Fatal(e)
	}
	codec, e = compression.Configure(codec, m)
	if e != nil {
		t.Fatal(e)
	}
	fdCount := func() int {
		for _, p := range []string{"/proc/self/fd", "/dev/fd"} {
			entries, err := os.ReadDir(p)
			if err == nil {
				return len(entries)
			}
		}
		return -1
	}
	beforeFD, beforeG := fdCount(), runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		server, client := net.Pipe()
		done := make(chan error, 1)
		go func() { defer server.Close(); done <- handle(context.Background(), server, s, 32) }()
		if _, e = Call(client, Request{Op: "OpenAcquisition", ID: m.ID}); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			select {
			case err := <-done:
				t.Fatalf("slow reader did not block transfer: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
		}
		client.Close()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("disconnect not reported")
			}
		case <-time.After(time.Second):
			t.Fatal("disconnected handler leaked")
		}
	}
	// A fresh consumer can still decode the same retained acquisition after failures.
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() { defer server.Close(); done <- handle(context.Background(), server, s, 32) }()
	res, e := Call(client, Request{Op: "OpenAcquisition", ID: m.ID})
	if e != nil {
		t.Fatal(e)
	}
	var got bytes.Buffer
	if e = codec.Decode(context.Background(), &got, io.LimitReader(client, res.Bytes), make([]byte, 4096)); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got.Bytes(), raw) {
		t.Fatal("reconstruction after consumer failures")
	}
	if _, e = Call(client, Request{Op: "ReleaseAcquisition", ID: m.ID}); e != nil {
		t.Fatal(e)
	}
	client.Close()
	<-done
	// Each handler was joined; allow the runtime to finish deferred teardown.
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > beforeG+1 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if n := runtime.NumGoroutine(); n > beforeG+1 {
		t.Fatalf("goroutines before=%d after=%d", beforeG, n)
	}
	afterFD := fdCount()
	t.Logf("cleanup counters: goroutines before=%d after=%d; FDs before=%d after=%d", beforeG, runtime.NumGoroutine(), beforeFD, afterFD)
	if beforeFD < 0 || afterFD < 0 {
		t.Log("FD counting unavailable; descriptor leak check not performed")
	}
	if beforeFD >= 0 && afterFD > beforeFD {
		t.Fatalf("file descriptors before=%d after=%d", beforeFD, afterFD)
	}
}
