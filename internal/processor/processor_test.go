package processor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"met-to-wg/internal/observation"
	"met-to-wg/internal/stations"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFetcher returns canned bodies per URL.
type fakeFetcher struct {
	mu      sync.Mutex
	bodies  map[string]string
	errs    map[string]error
	getURLs []string
}

func (f *fakeFetcher) Get(_ context.Context, url string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getURLs = append(f.getURLs, url)
	if err, ok := f.errs[url]; ok {
		return nil, err
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, errors.New("no body for url")
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

// fakeStorage is a thread-safe in-memory store keyed on (datetime, location).
type fakeStorage struct {
	mu        sync.Mutex
	rows      map[string]*observation.Observation
	insertErr error
	hasErr    error
}

func newFakeStorage() *fakeStorage { return &fakeStorage{rows: map[string]*observation.Observation{}} }

func key(t time.Time, loc int) string { return t.UTC().Format(time.RFC3339) + "|" + itoa(loc) }

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = digits[i%10]
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

func (s *fakeStorage) HasObservation(_ context.Context, t time.Time, loc int) (bool, error) {
	if s.hasErr != nil {
		return false, s.hasErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rows[key(t, loc)]
	return ok, nil
}

func (s *fakeStorage) InsertObservation(_ context.Context, obs *observation.Observation) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[key(obs.Datetime, obs.Location)] = obs
	return nil
}

func (s *fakeStorage) MarkUploaded(_ context.Context, t time.Time, loc int, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[key(t, loc)]; !ok {
		return fmt.Errorf("mark uploaded: no row for %v/%d", t, loc)
	}
	return nil
}

// fakeUploader records every upload and can be configured to fail.
type fakeUploader struct {
	mu      sync.Mutex
	calls   []map[string]string
	err     error
	cancelC chan struct{}
}

func (u *fakeUploader) Upload(_ context.Context, uid, pw string, fields map[string]string) error {
	if u.err != nil {
		return u.err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make(map[string]string, len(fields)+2)
	for k, v := range fields {
		out[k] = v
	}
	out["__uid"] = uid
	out["__password"] = pw
	u.calls = append(u.calls, out)
	return nil
}

// fakeHealthcheck records hits.
type fakeHealthcheck struct {
	hits int32
	err  error
}

func (h *fakeHealthcheck) Ping(context.Context) error {
	atomic.AddInt32(&h.hits, 1)
	return h.err
}

// stubParser returns a Station whose Parser yields a fixed Observation (or nil
// for "skip"). Used by tests that want to control what the parser produces
// without going through goquery + HTML.
func stubStation(name string, location int, parsed *observation.Observation, parseErr error) *stations.Station {
	return &stations.Station{
		Name:     name,
		URL:      "https://example.test/" + name,
		Location: location,
		UID:      name + "-uid",
		Password: name + "-pw",
		Parser: func(*goquery.Document) (*observation.Observation, error) {
			if parsed != nil {
				cp := *parsed
				return &cp, parseErr
			}
			return nil, parseErr
		},
		UploadFields: func(obs *observation.Observation) map[string]string {
			return map[string]string{"wind_avg": "1.23", "wind_direction": "180"}
		},
	}
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{bodies: map[string]string{}, errs: map[string]error{}}
}

func sampleObs(ts time.Time, location int) *observation.Observation {
	return &observation.Observation{
		Datetime:      ts,
		Location:      location,
		WindAvg:       1.23,
		WindDirection: 180,
	}
}

func TestTick_HappyPath_PersistAndUpload(t *testing.T) {
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)
	st := stubStation("alpha", 7, sampleObs(ts, 7), nil)

	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>anything</html>"

	storage := newFakeStorage()
	uploader := &fakeUploader{}
	hc := &fakeHealthcheck{}

	p := &Processor{
		Stations:    []*stations.Station{st},
		Fetcher:     fetcher,
		Storage:     storage,
		Uploader:    uploader,
		Healthcheck: hc,
		Concurrency: 1,
	}
	p.Tick(context.Background())

	assert.Equal(t, int32(1), atomic.LoadInt32(&hc.hits))
	has, _ := storage.HasObservation(context.Background(), ts, 7)
	assert.True(t, has, "observation must have been persisted")
	require.Len(t, uploader.calls, 1)
	assert.Equal(t, "alpha-uid", uploader.calls[0]["__uid"])
	assert.Equal(t, "alpha-pw", uploader.calls[0]["__password"])
}

func TestTick_SkipsUploadWhenAlreadyPersisted(t *testing.T) {
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)
	st := stubStation("alpha", 7, sampleObs(ts, 7), nil)

	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>x</html>"

	storage := newFakeStorage()
	// Pre-seed: an earlier tick already persisted this reading.
	require.NoError(t, storage.InsertObservation(context.Background(), sampleObs(ts, 7)))

	uploader := &fakeUploader{}
	p := &Processor{
		Stations:    []*stations.Station{st},
		Fetcher:     fetcher,
		Storage:     storage,
		Uploader:    uploader,
		Concurrency: 1,
	}
	p.Tick(context.Background())

	assert.Empty(t, uploader.calls, "dup observation must not re-upload")
}

func TestTick_NilParseResultIsNoop(t *testing.T) {
	st := stubStation("alpha", 7, nil, nil) // parser returns (nil, nil)
	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>x</html>"
	storage := newFakeStorage()
	uploader := &fakeUploader{}

	p := &Processor{
		Stations: []*stations.Station{st},
		Fetcher:  fetcher,
		Storage:  storage,
		Uploader: uploader,
	}
	p.Tick(context.Background())

	n, _ := storage.HasObservation(context.Background(), time.Time{}, 7)
	assert.False(t, n)
	assert.Empty(t, uploader.calls)
}

func TestTick_FetchFailureDoesNotStopOtherStations(t *testing.T) {
	tsA := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)
	tsB := time.Date(2024, 5, 21, 7, 56, 0, 0, time.UTC)
	stA := stubStation("alpha", 1, sampleObs(tsA, 1), nil)
	stB := stubStation("bravo", 2, sampleObs(tsB, 2), nil)

	fetcher := newFakeFetcher()
	fetcher.errs[stA.URL] = errors.New("network down")
	fetcher.bodies[stB.URL] = "<html>x</html>"

	storage := newFakeStorage()
	uploader := &fakeUploader{}
	p := &Processor{
		Stations:    []*stations.Station{stA, stB},
		Fetcher:     fetcher,
		Storage:     storage,
		Uploader:    uploader,
		Concurrency: 2,
	}
	p.Tick(context.Background())

	hasB, _ := storage.HasObservation(context.Background(), tsB, 2)
	assert.True(t, hasB, "bravo should have been processed despite alpha failing")
	require.Len(t, uploader.calls, 1)
}

func TestTick_ParseErrorIsLoggedNotPropagated(t *testing.T) {
	st := stubStation("alpha", 1, sampleObs(time.Now(), 1), errors.New("bad html"))
	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>x</html>"
	storage := newFakeStorage()
	uploader := &fakeUploader{}
	p := &Processor{
		Stations: []*stations.Station{st},
		Fetcher:  fetcher,
		Storage:  storage,
		Uploader: uploader,
	}
	// Mustn't panic, mustn't insert, mustn't upload.
	p.Tick(context.Background())
	assert.Empty(t, uploader.calls)
}

func TestTick_UploadErrorLeavesObservationPersisted(t *testing.T) {
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)
	st := stubStation("alpha", 1, sampleObs(ts, 1), nil)
	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>x</html>"
	storage := newFakeStorage()
	uploader := &fakeUploader{err: errors.New("windguru 500")}

	p := &Processor{
		Stations: []*stations.Station{st},
		Fetcher:  fetcher,
		Storage:  storage,
		Uploader: uploader,
	}
	p.Tick(context.Background())

	has, _ := storage.HasObservation(context.Background(), ts, 1)
	assert.True(t, has, "an upload failure must not delete the persisted record")
}

func TestTick_HealthcheckFailureDoesNotStopTick(t *testing.T) {
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)
	st := stubStation("alpha", 1, sampleObs(ts, 1), nil)
	fetcher := newFakeFetcher()
	fetcher.bodies[st.URL] = "<html>x</html>"
	storage := newFakeStorage()
	uploader := &fakeUploader{}
	hc := &fakeHealthcheck{err: errors.New("hc down")}

	p := &Processor{
		Stations:    []*stations.Station{st},
		Fetcher:     fetcher,
		Storage:     storage,
		Uploader:    uploader,
		Healthcheck: hc,
	}
	p.Tick(context.Background())

	require.Len(t, uploader.calls, 1, "tick must proceed even when the healthcheck ping fails")
}

func TestTick_ConcurrencyBound(t *testing.T) {
	// Build many slow-fetching stations; ensure no more than `Concurrency`
	// are in flight at once.
	const N = 8
	const limit = 3
	inflight := int32(0)
	maxSeen := int32(0)

	fetcher := newFakeFetcher()
	stns := make([]*stations.Station, N)
	for i := 0; i < N; i++ {
		ts := time.Date(2024, 5, 21, 7, 55+i, 0, 0, time.UTC)
		stns[i] = stubStation(itoa(i), i+10, sampleObs(ts, i+10), nil)
		fetcher.bodies[stns[i].URL] = "<html>x</html>"
	}

	wrapped := &countingFetcher{inner: fetcher, inflight: &inflight, max: &maxSeen, delay: 30 * time.Millisecond}

	storage := newFakeStorage()
	uploader := &fakeUploader{}
	p := &Processor{
		Stations:    stns,
		Fetcher:     wrapped,
		Storage:     storage,
		Uploader:    uploader,
		Concurrency: limit,
	}
	p.Tick(context.Background())

	require.LessOrEqual(t, int(atomic.LoadInt32(&maxSeen)), limit,
		"observed inflight must not exceed the configured concurrency")
	require.Len(t, uploader.calls, N)
}

type countingFetcher struct {
	inner    Fetcher
	inflight *int32
	max      *int32
	delay    time.Duration
}

func (f *countingFetcher) Get(ctx context.Context, url string) (io.ReadCloser, error) {
	n := atomic.AddInt32(f.inflight, 1)
	for {
		cur := atomic.LoadInt32(f.max)
		if n <= cur {
			break
		}
		if atomic.CompareAndSwapInt32(f.max, cur, n) {
			break
		}
	}
	time.Sleep(f.delay)
	defer atomic.AddInt32(f.inflight, -1)
	return f.inner.Get(ctx, url)
}
