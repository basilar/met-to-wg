// Package processor orchestrates one polling tick: for every configured
// station it fetches the source page, parses it, deduplicates against
// storage, persists fresh observations, and uploads them to Windguru.
//
// All collaborators are interfaces so the processor is fully testable
// without network or filesystem access.
package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"met-to-wg/internal/observation"
	"met-to-wg/internal/stations"
)

// Fetcher retrieves a station's source page.
type Fetcher interface {
	Get(ctx context.Context, url string) (io.ReadCloser, error)
}

// Storage handles dedup + persistence.
type Storage interface {
	HasObservation(ctx context.Context, datetime time.Time, location int) (bool, error)
	InsertObservation(ctx context.Context, obs *observation.Observation) error
}

// Uploader forwards a fresh observation to Windguru.
type Uploader interface {
	Upload(ctx context.Context, uid, password string, fields map[string]string) error
}

// Healthchecker emits a "still alive" heartbeat. May be nil.
type Healthchecker interface {
	Ping(ctx context.Context) error
}

// Processor runs one tick of the polling loop.
type Processor struct {
	Stations    []*stations.Station
	Fetcher     Fetcher
	Storage     Storage
	Uploader    Uploader
	Healthcheck Healthchecker
	Concurrency int
	Logger      *slog.Logger
}

// Tick runs a single polling cycle: heartbeat (best-effort) followed by a
// bounded-concurrency fan-out across stations. Tick never returns an error —
// failures are logged per-station so one broken source can't take down the
// whole worker.
func (p *Processor) Tick(ctx context.Context) {
	logger := p.logger()

	if p.Healthcheck != nil {
		if err := p.Healthcheck.Ping(ctx); err != nil {
			logger.Warn("healthcheck ping failed", "err", err)
		}
	}

	concurrency := p.Concurrency
	if concurrency <= 0 {
		concurrency = 2
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, st := range p.Stations {
		wg.Add(1)
		sem <- struct{}{}
		go func(st *stations.Station) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					logger.Error("station goroutine panicked",
						"station", st.Name, "panic", fmt.Sprintf("%v", r))
				}
			}()
			p.processStation(ctx, st)
		}(st)
	}
	wg.Wait()
}

func (p *Processor) processStation(ctx context.Context, st *stations.Station) {
	logger := p.logger().With("station", st.Name, "location", st.Location)

	body, err := p.Fetcher.Get(ctx, st.URL)
	if err != nil {
		logger.Error("fetch failed", "err", err)
		return
	}
	defer body.Close()

	obs, err := st.Parse(body)
	if err != nil {
		logger.Error("parse failed", "err", err)
		return
	}
	if obs == nil {
		logger.Info("parser returned nil — skipping tick")
		return
	}
	obs.Location = st.Location // belt-and-braces: parser already sets this

	has, err := p.Storage.HasObservation(ctx, obs.Datetime, obs.Location)
	if err != nil {
		logger.Error("dedup lookup failed", "err", err)
		return
	}
	if has {
		logger.Debug("observation already persisted, skipping upload", "datetime", obs.Datetime)
		return
	}
	if err := p.Storage.InsertObservation(ctx, obs); err != nil {
		logger.Error("insert failed", "err", err)
		return
	}
	if err := p.Uploader.Upload(ctx, st.UID, st.Password, st.UploadFields(obs)); err != nil {
		// The observation is already persisted, so a future tick won't try to
		// re-upload — that's intentional. Use Windguru's UI to backfill if a
		// transient outage caused a miss.
		logger.Error("windguru upload failed", "err", err)
		return
	}
	logger.Info("uploaded new observation", "datetime", obs.Datetime)
}

func (p *Processor) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// ErrNoStations is reported by config-level validation; the processor itself
// is happy to run an empty station list (it'll just do nothing per tick).
var ErrNoStations = errors.New("no stations configured")
