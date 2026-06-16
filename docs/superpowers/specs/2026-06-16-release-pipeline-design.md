# Release pipeline — GitHub Actions + GoReleaser

**Date:** 2026-06-16
**Status:** Approved (design)

## Goal

Provide downloadable Windows and Linux binaries on the GitHub **Releases**
page, built automatically by GitHub Actions. Pushing a `vX.Y.Z` git tag is the
only manual action; everything else (cross-compile, package, checksum,
changelog, publish) is automated. No repository secrets required — the workflow
uses the built-in `GITHUB_TOKEN`.

## Decisions

| Decision | Choice |
|----------|--------|
| Trigger | Push a `vX.Y.Z` git tag |
| Targets | linux/amd64, linux/arm64, windows/amd64 (windows/arm64 omitted) |
| Versioning | Stamp the tag into the binary via `-ldflags -X` |
| Tooling | GoReleaser (v2) via the official action |

## Files added / changed (4)

### 1. `cmd/rgdevenv/serve.go` — enable version stamping
`-ldflags -X` can only overwrite a package-level `var`, not a `const`. Change:

```go
const version = "0.1.0"   // before
var   version = "0.1.0"    // after
```

`version` is read only at runtime (`serve.go:144`, `Version: version`), so this
is a safe one-line change. The literal stays `0.1.0` as the dev/default value;
release builds overwrite it with the tag.

### 2. `.goreleaser.yaml` (new)
- `version: 2`, `project_name: rgdevenv`.
- `before.hooks: [go mod download]` — populate the module cache without
  modifying `go.mod`/`go.sum` (avoids GoReleaser's dirty-tree failure that
  `go mod tidy` could cause).
- `builds`: one build —
  - `main: ./cmd/rgdevenv`, `binary: rgdevenv`
  - `env: [CGO_ENABLED=0]`, `flags: [-trimpath]`
  - `ldflags: -s -w -X main.version={{ .Version }}` — the `version` var lives in
    package `main` (`cmd/rgdevenv`). The symbol **must** be `main.version`, not
    the full module path: `-trimpath` (set above) rewrites the main package's
    recorded import path, so a full-path `-X` silently no-ops and the binary
    keeps its `0.1.0` default. (Verified empirically against goreleaser 2.15.4.)
  - `goos: [linux, windows]`, `goarch: [amd64, arm64]`,
    `ignore: [{goos: windows, goarch: arm64}]` → exactly 3 binaries.
- `archives`: name `rgdevenv_{{.Version}}_{{.Os}}_{{.Arch}}`; `tar.gz` default,
  `zip` for windows; bundle the binary + `README.md` + `LICENSE`.
- `checksum`: single `rgdevenv_{{.Version}}_checksums.txt`, SHA-256.
- `changelog`: generated from git log, excluding `chore:`/`docs:`/`test:`.
- `release`: `draft: false`, `prerelease: auto` (pre-release tags like
  `v1.0.0-rc1` auto-flagged). Owner/repo auto-detected from the git remote.

### 3. `.github/workflows/release.yml` (new)
- Trigger: `push` on tags matching `v*`.
- `permissions: contents: write` (create the release, upload assets).
- Two jobs:
  - **test** — `checkout` → `setup-go` (`go-version-file: go.mod`) →
    `go test ./...`.
  - **release** (`needs: test`) — `checkout` (`fetch-depth: 0`; GoReleaser needs
    full history + tags) → `setup-go` (`go-version-file: go.mod`) →
    `goreleaser/goreleaser-action@v6` with `version: "~> v2"`,
    `args: release --clean`, `GITHUB_TOKEN` from secrets.
  - Two jobs (rather than one) so the release job runs on a pristine checkout,
    keeping the working tree clean for GoReleaser's dirty-state check.

### 4. `.gitignore` — add `/dist/`
GoReleaser writes build output to `dist/`.

## Release flow

```
git tag v0.2.0 && git push origin v0.2.0
  -> Actions: test -> cross-compile -> archive -> checksum -> publish
  -> Releases/v0.2.0:
       rgdevenv_0.2.0_linux_amd64.tar.gz
       rgdevenv_0.2.0_linux_arm64.tar.gz
       rgdevenv_0.2.0_windows_amd64.zip
       rgdevenv_0.2.0_checksums.txt
       + auto-generated changelog
```

Released binaries self-report `0.2.0` (via `rgdevenv status` and the daemon
`Version` field).

## Verification

- `goreleaser check` — validate config schema (install GoReleaser v2 locally).
- `goreleaser build --snapshot --clean` — real cross-compile of all 3 targets
  without publishing.
- Confirm the stamped version: build with the `-X` ldflag and assert the binary
  reports the injected value (not `0.1.0`).
- Existing `go test ./...` continues to pass after the `const`→`var` change.

## Out of scope

- macOS/darwin builds (request was Windows + Linux only).
- A separate push/PR CI workflow (test/vet/lint) — distinct from releases.
