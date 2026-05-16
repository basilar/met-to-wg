# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`met-to-wg` is a Go service that polls three Lake Balaton weather stations
(Csopak, Balatonfüred, Balatonalmádi) every minute, deduplicates their readings
in a local SQLite DB, and republishes new observations to Windguru's upload API.
Toolchain pinned by `.tool-versions` (`golang 1.26.3` via mise); `go.mod`
declares `go 1.23`.

## Common commands

```
make build       # go build -o met-to-wg ./cmd/met-to-wg
make test        # go test ./...
make test-race   # go test -race -count=1 ./...
make vet
make tidy
make run         # sops exec-env secrets.enc.yaml 'go run ./cmd/met-to-wg'
```

Run a single test: `go test ./internal/processor -run TestTick_DedupsAcrossTicks -v`.
Coverage by package: `go test -cover ./...`.

Running the binary requires env vars (see README "Configuration reference"). The
normal path is `sops exec-env secrets.enc.yaml './met-to-wg'`. For local dev
without SOPS, export `DATABASE_PATH` plus at least one station's
`*_WEATHER_UID` / `*_WEATHER_API_PASSWORD` pair — `config.Load` fails fast if
no station is configured.

## Architecture

The code is organized around a single per-tick orchestration in
`internal/processor`. The interesting layering is at the seams — most
collaborators are interfaces so every test substitutes a fake:

- `cmd/met-to-wg/main.go` wires concrete implementations
  (`httpx.New`, `windguru.New`, `storage.Open`, `healthcheck.New`,
  `scheduler.RealTicker`) into a `processor.Processor` and a `scheduler.Scheduler`.
- `scheduler.Scheduler` runs an initial tick immediately, then drives
  `processor.Tick` on every signal from an injected `TickSource` until the
  context is cancelled. Tests pass a fake channel so there are no wall-clock sleeps.
- `processor.Tick` fans out across stations with a bounded worker pool
  (`Concurrency`, default 2). Per-station goroutines recover from panics and
  log; one broken source never takes down the loop. `Tick` itself returns no
  error — all failures are swallowed at the station boundary.
- For each station the flow is: `Fetcher.Get` → `Station.Parse` →
  `Storage.HasObservation` (dedup) → `Storage.InsertObservation` →
  `Uploader.Upload`. The healthcheck ping fires once per tick, before the fan-out.

Stations are values, not types. `stations.Station` bundles `Name`, `URL`,
`Location` (a stable int persisted to the DB — `LocCsopak=1`,
`LocBalatonfured=2`, `LocBalatonalmadi=3` and these IDs must not change), Windguru
credentials, a `Parser`, and an `UploadFields` function. Parsers are free
functions over `*goquery.Document` with no I/O — golden-tested against the
HTML fixtures in `testdata/`. Two parser families exist:

- `parseCsopak` (single station) — reads `.localinfo_td_text` cells as
  `(label, value)` pairs plus a trailing date cell. Returns `(nil, nil)` on
  "N/A" to mean "skip this tick".
- `makeMetHuParser` (shared by Balatonfüred and Balatonalmádi) — zips
  `.cella_bal` labels with `.cella_jobb` values; only wind data is captured.

`observation.Observation` is the value moved through that pipeline. Optional
fields are `sql.NullFloat64` so "not measured" (e.g. met.hu stations don't
report temperature) is distinguishable from "zero". Use `NullableFloat(v)` to
construct a valid reading — a bare `sql.NullFloat64{}` means NULL.

`storage.Open` opens SQLite via `modernc.org/sqlite` (no CGO) with
`journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`,
`foreign_keys=ON`, and `SetMaxOpenConns(1)`. Migrations live in
`internal/storage/migrations/*.sql`, are `//go:embed`'d, and applied in
lexicographic order with a `schema_migrations` ledger. Dedup is enforced by a
unique index on `(datetime, location)`.

`windguru.Client` signs each request: `salt = md5(unix_ts)`,
`hash = md5(salt + uid + password)`, query string keys sorted for
determinism. Sent as HTTP GET (Windguru parses the query string regardless of
verb). `Now` is injectable for deterministic tests.

## Quirks to preserve

These are intentional behaviors, not bugs — don't "fix" them without explicit
direction:

- **Source page timestamps are parsed in `Europe/Budapest`** (a package-level
  `hungaryTZ` in `internal/stations/station.go`) and converted to UTC before
  persistence. The pages emit naive "YYYY.MM.DD HH:MM" with no offset; doing
  the conversion manually would get DST wrong twice a year. `time/tzdata` is
  imported blank so static binaries carry the zoneinfo.
- **`water_temperature` is parsed and persisted but never uploaded** — Windguru
  rejects the field. `stations.csopakUploadFields` excludes it.
- **Upload-after-persist is one-shot.** If `InsertObservation` succeeds but
  `Upload` fails, the row is in SQLite and the dedup check will prevent any
  future tick from retrying the upload. Backfill via Windguru's UI if needed.
- **Per-tick station failures are swallowed and logged**, never propagated.

## Secrets

`secrets.yaml` is gitignored plaintext; `secrets.enc.yaml` is the SOPS-encrypted
form and is safe to commit. The binary itself only reads env vars — never read
secret files directly from Go code.
