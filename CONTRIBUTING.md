# Contributing to Loop

Thanks for contributing! This page covers the local dev setup and the checks a change must pass.

## Backend (Go)

Requires [Go 1.27+](https://go.dev/dl/) and Docker (several make targets run inside containers).

```sh
make build            # Build the loop binary (runs go generate first)
make test             # Run all unit tests (-race)
make coverage-check   # Enforce 100% test coverage — CI fails below 100%
make lint             # golangci-lint via Docker (auto-fixes what it can)
make restart          # Reinstall + restart the local daemon
```

Conventions:

- **100% test coverage is enforced** (`make coverage-check`). New code ships with tests; prefer `testify` suites with `require` and table tests.
- **No global-variable mocking** — inject dependencies through struct fields, function parameters, or interfaces.
- Run `make lint` before pushing; CI runs the same linter.

## Frontend (Electron app under `app/`)

Requires [Node.js 24+](https://nodejs.org/).

```sh
cd app && npm install
npm run dev           # Vite + Electron (expects a running daemon on :8222)
npm run typecheck     # tsc --noEmit
npm test              # vitest unit tests (or: make app-test from the repo root)
```

- The renderer talks to a local daemon — start one with `loop serve` (foreground) or `loop daemon:start` before `npm run dev`.
- **Headless/Linux environments:** `LOOP_NO_ELECTRON=1 npm run dev` skips the Electron plugins and serves a plain browser app on `:5173` — useful in containers where Electron can't launch. `make app-dev-docker` wraps the same thing in Docker.
- Pure logic (parsers, ordering, path matching) lives in plain `.ts` modules with vitest tests next to them — extract before testing rather than testing through the React tree.

## Component / BDD tests

```sh
make test-component-bdd    # Backend + frontend BDD suites (Docker on host, native in CI)
```

Frontend scenarios live in `test/component/features/frontend/*.feature` and drive a real headless Chrome against the built app. Filter with `GODOG_TAGS`, e.g. `GODOG_TAGS=@gate-approval`.

## Pull requests

- Keep commits focused; use conventional-commit prefixes (`fix:`, `feat:`, `docs:`, `chore:`, `ci:`, `refactor:`, scope optional like `fix(container):`).
- A PR must pass CI: lint, `coverage-check` (100%), cross-platform builds, frontend typecheck, and the component suites.
- `README.md` is the source for `internal/readme/README.md` — after editing it, run `go generate ./internal/readme/` (or `make build`, which does it) so the embedded copy stays in sync.
- Releases are calendar-tagged (`vYYYY.M.N`) by the maintainer; don't bump versions in PRs.
