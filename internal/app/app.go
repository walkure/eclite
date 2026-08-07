// Package app wires skstack, echonet, meter, and metricsexport together
// into the running daemon — the equivalent of echonet.pl's top-level code:
// config load, socket setup, signal handling, and the main loop.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/walkure/eclite/internal/config"
	"github.com/walkure/eclite/internal/echonet"
	"github.com/walkure/eclite/internal/meter"
	"github.com/walkure/eclite/internal/metricsexport"
	"github.com/walkure/eclite/internal/skstack"
)

// Options lets callers (production main, and tests) substitute everything
// Run would otherwise reach for directly: the serial transport, the HTTP
// listener, the process-exit function, the clock, and the logger.
type Options struct {
	NewTransport func(device string) (skstack.Transport, error)
	Listen       func(addr string) (net.Listener, error)
	// Exit is called exactly once, as Run's last action on every shutdown
	// path (clean or fatal) — matching echonet.pl's SIGINT/SIGTERM
	// handlers and the timeout/WDT restart_process fix: any exit here is
	// meant to end the process so the container supervisor restarts it.
	// Defaults to os.Exit. Tests substitute a func that records the call
	// instead of terminating, so they can assert intent without killing
	// the test binary.
	Exit   func(code int)
	Clock  meter.Clock
	Logger *slog.Logger

	// shutdownGrace bounds the HTTP server's graceful shutdown. Not part
	// of the public contract (unexported) — tests shrink it directly.
	shutdownGrace time.Duration

	// wdtCheckInterval overrides how often the WDT ticker polls
	// staleness. Unexported: production always uses the window/8 default;
	// tests shrink it since the ticker itself runs on real wall-clock
	// time even when a fake meter.Clock is injected (Go's time.Ticker
	// can't be mocked) — only the staleness *comparison* is fake-clock-
	// driven, so the poll interval must be shrunk for a test to observe
	// a trip without waiting out a real production-sized window.
	wdtCheckInterval time.Duration
}

func (o Options) withDefaults() Options {
	if o.NewTransport == nil {
		o.NewTransport = skstack.NewSerialTransport
	}
	if o.Listen == nil {
		o.Listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
	}
	if o.Exit == nil {
		o.Exit = os.Exit
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.shutdownGrace <= 0 {
		o.shutdownGrace = 5 * time.Second
	}
	return o
}

// Run wires everything together and blocks until a fatal condition (lost
// meter, WDT trip, handshake failure, HTTP listener failure, or ctx
// cancellation from SIGINT/SIGTERM) occurs. It then performs a unified
// shutdown — best-effort SKTERM, stop the HTTP server, close the serial
// port — calls opts.Exit (production: os.Exit(1)), and returns the error
// describing why.
//
// There is deliberately no in-process reconnect/retry beyond what's
// already inside skstack.Client.Connect. echonet.pl's SKTERM->SKRESET
// auto-rejoin (triggered from inside its line-dispatch callback, at any
// point in the process's life) is exactly the kind of half-working
// complexity that made its own timeout/restart design silently not work;
// a hard exit and letting systemd restart the container is the intended
// reliability model here.
func Run(ctx context.Context, cfg *config.Config, opts Options) error {
	opts = opts.withDefaults()
	logger := opts.Logger

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	tr, err := opts.NewTransport(cfg.BP35A1.Device)
	if err != nil {
		err = fmt.Errorf("open transport: %w", err)
		logger.Error("app: exiting", "err", err)
		opts.Exit(1)
		return err
	}

	client := skstack.NewClient(tr, cfg.BP35A1.ID, cfg.BP35A1.Pass, logger)

	if err := client.Connect(runCtx); err != nil {
		tr.Close()
		err = fmt.Errorf("handshake: %w", err)
		logger.Error("app: exiting", "err", err)
		opts.Exit(1)
		return err
	}
	logger.Info("app: connected to meter")

	state := meter.New(logger, opts.Clock)
	state.MarkConnected(state.Now())

	requestWatt := func() {
		if err := client.SendUDP(echonet.BuildGetRequest(echonet.EPCCumulativeEnergy, echonet.EPCInstantaneousWatt)); err != nil {
			logger.Warn("app: failed to request watt/kwh refresh", "err", err)
		}
	}
	collector := metricsexport.NewCollector(state, cfg.Prometheus.Labels, requestWatt)
	reg := prometheus.NewRegistry()
	reg.MustRegister(collector)
	promHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if !state.Snapshot().Ready {
			logger.Info("app: /metrics requested before initialized")
			http.Error(w, "not initialized", http.StatusInternalServerError)
			return
		}
		promHandler.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", cfg.Prometheus.HTTP)
	ln, err := opts.Listen(addr)
	if err != nil {
		client.Terminate()
		tr.Close()
		err = fmt.Errorf("listen %s: %w", addr, err)
		logger.Error("app: exiting", "err", err)
		opts.Exit(1)
		return err
	}
	httpServer := &http.Server{Handler: mux}
	httpServeErr := make(chan error, 1)
	go func() { httpServeErr <- httpServer.Serve(ln) }()

	// Initial property query, mirroring echonet.pl's on_connected.
	initialQuery := echonet.BuildGetRequest(
		echonet.EPCCoefficient, echonet.EPCUnit, echonet.EPCCumulativeEnergy,
		echonet.EPCInstantaneousWatt, echonet.EPCApplianceVersion,
	)
	if err := client.SendUDP(initialQuery); err != nil {
		logger.Warn("app: failed to send initial query", "err", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- client.Run(runCtx) }()

	erxudpDone := make(chan struct{})
	go func() {
		defer close(erxudpDone)
		for {
			select {
			case <-runCtx.Done():
				return
			case msg, ok := <-client.ERXUDP():
				if !ok {
					return
				}
				frame, err := echonet.Parse(msg.Data)
				if err != nil {
					logger.Warn("app: malformed ECHONET Lite frame, ignoring", "err", err)
					continue
				}
				result := state.HandleFrame(frame)
				if result.ResendReducedEPCs != nil {
					if err := client.SendUDP(echonet.BuildGetRequest(result.ResendReducedEPCs...)); err != nil {
						logger.Warn("app: failed to resend reduced query", "err", err)
					}
				}
			}
		}
	}()

	wdtWindow := time.Duration(cfg.Prometheus.WDT) * time.Minute
	wdtTrip := runWDT(runCtx, state, wdtWindow, opts.wdtCheckInterval)

	var fatalErr error
	select {
	case <-runCtx.Done():
		fatalErr = ctx.Err()
		logger.Info("app: context cancelled, shutting down", "reason", fatalErr)
	case err := <-runErr:
		fatalErr = fmt.Errorf("skstack: %w", err)
		logger.Error("app: fatal skstack condition, shutting down", "err", err)
	case since := <-wdtTrip:
		fatalErr = fmt.Errorf("WDT: no watt reading for %s (window %s)", since, wdtWindow)
		logger.Error("app: WDT tripped, shutting down", "stale_for", since, "window", wdtWindow)
	case err := <-httpServeErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatalErr = fmt.Errorf("http server: %w", err)
			logger.Error("app: http server failed, shutting down", "err", err)
		}
	}

	// Unified shutdown funnel: stop internal goroutines, best-effort
	// SKTERM (release the PANA session so the meter doesn't hold a stale
	// one), stop HTTP, close the serial port.
	cancelRun()
	client.Terminate()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), opts.shutdownGrace)
	_ = httpServer.Shutdown(shutdownCtx)
	cancelShutdown()
	tr.Close()
	<-erxudpDone

	opts.Exit(1)
	return fatalErr
}

// runWDT starts an independent ticker-driven watchdog goroutine, decoupled
// from HTTP scrape traffic. echonet.pl's WDT check only runs when the
// HTTP listener itself becomes readable — i.e. only when a scrape request
// arrives — so if Prometheus stops scraping, the WDT silently never
// fires at all, defeating its own purpose. This runs on wall-clock time
// regardless of scrape activity.
func runWDT(ctx context.Context, state *meter.State, window, interval time.Duration) <-chan time.Duration {
	trip := make(chan time.Duration, 1)
	if interval <= 0 {
		interval = window / 8
		if interval < time.Second {
			interval = time.Second
		}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if since, stale := state.StaleFor(window); stale {
					select {
					case trip <- since:
					default:
					}
					return
				}
			}
		}
	}()
	return trip
}
