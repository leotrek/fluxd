// Command fluximport imports image, video or raw files into an offline Flux store.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"fluxd/internal/importer"
)

func main() {
	var o importer.Options
	flag.StringVar(&o.Kind, "kind", "image", "input kind: image, video, raw")
	flag.StringVar(&o.Input, "input", "", "source file or directory (required)")
	flag.StringVar(&o.StateDir, "state-dir", "", "offline state directory (default data/state/<kind>)")
	flag.StringVar(&o.Backend, "backend", "ccsds-123", "codec: ccsds-123, deflate, identity")
	flag.StringVar(&o.PixelFormat, "pixel-format", "rgb8", "image samples: rgb8, rgba8, gray8, gray16; video: rgb8")
	flag.BoolVar(&o.Recursive, "recursive", false, "include subdirectories")
	flag.Int64Var(&o.MaxAcquisitionBytes, "max-acquisition-bytes", 256<<20, "maximum decoded acquisition bytes and image decoder workspace allowance")
	flag.Int64Var(&o.MaxStorageBytes, "max-storage-bytes", 4<<30, "maximum compressed payload bytes in store")
	flag.IntVar(&o.MaxFrames, "max-frames", 0, "maximum frames per video; 0 means all")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments; use -input PATH")
		os.Exit(1)
	}
	if o.StateDir == "" {
		o.StateDir = filepath.Join("data", "state", o.Kind)
	}
	o.Output = os.Stdout
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := importer.Run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
