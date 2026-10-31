# No-setup change report

Status: complete.

## Commits
- `ae44f2b` feat: generate config on first start and remove setup flow
- `72d2f5b` fix: embed generated session secret in first-start template
- Fix-round commit for absolute DB paths, docs, and dead-code cleanup.

## What changed
- `internal/config/template.go`: `EnsureFile(path, Paths)` writes an annotated
  YAML template atomically (0600, new parent 0700) when the file is missing;
  existing files are untouched. Template has version 1, effective listen and
  database, `time.Local.String()`, retention 90, an empty admin hash but a
  generated session secret, empty `upstreams`/`gateway_keys`, and comments for
  adding the first upstream/key in YAML.
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

## Follow-up
- Fixed the first-start comment loss: `EnsureFile` now generates the session
  secret itself (`secure.RandomSecret`, base64) and embeds it in the template,
  so `ensureSessionSecret` is a no-op on the freshly written file and the
  annotated comments survive. Updated the template test to assert a 32-byte
  secret and that `OpenStore` does not rewrite the file, plus the spec lines
  that described an empty template secret.
- Fix round 2: `ResolvePaths` now makes `Paths.Database` absolute (with a test
  covering relative `-db`, template persistence, and restart resolution).
  Removed the remaining false WebUI claims for adding upstreams/keys from the
  template and README; Docker docs now pin the DB to `/data/llmmeter.db` and
  stay loopback-only until an in-app password setup exists. Removed the unused
  `RequireLoopback` middleware, leftover `.key-box` CSS, and corrected the
  template's listen comment.

## Concerns
- Historical specs under `docs/superpowers/specs/` still mention the old setup
  flow; they are superseded reference material and were left intact.
- The Docker example uses `network_mode: host` (Linux) to keep the container
  loopback-only; macOS/Windows would need a different networking arrangement.
