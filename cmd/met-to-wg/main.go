// Command met-to-wg polls Lake Balaton weather stations and republishes their
// observations to Windguru. Configuration comes entirely from environment
// variables; see the project README for the full list.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"met-to-wg/internal/config"
	"met-to-wg/internal/healthcheck"
	"met-to-wg/internal/httpx"
	"met-to-wg/internal/processor"
	"met-to-wg/internal/scheduler"
	"met-to-wg/internal/stations"
	"met-to-wg/internal/status"
	"met-to-wg/internal/storage"
	"met-to-wg/internal/windguru"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	defer db.Close()

	st := buildStations(cfg)
	if len(st) == 0 {
		// config.Load already guarantees at least one station, but keep the
		// guard so a future refactor doesn't accidentally let an empty list
		// through.
		return processor.ErrNoStations
	}

	fetcher := httpx.New(cfg.FetchTimeout, cfg.UserAgent)
	uploader := windguru.New(cfg.WindguruBaseURL, cfg.UploadTimeout)
	hc := healthcheck.New(cfg.HealthcheckURL, cfg.FetchTimeout)

	p := &processor.Processor{
		Stations:    st,
		Fetcher:     fetcher,
		Storage:     db,
		Uploader:    uploader,
		Healthcheck: hc,
		Concurrency: cfg.Concurrency,
	}

	sched := &scheduler.Scheduler{
		Source: scheduler.RealTicker(cfg.Interval),
		Tick:   p.Tick,
	}

	slog.Info("met-to-wg starting",
		"stations", stationNames(st),
		"interval", cfg.Interval,
		"db", cfg.DatabasePath,
		"healthcheck_enabled", cfg.HealthcheckURL != "",
		"status_addr", cfg.StatusAddr,
	)

	if cfg.StatusAddr != "" {
		go runStatusServer(ctx, cfg.StatusAddr, db, st)
	}

	return sched.Run(ctx)
}

// runStatusServer starts the local HTML status page. It is intended for
// CLI/dev runs — the cluster deployment leaves STATUS_ADDR unset.
func runStatusServer(ctx context.Context, addr string, db *storage.DB, st []*stations.Station) {
	srv := &http.Server{
		Addr: addr,
		Handler: (&status.Server{
			Storage:  db,
			Stations: st,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	slog.Info("status server listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("status server failed", "err", err)
	}
}

func buildStations(cfg *config.Config) []*stations.Station {
	var out []*stations.Station
	if cfg.Csopak.UID != "" {
		out = append(out, stations.NewCsopak(cfg.Csopak.UID, cfg.Csopak.Password))
	}
	if cfg.Balatonfured.UID != "" {
		out = append(out, stations.NewBalatonfured(cfg.Balatonfured.UID, cfg.Balatonfured.Password))
	}
	if cfg.Balatonalmadi.UID != "" {
		out = append(out, stations.NewBalatonalmadi(cfg.Balatonalmadi.UID, cfg.Balatonalmadi.Password))
	}
	return out
}

func stationNames(ss []*stations.Station) []string {
	names := make([]string, len(ss))
	for i, s := range ss {
		names[i] = s.Name
	}
	return names
}
