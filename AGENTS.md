# Repository Guidelines

## Project Structure & Module Organization

The root Go module contains the server entry point in `cmd/server`, reusable application and infrastructure code in `internal/`, database migrations in `migrations/`, and integration fixtures in `testdata/`. The `cli/` and `client/` directories are independent Go modules with their own tests. The Vue/Vite web application lives in `frontend/src`; browser assets and packaging files are adjacent to it. Deployment material is under `docker/`, `deploy/`, and `helm/`. Read `docs/README.md` and `docs/TEAM_DEVELOPMENT_TASKS.md` before changing LingDoc contracts or task boundaries.

## Build, Test, and Development Commands

- `make build` builds the server binary; `make run` builds and starts it.
- `make test` runs all root Go tests (`go test -v ./...`). For a focused change, run `go test ./internal/<package>/...`.
- `make fmt` and `make lint` apply Go formatting and run `golangci-lint`.
- `cd frontend && npm ci` installs locked dependencies; `npm run dev` starts Vite, while `npm run build`, `npm run type-check`, and `npm test` build, type-check, and run frontend tests.
- `make dev-start`, `make dev-app`, and `make dev-frontend` start dependencies, the local backend, and the local frontend. Use `make check-env` before Docker-based work.

## Development gates

- Every Spec-backed change follows [`docs/agents/development-rules.md`](docs/agents/development-rules.md). Before code, create the acceptance matrix, lock scope and dependencies, and record the domain/transaction invariants.
- Before requesting review, every required matrix row must have evidence from the current head: implementation seam, success/failure/permission/recovery coverage, and exact validation commands and results. Keep the PR Draft until this gate is complete.
- Cross-module, persistence, authorization, concurrency, or audit changes must prove the real application/HTTP seam and transaction behavior; a direct store test or a green generic CI job is not sufficient by itself.
- PR descriptions must identify the current head and distinguish PASS, NOT RUN, and environment limits. Do not reuse evidence from an older head.

## Coding Style & Naming Conventions

Run `gofmt` (or `make fmt`) on Go changes. Follow idiomatic Go naming: exported identifiers use `PascalCase`, local values use `camelCase`, and package names are short lowercase words. Keep Vue/TypeScript components in `PascalCase.vue`, composables in `useX.ts`, and tests next to the code they exercise. Keep API and migration changes synchronized with the applicable files in `docs/08-本轮实施方案/`.

## Testing Guidelines

Name Go tests `TestXxx` and frontend tests with the existing `.test.ts`/`.test.js` convention. Add focused unit or contract tests for changed behavior, including failure paths. Before opening a PR, run relevant package tests plus `go test -race ./...` when practical, and record exact commands and PASS/FAIL/NOT RUN status.

## Commit & Pull Request Guidelines

Use Conventional Commit prefixes such as `feat:`, `fix:`, `test:`, `docs:`, `chore:`, or `ci:`; include the LingDoc task ID when relevant (for example, `feat(T03): ...`). PR titles follow the same convention. Fill in `.github/pull_request_template.md` with the user-visible result, related task/issue, contract impact, validation evidence, risks, and screenshots for UI changes. Never commit secrets; derive local configuration from `.env.example`.

## Agent skills

### Development gates

Use [`lingdoc-development-gates`](.agents/skills/lingdoc-development-gates/SKILL.md) for Spec-backed implementation, review preparation, and Draft/Ready decisions. It applies the two-gate workflow defined in [`docs/agents/development-rules.md`](docs/agents/development-rules.md).

### Issue tracker

Issues and specs live as GitHub issues on `magician336/lingdoc`, created and read with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical triage roles keep their default strings (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` at the repo root alongside the existing `docs/adr/`. See `docs/agents/domain.md`.
