# met-to-wg

A small Go service that polls three Lake Balaton weather stations
(Csopak, Balatonfüred, Balatonalmádi) every minute, deduplicates their
readings in SQLite, and republishes new observations to
[Windguru's upload API](https://www.windguru.cz/upload/).

## Implementation

This version is built to run **locally on macOS** (eventually inside a
Kubernetes pod on a NAS) with:
- **SQLite + WAL** as the store — single-file, easy to back up, no
  daemon to keep alive. WAL mode is the form a [Litestream](https://litestream.io) 
  sidecar will eventually be able to stream over NFS without blocking writers.
- **SOPS** for secrets — credentials live encrypted on disk and only
  exist as environment variables for the lifetime of the process.
- **No CGO** — uses [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite),
  so `go build` produces a single static-friendly binary you can copy
  anywhere.

## Layout

    cmd/met-to-wg/                 main(), signal handling, wiring
    internal/config/               env-var loader + validation
    internal/observation/          the Observation record type
    internal/storage/              SQLite (WAL) + embedded migrations
    internal/stations/             station definitions and HTML parsers
    internal/httpx/                injectable HTTP fetcher
    internal/windguru/             signed Windguru upload client
    internal/healthcheck/          healthchecks.io ping
    internal/processor/            per-tick orchestration
    internal/scheduler/            tick-driven main loop
    testdata/                      fixture HTML used by parser + integration tests
    migrations/                    (lives under internal/storage/migrations/)

The interesting layering is at the seams:

- The processor depends on `Fetcher`, `Storage`, `Uploader`,
  `Healthchecker` **interfaces**, so every test substitutes a fake.
- Parsers are pure functions over `*goquery.Document` — they have no
  network or DB dependency, and they're golden-tested against the
  HTML fixtures.
- The scheduler takes a `TickSource` interface; in tests we drive it
  with a fake channel so no wall-clock sleeps are needed.

## Building & running

Install Go via mise (the project pins `golang 1.26.3` in `.tool-versions`):

    cd met-to-wg
    mise install
    make build        # produces ./met-to-wg

Run:

    sops exec-env secrets.enc.yaml './met-to-wg'

For local dev without SOPS:

    export DATABASE_PATH=./dev.sqlite
    export CSOPAK_WEATHER_UID=...
    export CSOPAK_WEATHER_API_PASSWORD=...
    ./met-to-wg

## Tests

    make test              # full suite
    make test-race         # race detector enabled
    go test -cover ./...   # coverage by package

What's covered:

| Package          | Strategy                                                                                |
|------------------|-----------------------------------------------------------------------------------------|
| `observation`    | Table-driven unit tests for `NullableFloat` (zero, negative, `MaxFloat64`, NaN, ±Inf) + zero-value `Observation` invariants. |
| `stations`       | Golden parser tests against the real HTML fixtures + synthetic edge cases (N/A, malformed date, unknown label). |
| `storage`        | Real on-disk SQLite in a `t.TempDir()`, asserts WAL is on, unique-index dedup, idempotent migrations. |
| `windguru`       | `httptest.Server` records the URL; salt/hash are deterministic via an injected clock.   |
| `healthcheck`    | `httptest.Server` records hits; nil receiver is a no-op for ergonomic "disabled" config. |
| `httpx`          | `httptest.Server` exercises happy path, non-2xx errors, context cancellation.           |
| `scheduler`      | Fake tick source fires deterministically; race-checked initial tick + cancel.           |
| `processor`      | Hand-rolled fakes for every collaborator. Asserts dedup, panic recovery, parse-error isolation, **concurrency bound is respected**. |
| `processor` (integration) | End-to-end: real fetcher + real SQLite + real Windguru client, all against `httptest.Server` returning the fixture HTML. Two ticks → one set of uploads. |

Nothing in the suite touches the public internet or a real DB beyond
SQLite files inside `t.TempDir()`.

## Secrets via SOPS

The binary itself only reads env vars. SOPS is used as a wrapper:

    # one-time: pick a key and tell sops to use it
    edit .sops.yaml

    cp secrets.example.yaml secrets.yaml
    $EDITOR secrets.yaml           # fill in real values
    sops -e secrets.yaml > secrets.enc.yaml
    rm secrets.yaml                # don't commit the plaintext

    sops exec-env secrets.enc.yaml './met-to-wg'

`secrets.yaml` is gitignored; `secrets.enc.yaml` is safe to commit.

## SQLite + Litestream

`storage.Open` enables:

    journal_mode = WAL
    synchronous  = NORMAL
    busy_timeout = 5000ms
    foreign_keys = ON

This is the configuration Litestream expects. The eventual deployment
is a Kubernetes pod with a Litestream sidecar streaming WAL frames to
an NFS export on the NAS:

    [pod]
      ├── met-to-wg            (writes to /data/obs.sqlite)
      └── litestream replicate (tails /data/obs.sqlite-wal → nfs:/backups)

Locally the same DB file works without any sidecar; Litestream simply
isn't running.

## Configuration reference

All settings are environment variables. Missing required values cause
the process to exit at startup with a structured-log error.

| Variable                       | Required | Default              | Notes                                    |
|--------------------------------|----------|----------------------|------------------------------------------|
| `DATABASE_PATH`                | yes      | —                    | Path to the SQLite file.                 |
| `INTERVAL`                     | no       | `60s`                | Poll cadence.                            |
| `CONCURRENCY`                  | no       | `2`                  | Max stations in flight per tick.         |
| `FETCH_TIMEOUT`                | no       | `15s`                | HTTP timeout for source pages.           |
| `UPLOAD_TIMEOUT`               | no       | `15s`                | HTTP timeout for Windguru uploads.       |
| `USER_AGENT`                   | no       | `met-to-wg/1.0`      | Sent on outgoing requests.               |
| `WINDGURU_BASE_URL`            | no       | upstream production  | Useful for staging / testing.            |
| `HEALTHCHECK_URL`              | no       | (disabled)           | e.g. `https://hc-ping.com/<uuid>`.       |
| `CSOPAK_WEATHER_UID`           | per stn  | —                    | Disable Csopak by leaving empty.         |
| `CSOPAK_WEATHER_API_PASSWORD`  | per stn  | —                    |                                          |
| `FURED_WEATHER_UID`            | per stn  | —                    | Disable Balatonfüred by leaving empty.   |
| `FURED_WEATHER_API_PASSWORD`   | per stn  | —                    |                                          |
| `ALMADI_WEATHER_UID`           | per stn  | —                    | Disable Balatonalmádi by leaving empty.  |
| `ALMADI_WEATHER_API_PASSWORD`  | per stn  | —                    |                                          |

At least one station must be configured.

## Quirks worth knowing

- **Source timestamps are parsed in `Europe/Budapest`, then stored as UTC.**
  The pages publish naive Hungarian local time without a TZ marker; the
  parsers in `internal/stations` apply CET/CEST via
  `time.ParseInLocation(..., hungaryTZ)` and the persisted value is the
  resulting UTC instant. `time/tzdata` is imported blank so the binary
  carries its own zoneinfo and works in minimal container images.
- **`water_temperature` is never uploaded.** Windguru does not accept
  it. It is parsed and persisted from Csopak (where it's measured) so
  we keep the data, but `stations.csopakUploadFields` excludes it
  from the request to Windguru.
- **Per-tick failures are swallowed.** If one station's source is down
  or its HTML changes, the processor logs and continues. The other
  stations keep working; the next tick retries.
- **Upload-after-persist.** If the Windguru POST fails, the row is
  already in SQLite — a future tick will *not* try to re-upload it
  (the dedup check short-circuits). Use the Windguru UI to backfill if
  this matters.
