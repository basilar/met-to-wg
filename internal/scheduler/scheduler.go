// Package scheduler drives a Tick function on an injectable cadence.
//
// In production the cadence is a *time.Ticker; in tests it's a channel that
// the test fires manually, so the scheduler loop can be exercised without
// real-time sleeps.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// TickSource yields ticks until Stop is called.
type TickSource interface {
	C() <-chan time.Time
	Stop()
}

// Scheduler invokes Tick once immediately on Run, then on every signal from
// TickSource.C until the context is cancelled.
type Scheduler struct {
	Source TickSource
	Tick   func(context.Context)
	Logger *slog.Logger
}

// Run blocks until ctx is cancelled. Returns ctx.Err() on shutdown.
func (s *Scheduler) Run(ctx context.Context) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	defer s.Source.Stop()

	logger.Info("scheduler starting; firing initial tick")
	s.Tick(ctx)

	for {
		select {
		case <-ctx.Done():
			logger.Info("scheduler stopping", "reason", ctx.Err())
			return ctx.Err()
		case <-s.Source.C():
			s.Tick(ctx)
		}
	}
}

// RealTicker wraps time.Ticker to satisfy TickSource. The constructor returns
// the interface, not the struct, so callers can swap it for a fake.
func RealTicker(d time.Duration) TickSource {
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }
