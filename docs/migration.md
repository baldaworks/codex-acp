# Migration to baldaworks

Release `1.9.3` moves ongoing implementation, Go API and release ownership to
`baldaworks/codex-acp`. The original `normahq/codex-acp-bridge` repository
retains its URL, Git history, tags, historical GitHub assets and a thin Go
compatibility module. Its stars and historical GitHub metadata remain there.
The original MIT license and attribution remain intact.

## Go installation and imports

```bash
# Canonical executable
go install github.com/baldaworks/codex-acp/cmd/codex-acp@v1.10.1
# Existing executable path
go install github.com/normahq/codex-acp-bridge/cmd/codex-acp-bridge@v1.10.1
```

Both paths support `@latest`. The archived legacy Go module stays at
`v1.10.1`; only the canonical path follows future releases. Go binary
installation uses `GOBIN` or the Go workspace bin directory; put it on `PATH`.

Canonical applications import `github.com/baldaworks/codex-acp/pkg/cobracmd`.
Existing applications may retain
`github.com/normahq/codex-acp-bridge/pkg/cobracmd`. Both public constructors,
`New()` and `Command()`, return the canonical `*cobra.Command`. The legacy
command package also retains its original import path. The legacy module
keeps its original `module` directive and pins the canonical dependency;
consumers need no `replace`. The old repository contains no separate bridge
runtime. Command flags and ACP behavior come from the canonical implementation.

Versions use standard root-module tags `vX.Y.Z`. Historical tags through
`v1.9.2` retain their original contents, including the old module path.
`codex-acp/vX.Y.Z` is not the root Go module release format.

## npm installation and command names

```bash
npx -y codex-acp@latest
npx -y @normahq/codex-acp-bridge@latest
npm install -g codex-acp@latest
# Or retain the old package and executable:
npm install -g @normahq/codex-acp-bridge@latest
codex-acp-bridge version
```

Both npm meta packages depend directly on the same five
`@baldaworks/codex-acp-*` native packages at the exact release version.
The legacy package exposes two `bin` entries pointing to one `codex-acp.js`
launcher; `npx` can therefore infer the executable unambiguously. That launcher
runs the canonical native binary. Installed old package versions retain their
original `@normahq/codex-acp-bridge-*` dependencies; those historical native
packages remain published. Updating the meta package selects the new packages.

Use the entrypoint appropriate for your ACP client configuration. Both current
entrypoints expose canonical identity `codex-acp` by default; set `--name` if
an integration needs a custom agent name. Historical wire metadata names
`codex-acp-bridge/*` and deterministic reasoning identifiers remain unchanged.
No changes to existing session/model/MCP transport contracts accompany migration.

## Releases and archived compatibility

`normahq/codex-acp-bridge` is archived and read-only after the final legacy
release `v1.10.1`. Its Go adapters pin canonical `v1.10.1` permanently.
The synchronization workflow has been retired; no new Go tags or GitHub
releases are published there. Use canonical Go paths for future updates.

The canonical repository owns all ongoing development, Go tags, npm publication
and native builds. The deprecated npm alias `@normahq/codex-acp-bridge` is
published from the canonical repository and continues to share current native
packages. Repository archival freezes the legacy Go module, not the npm alias.

Existing Git history, tags and GitHub archive URLs remain available. Legacy
archives through `v1.10.1` retain the `codex-acp-bridge-*` names and contain the
same binary bytes as their matching canonical releases. Published npm versions
and historical native packages also remain installable.

New issues and contributions belong in
https://github.com/baldaworks/codex-acp. See [releasing.md](releasing.md) for
canonical publication and partial-release recovery.
