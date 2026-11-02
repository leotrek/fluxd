package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"fluxd/internal/config"
	"fluxd/internal/daemon"
)

func main() {
	path := flag.String("config", "", "JSON configuration file")
	flag.Parse()
	c, e := config.Load(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	var level slog.Level
	level.UnmarshalText([]byte(c.LogLevel))
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e = daemon.Run(ctx, c, log); e != nil {
		log.Error("daemon failed", "error", e)
		os.Exit(1)
	}
}
