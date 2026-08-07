// Command eclite polls a smart power meter over a BP35A1 Wi-SUN dongle via
// ECHONET Lite and exposes the readings on a Prometheus /metrics endpoint.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/walkure/eclite/internal/app"
	"github.com/walkure/eclite/internal/config"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("ECLITE_DEBUG") != "" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	configPath := os.Getenv("ECLITE_CONFIG")
	if configPath == "" {
		configPath = "./config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = app.Run(ctx, cfg, app.Options{})
	if err != nil {
		logger.Error("exiting", "err", err)
	}
	// app.Run already calls the configured Exit (os.Exit in production) as
	// its last action before returning, so control does not normally
	// reach past this point.
}
