# Contributing to Micelio

Thanks for helping. This page covers building, testing and releasing. For how
the code is organised, see [Architecture](docs/ARCHITECTURE.md).

## Prerequisites

- Go 1.26+
- Node.js 20+ and npm, for the dashboard
- Chrome or Chromium, only for JS rendering and Universe thumbnails
- `golangci-lint`, only for `make lint`

## Build

```bash
# Full build: dashboard + Go binary (writes ./micelio)
make build-with-dashboard

# Go binary only (needs a built dashboard in cmd/micelio/dashboard/dist/)
make build

# Dashboard only (builds it and copies it into cmd/micelio/dashboard/dist/)
make build-dashboard

# Install to $GOPATH/bin (also updates /Applications/Micelio.app if present)
make install

# Copy the binary to $GOPATH/bin and the app bundle
make sync
```

A source build embeds its source directory, so `micelio build` can rebuild and
reinstall it later (`--skip-dashboard` for the Go binary only).

For dashboard work, run the Go backend and the Vite dev server separately. The
dev server proxies `/api` and `/ws` to port 3000 (`dashboard/vite.config.ts`):

```bash
go run ./cmd/micelio ui --port 3000
npm --prefix dashboard run dev
```

## Test

```bash
go test ./...                       # all Go tests (same as make test)
go test -race -count=1 ./...        # what CI runs
make bench-short                    # quick benchmarks
make bench                          # full benchmarks (3 runs)
make lint                           # golangci-lint
npm --prefix dashboard run build    # the dashboard must build
npm --prefix dashboard run test:ui  # Playwright UI tests
./scripts/check-secrets.sh          # no tokens or webhook URLs committed
```

`make bench-cpu` and `make bench-mem` write `cpu.prof` and `mem.prof` for
`go tool pprof`.

**Tests must pass locally before you open a pull request.** CI is not run on
pull requests, so a broken test would only show up after the merge.

## Pull requests

- Keep each pull request to one change.
- Add or update tests for behaviour changes.
- Update the [User Guide](docs/USER-GUIDE.md), the
  [CLI Reference](docs/CLI-REFERENCE.md) and the [Changelog](CHANGELOG.md) when
  users will see the change.
- Never commit secrets. Keep them in `~/.micelio/micelio.env` (see the User
  Guide).

## Release

Releases are built by the **Release** GitHub workflow when a `v*` tag is pushed.
It cross-compiles the binaries and publishes them with a `checksums.txt`. The
version shown in the app and used by the in-app updater comes from the git tag.

To build the same binaries locally:

```bash
make release
# Output: dist/micelio-{linux,darwin,windows}-{amd64,arm64} and dist/checksums.txt
```

## macOS app

Micelio can be packaged as a macOS `.app` that starts the dashboard.

```bash
make app            # builds dashboard + binary, output: dist/Micelio.app
open dist/Micelio.app
cp -r dist/Micelio.app /Applications/
```

The app is unsigned. On first launch, open **System Settings > Privacy &
Security** and click **Open Anyway**, or run
`xattr -d com.apple.quarantine dist/Micelio.app`. `make clean-app` removes the
bundle.
