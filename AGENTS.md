# codex-acp — AGENTS.md

## Development Standards

- Follow idiomatic Go and Google Go best practices.
- Prefer project-local tooling via `go tool ...` when available.
- Use Conventional Commits for all commits.
- Sync shared branches with merge (`git pull --no-rebase`), not rebase.

## Quality Gates (Required)

Run before submitting changes:

```bash
go test -race ./...
go tool golangci-lint run
```

## Logging Policy

- Allowed: `github.com/rs/zerolog`, `log/slog`.
- Disallowed: `logrus`, `zap`, direct standard `log` usage.
- Initialize logging through `internal/logging.Init()`.
- Prefer structured logging fields over formatted strings.

## Bridge Guardrails

- Keep ACP contract compatibility stable.
- Keep strict validation for `session/new._meta.codex`.
- Keep model handling ACP-native (`session/set_config_option` with config ID `model`), keep legacy `session/set_model` compatibility, and do not add bridge-specific model CLI flags.
- Keep MCP transport constraints aligned with implementation (`stdio` and `http`, reject `sse`).

## Documentation

- Product/usage docs are rooted in `README.md`.
- Protocol details are in:
  - `docs/usage.md`
  - `docs/json-api.md`

## Release and Migration

- This repository is the canonical implementation and Go module, `github.com/baldaworks/codex-acp`.
- Use normal root-module `vX.Y.Z` tags. `scripts/release-version.sh` requires the tag to resolve to HEAD; Omnidist reads `OMNIDIST_VERSION` for all seven npm packages and the binary ldflags.
- Omnidist profile is authoritative for `codex-acp`, legacy `@normahq/codex-acp-bridge` and five shared `@baldaworks/codex-acp-*` packages. Run `node scripts/npm-legacy-bin.cjs` after npm stage and before verify/pack.
- npm publishing uses `.github/workflows/omnidist-release.yml` and OIDC. All seven packages require trusted publishers. Bootstrap or administration may require npm CLI web 2FA; never log credentials.
- The legacy repository preserves its original Go module path, public API/CLI adapters and historical releases. Its hourly/manual workflow follows canonical published releases with a pinned dependency and own-repository token. It does not publish npm or maintain a copied bridge implementation.
- Preserve existing published tags, npm versions and archive assets. Resume only missing exact release units after a partial publish; never overwrite accepted artifacts.
- Update all affected docs in both repositories when migration behavior changes. See `docs/migration.md` and `docs/releasing.md`.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --no-rebase
   bd dolt push
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->
