# No-setup change report

Status: complete.

## Commits
- Pending in this worktree (see `git log -1` after commit).

## What changed
- `internal/config/template.go`: `EnsureFile(path, Paths)` writes an annotated
  YAML template atomically (0600, new parent 0700) when the file is missing;
  existing files are untouched. Template has version 1, effective listen and
  database, `time.Local.String()`, retention 90, empty auth, empty
  `upstreams`/`gateway_keys`, and comments for adding the first upstream/key.
- `main.go`: calls `EnsureFile` after `ResolvePaths` and before `OpenStore`,
  logs creation, keeps `ensureSessionSecret`; removed the `init` subcommand and
  its helpers. Startup status now says "not configured" instead of "setup
  required".
- API: deleted `setup.go`/`setup_test.go`; moved `slugify` to `ids.go` and
  `validSetupURL` to `validUpstreamURL` in `url.go`. Removed `POST /api/setup`,
  `needsSetup`, `requireConfigSetup` and all pre-setup 409 gates. Zero-upstream
  config writes are allowed; first upstream/key creation is covered by tests.
- Gateway: 503 code renamed from `setup_required` to `not_configured`, message
  "no upstream configured"; tests updated.
- SPA routing: removed `/setup` from the backend allowlist and React app
  (guard, route, `Setup.tsx`, types, client method, auth helpers, CSS).
- Docs: README and the 2026-10-31 spec updated for first-run template, no
  wizard/init, `not_configured`, and Docker YAML mounting.

## Verification
- `grep ...` for the requested patterns: no hits in Go/TS/TSX/README/current
  spec. Remaining hits are only in superseded historical specs
  (`2026-10-29-llmmeter-v2-design.md`, `2026-10-30-llmmeter-ui-redesign-design.md`)
  and historical plans, which were intentionally not rewritten.
- `go vet ./...`: pass.
- `go test ./...`: pass (all packages).
- `cd web && npm test`: 10 files, 64 tests pass.
- `make build`: pass (`tsc -b`, Vite build, `go build`).

## Concerns
- `ensureSessionSecret` runs immediately after first-start template creation and
  rewrites the file through the normal serializer, so the template's inline
  comments are replaced by the standard two-line header on the very first
  start. The template itself is annotated as required, but it is not preserved.
- Historical specs under `docs/superpowers/specs/` still mention the old setup
  flow; they are superseded reference material and were left intact.
