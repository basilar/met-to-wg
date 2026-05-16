// Package storage is the persistence layer. Backed by SQLite in WAL mode so a
// Litestream sidecar can stream WAL frames to remote storage without
// interfering with writers.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"met-to-wg/internal/observation"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a *sql.DB pointed at a SQLite database with WAL enabled.
type DB struct {
	db *sql.DB
}

// Open opens (creating if absent) a SQLite database at path, sets WAL mode and
// a few other pragmas suitable for a long-running poller, and applies any
// pending migrations.
//
// Use ":memory:" for tests when you don't need WAL semantics.
func Open(ctx context.Context, path string) (*DB, error) {
	// _pragma values are applied by the modernc driver when opening the
	// connection. journal_mode=WAL is the headline setting; the rest are
	// reasonable defaults for a single-writer service.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)",
		path,
	)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc.org/sqlite's connection pool is fine but a single connection
	// avoids surprises with WAL checkpointing.
	sqlDB.SetMaxOpenConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	db := &DB{db: sqlDB}
	if err := db.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the underlying database handle.
func (d *DB) Close() error { return d.db.Close() }

// migrate runs every embedded .sql file in lexicographic order. We use a
// schema_migrations table to record which ones have been applied.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var existing string
		err := d.db.QueryRowContext(ctx,
			`SELECT name FROM schema_migrations WHERE name = ?`, name,
		).Scan(&existing)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		sqlBytes, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := d.db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := d.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(name, applied_at) VALUES (?, ?)`,
			name, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

// HasObservation reports whether an observation at the given datetime+location
// already exists. Used for dedup before insert+upload.
func (d *DB) HasObservation(ctx context.Context, datetime time.Time, location int) (bool, error) {
	var id int64
	err := d.db.QueryRowContext(ctx,
		`SELECT id FROM observation WHERE datetime = ? AND location = ?`,
		datetime.UTC().Format(time.RFC3339), location,
	).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("query observation: %w", err)
	default:
		return true, nil
	}
}

// InsertObservation stores a fresh observation. Callers should check
// HasObservation first; the unique index makes a duplicate insert error out
// rather than silently overwrite.
func (d *DB) InsertObservation(ctx context.Context, obs *observation.Observation) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO observation
			(datetime, location, mslp, rh, temperature, water_temperature,
			 wind_avg, wind_direction, wind_max)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		obs.Datetime.UTC().Format(time.RFC3339),
		obs.Location,
		obs.MSLP, obs.RH, obs.Temperature, obs.WaterTemperature,
		obs.WindAvg, obs.WindDirection, obs.WindMax,
	)
	if err != nil {
		return fmt.Errorf("insert observation: %w", err)
	}
	return nil
}

// CountObservations is exposed for tests and ops poking.
func (d *DB) CountObservations(ctx context.Context) (int, error) {
	var n int
	err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observation`).Scan(&n)
	return n, err
}
