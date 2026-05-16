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

## Docker

A minimal multi-stage `Dockerfile` produces a `scratch`-based image
containing only the static binary, the system CA bundle (for HTTPS),
and an empty `/data` directory owned by UID 65534. No shell, no
package manager.

Build (requires `docker buildx` — install via `brew install
docker-buildx` if you only have the legacy builder):

    docker buildx build -t met-to-wg --load .

`--load` puts the image into your local Docker so `docker run` can
find it. Drop it (and add `--push`) when pushing to a registry. For
multi-arch:

    docker buildx build --platform linux/amd64,linux/arm64 \
      -t ghcr.io/you/met-to-wg:latest --push .

Run with secrets injected by SOPS:

    sops exec-env secrets.enc.yaml \
      'docker run -d --name met-to-wg --restart unless-stopped \
         -e DATABASE_PATH=/data/observations.sqlite \
         -e CSOPAK_WEATHER_UID -e CSOPAK_WEATHER_API_PASSWORD \
         -e FURED_WEATHER_UID  -e FURED_WEATHER_API_PASSWORD \
         -e ALMADI_WEATHER_UID -e ALMADI_WEATHER_API_PASSWORD \
         -e HEALTHCHECK_URL -e WINDGURU_BASE_URL \
         -e INTERVAL -e FETCH_TIMEOUT -e UPLOAD_TIMEOUT \
         -e CONCURRENCY -e USER_AGENT \
         -v met-to-wg-data:/data \
         met-to-wg'

`sops exec-env` decrypts into the environment for the lifetime of the
`docker run` invocation; each `-e VAR` (without `=value`) forwards
that variable from the host env into the container. The named volume
`met-to-wg-data` persists the SQLite DB across container restarts.

Alternative without enumerating every variable — let SOPS write a
dotenv file Docker can read directly:

    sops -d --output-type dotenv secrets.enc.yaml > /tmp/met-to-wg.env
    docker run -d --name met-to-wg --restart unless-stopped \
      --env-file /tmp/met-to-wg.env \
      -v met-to-wg-data:/data \
      met-to-wg
    rm /tmp/met-to-wg.env

The tradeoff: secrets briefly hit disk. Fine on a personal machine,
worth thinking about on shared hosts.

Operations:

    docker logs -f met-to-wg            # tail JSON logs
    docker stats met-to-wg              # live CPU/memory
    docker stop met-to-wg               # graceful stop (SIGTERM)
    docker inspect met-to-wg

Inspect the SQLite DB from the host:

    docker run --rm -v met-to-wg-data:/data keinos/sqlite3 \
      sqlite3 /data/observations.sqlite \
      "SELECT location, COUNT(*), MAX(datetime) FROM observation GROUP BY location;"

A healthy first log line shows the loaded stations and the
healthcheck status — e.g.
`{"msg":"met-to-wg starting","stations":["csopak","fured","almadi"],"healthcheck_enabled":true}`.
The happy path through a tick is silent; only errors are logged. CPU
should spike briefly once per `INTERVAL` and otherwise sit at zero.

A note on volume permissions: the container runs as UID 65534 (no
root, no shell). The image bakes in `/data` with the right ownership
so a fresh named volume inherits it on first mount. If you bind-mount
a host directory instead of using a named volume, `chown 65534:65534`
the directory yourself first — Docker does not adjust bind-mount
ownership.

### CI: image builds on every push

`.github/workflows/docker.yml` builds and publishes the image to
GitHub Container Registry on every push to `main` and every tag
matching `v*`. Pull requests build for verification but do not push.

- **Registry:** `ghcr.io/<owner>/<repo>` — uses the built-in
  `GITHUB_TOKEN`; no secrets to configure.
- **Platforms:** `linux/amd64` and `linux/arm64` (QEMU + buildx).
- **Tags** (via `docker/metadata-action`):
  - branch name (`main` push → `:main`)
  - short commit SHA (`:sha-abc1234`) on every build
  - semver from git tags (`v1.2.3` → `:1.2.3`, `:1.2`, `:1`)
  - `:latest` on the default branch
- **Cache:** GHA cache for buildx layers — incremental rebuilds are
  fast.

First-time setup: after the workflow's first successful push, the
package appears under the repo's *Packages* tab as private by default.
Make it public (or grant pull rights to whatever pulls it) via
*Package settings → Change visibility*.

Pull and run a specific tag:

    docker pull ghcr.io/basilar/met-to-wg:main
    docker run --rm ghcr.io/basilar/met-to-wg:main      # smoke test, fails on missing env

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
