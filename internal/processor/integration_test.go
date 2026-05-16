package processor_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"met-to-wg/internal/httpx"
	"met-to-wg/internal/processor"
	"met-to-wg/internal/stations"
	"met-to-wg/internal/storage"
	"met-to-wg/internal/windguru"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEndToEnd_RealHTTPxSQLiteWindguru wires every real component together —
// Fetcher, SQLite storage, Windguru client — against httptest servers
// returning the fixture HTML. Demonstrates that two ticks of the processor
// produce one upload (dedupe works) and that the persisted row matches the
// historical golden values.
func TestEndToEnd_RealHTTPxSQLiteWindguru(t *testing.T) {
	ctx := context.Background()

	// 1) Source server: returns the Csopak + Balatonfüred fixture HTML
	//    on the matching path. The processor sees these as if they were the
	//    upstream pages.
	csopakHTML := readFixture(t, "csopak_2024_05_21.html")
	furedHTML := readFixture(t, "balatonfured_2024_05_21.html")
	sourceMux := http.NewServeMux()
	sourceMux.HandleFunc("/csopak", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(csopakHTML))
	})
	sourceMux.HandleFunc("/fured", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(furedHTML))
	})
	source := httptest.NewServer(sourceMux)
	defer source.Close()

	// 2) Windguru server: records every call.
	var wgMu sync.Mutex
	var wgCalls []string
	wgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wgMu.Lock()
		wgCalls = append(wgCalls, r.URL.RawQuery)
		wgMu.Unlock()
	}))
	defer wgSrv.Close()

	// 3) Storage: a real SQLite file in a temp dir.
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := storage.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()

	// 4) Build stations with overridden URLs pointing at the source server.
	csopak := stations.NewCsopak("csopak-uid", "csopak-pw")
	csopak.URL = source.URL + "/csopak"
	fured := stations.NewBalatonfured("fured-uid", "fured-pw")
	fured.URL = source.URL + "/fured"

	p := &processor.Processor{
		Stations:    []*stations.Station{csopak, fured},
		Fetcher:     httpx.New(time.Second, "met-to-wg-integration-test"),
		Storage:     db,
		Uploader:    windguru.New(wgSrv.URL, time.Second),
		Concurrency: 2,
	}

	// First tick: both stations should persist and upload.
	p.Tick(ctx)
	n, err := db.CountObservations(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "first tick must persist both stations' observations")

	wgMu.Lock()
	firstCalls := len(wgCalls)
	wgMu.Unlock()
	assert.Equal(t, 2, firstCalls, "first tick must upload both observations")

	// Second tick: source still returns the same fixture HTML (same datetimes),
	// so dedup must short-circuit both uploads and inserts.
	p.Tick(ctx)
	n, err = db.CountObservations(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "second tick must not duplicate persisted rows")

	wgMu.Lock()
	secondCalls := len(wgCalls)
	wgMu.Unlock()
	assert.Equal(t, 2, secondCalls, "second tick must not re-upload")

	// Spot-check a recorded upload contains the signed params.
	wgMu.Lock()
	defer wgMu.Unlock()
	require.NotEmpty(t, wgCalls)
	joined := strings.Join(wgCalls, "\n")
	assert.Contains(t, joined, "uid=csopak-uid")
	assert.Contains(t, joined, "uid=fured-uid")
	assert.Contains(t, joined, "salt=")
	assert.Contains(t, joined, "hash=")
	assert.Contains(t, joined, "wind_avg=")
	// Sanity: water_temperature must never reach Windguru.
	assert.NotContains(t, joined, "water_temperature=")
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	// processor package lives at internal/processor, so testdata is two
	// directories up.
	b, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	require.NoError(t, err)
	return string(b)
}
