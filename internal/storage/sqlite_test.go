package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"met-to-wg/internal/observation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	// Use a real on-disk file so WAL mode + the schema_migrations table
	// behave the way they will in production.
	path := filepath.Join(t.TempDir(), "test.sqlite")
	db, err := Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sampleObservation(ts time.Time, location int) *observation.Observation {
	return &observation.Observation{
		Datetime:         ts,
		Location:         location,
		MSLP:             observation.NullableFloat(1013.2),
		RH:               observation.NullableFloat(80),
		Temperature:      observation.NullableFloat(17.5),
		WaterTemperature: observation.NullableFloat(20.1),
		WindAvg:          5.4,
		WindDirection:    180,
		WindMax:          sql.NullFloat64{},
	}
}

func TestOpenSetsWALMode(t *testing.T) {
	db := newTestDB(t)
	var mode string
	require.NoError(t, db.db.QueryRow("PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode, "WAL is required for Litestream replication")
}

func TestInsertAndHasObservation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)

	has, err := db.HasObservation(ctx, ts, 2)
	require.NoError(t, err)
	assert.False(t, has, "empty DB should not report the observation as present")

	require.NoError(t, db.InsertObservation(ctx, sampleObservation(ts, 2)))

	has, err = db.HasObservation(ctx, ts, 2)
	require.NoError(t, err)
	assert.True(t, has)

	// Different location at the same time is still a miss.
	has, err = db.HasObservation(ctx, ts, 3)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestDuplicateInsertRejected(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2024, 5, 21, 7, 55, 0, 0, time.UTC)

	require.NoError(t, db.InsertObservation(ctx, sampleObservation(ts, 2)))
	err := db.InsertObservation(ctx, sampleObservation(ts, 2))
	require.Error(t, err, "the (datetime, location) unique index must reject duplicates")
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	first, err := Open(context.Background(), path)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	// Re-opening must replay nothing and not error.
	second, err := Open(context.Background(), path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	var migrationCount int
	require.NoError(t, second.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount))
	assert.Equal(t, 2, migrationCount, "shipped migrations should each be recorded exactly once")
}

func TestCountObservations(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	n, err := db.CountObservations(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	for i := range 3 {
		ts := time.Date(2024, 5, 21, 7, 55+i, 0, 0, time.UTC)
		require.NoError(t, db.InsertObservation(ctx, sampleObservation(ts, 1)))
	}
	n, err = db.CountObservations(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
}

func TestDatetimeStoredAsUTC(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Budapest")
	require.NoError(t, err)
	localTime := time.Date(2024, 5, 21, 9, 55, 0, 0, loc) // 07:55 UTC
	require.NoError(t, db.InsertObservation(ctx, sampleObservation(localTime, 2)))

	// Looking it up by the UTC equivalent must hit.
	utcTime := localTime.UTC()
	has, err := db.HasObservation(ctx, utcTime, 2)
	require.NoError(t, err)
	assert.True(t, has, "storage normalises datetime to UTC")
}
