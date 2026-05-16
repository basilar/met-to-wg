package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTickSource lets tests fire ticks deterministically.
type fakeTickSource struct {
	ch      chan time.Time
	stopped atomic.Bool
}

func newFakeTickSource() *fakeTickSource     { return &fakeTickSource{ch: make(chan time.Time, 8)} }
func (f *fakeTickSource) C() <-chan time.Time { return f.ch }
func (f *fakeTickSource) Stop()                { f.stopped.Store(true) }
func (f *fakeTickSource) Fire()                { f.ch <- time.Now() }

func TestRun_FiresInitialTickAndThenOnEvery(t *testing.T) {
	src := newFakeTickSource()
	var ticks int32

	s := &Scheduler{
		Source: src,
		Tick: func(context.Context) {
			atomic.AddInt32(&ticks, 1)
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// The initial tick runs synchronously inside Run, so by the time we get
	// here it is either done or in-flight; poll briefly.
	require.Eventually(t, func() bool { return atomic.LoadInt32(&ticks) >= 1 }, time.Second, 5*time.Millisecond)

	src.Fire()
	src.Fire()
	require.Eventually(t, func() bool { return atomic.LoadInt32(&ticks) >= 3 }, time.Second, 5*time.Millisecond)

	cancel()
	err := <-done
	require.ErrorIs(t, err, context.Canceled)
	assert.True(t, src.stopped.Load(), "scheduler must call Stop on its source on shutdown")
}

func TestRun_ContextCancelStopsLoop(t *testing.T) {
	src := newFakeTickSource()
	s := &Scheduler{Source: src, Tick: func(context.Context) {}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	err := s.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRealTicker_StopReleasesResources(t *testing.T) {
	tk := RealTicker(50 * time.Millisecond)
	// Receive at least one tick, then stop.
	select {
	case <-tk.C():
	case <-time.After(time.Second):
		t.Fatal("expected a tick within 1s")
	}
	tk.Stop()
}
