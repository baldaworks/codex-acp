# codex-acp

[![test](https://github.com/baldaworks/codex-acp/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/baldaworks/codex-acp/actions/workflows/test.yml)
[![lint](https://github.com/baldaworks/codex-acp/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/baldaworks/codex-acp/actions/workflows/lint.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/baldaworks/codex-acp)](https://goreportcard.com/report/github.com/baldaworks/codex-acp)
[![coverage](https://codecov.io/gh/baldaworks/codex-acp/branch/main/graph/badge.svg)](https://codecov.io/gh/baldaworks/codex-acp)
[![npm version](https://img.shields.io/npm/v/codex-acp)](https://www.npmjs.com/package/codex-acp)

Run Codex as an ACP agent.

Fork of [normahq/codex-acp-bridge](https://github.com/normahq/codex-acp-bridge), preserving upstream release versions and the original MIT license.

`codex-acp` starts the local `codex app-server` backend and exposes it to Agent Client Protocol (ACP) clients over stdio. Use it when an ACP runner needs to talk to Codex through a stable command while keeping Codex authentication, session state, model selection, and tool behavior native to the Codex CLI.

It is not an OpenAI API proxy. It uses the authenticated Codex session on the machine where the bridge runs, so no OpenAI API key is required.

## Requirements

- `codex` CLI installed and available in `PATH`.
- Authenticated Codex session on the host running the bridge. Run `codex-acp login` or `codex login` to authenticate.
- Active Codex subscription.

## Quickstart

Run the bridge with `npx`:

```bash
npx -y codex-acp@latest
```

Inspect the ACP handshake:

```bash
npx -y @baldaworks/acpdump -- npx -y codex-acp@latest
```

Start an interactive ACP session:

```bash
npx -y @baldaworks/acpchat -- npx -y codex-acp@latest
```

## Installation

Install globally if your ACP client expects a stable executable name:

```bash
npm install -g codex-acp@latest
```

Then run:

```bash
codex-acp
```

## ACP client configuration

Configure your ACP client to launch:

```bash
codex-acp --defer-backend
```

`--defer-backend` allows ACP discovery and native Codex login before starting
`codex app-server`. Codex remains required for sessions. Use manual client
configuration for this fork; its npm package and executable are `codex-acp`.

## What The Bridge Provides

- ACP `initialize`, `session/new`, `session/prompt`, `session/cancel`, `session/list`, `session/close`, and `session/resume` backed by Codex app-server threads.
- One long-lived Codex app-server process per bridge, multiplexing independent ACP sessions as Codex threads.
- ACP terminal authentication that delegates to the native `codex login` command without handling credentials in the bridge.
- Durable ACP session IDs mapped directly to Codex app-server `thread.id` values.
- ACP-native model handling through stable `session/new.configOptions` and `session/set_config_option` for `model`, with legacy `session/new.models` and `session/set_model` kept for compatibility.
- ACP session configuration for model-advertised reasoning effort values.
- Text, image, and baseline ACP resource-link prompt blocks. Local `file://`
  resource links are forwarded to Codex as local-path attachment metadata.
- Optional streaming for Codex agent messages and reasoning thoughts.
- Per-session MCP server configuration from ACP `mcpServers`.
- Raw terminal provider/app-server failure details preserved in `session/prompt._meta.error`.
- Strict `session/new._meta.codex` validation for Codex-specific startup options.

For protocol-level details, see [docs/usage.md](https://github.com/baldaworks/codex-acp/blob/main/docs/usage.md) and [docs/json-api.md](https://github.com/baldaworks/codex-acp/blob/main/docs/json-api.md).

## Runtime Options

```bash
codex-acp [flags]
```

Common flags:

- `--name`: ACP agent name reported in `initialize.agentInfo.name`. Default: `codex-acp`.
- `--defer-backend`: allow ACP initialization before validating `codex app-server`; backend-dependent requests still start Codex and return an error if it is unavailable. Default: `false`.
- `--message-streaming`: stream Codex `agentMessage` deltas as ACP `agent_message_chunk` updates. Default: `false`.
- `--reasoning-streaming`: stream Codex reasoning text deltas live; when disabled, raw/content token deltas stay off, while summary thoughts still publish incrementally on completed summary parts. Default: `true`.
- `--reasoning-summary`: app-server reasoning summary level to request: `auto`, `concise`, `detailed`, or `none`. Default: `auto`.
- `--reasoning-thoughts`: reasoning lane projected as ACP thoughts: `off`, `summary`, `content`, or `both`. Default: `summary`; when no summary is available, completed raw content is emitted as a fallback thought.
- `--mcp-approval-policy`: process-wide policy for MCP tool-call approval prompts: `ask`, `allow`, or `deny`. Default: `ask`. It is separate from `--sandbox`: `allow` accepts the MCP tool call without an ACP permission request, `deny` declines it, and `ask` presents ACP permission options when the client supports them.

Ordinary MCP `form` and `url` elicitations are forwarded as ACP elicitations only when the ACP client advertises the corresponding capability. The v1 bridge cancels `openai/form` elicitation with a diagnostic.
- `--sandbox`: Codex sandbox mode applied both to the `codex` CLI invocation and as the default for ACP `thread/start` and `thread/resume`: `read-only`, `workspace-write`, or `danger-full-access`.
- `--codex-args`: repeatable additional global Codex argument inserted before `app-server`.
- `--debug`: enable debug logging.

Examples:

```bash
codex-acp --name team-codex
codex-acp --defer-backend
codex-acp --message-streaming
codex-acp --reasoning-thoughts=both
codex-acp --reasoning-summary=detailed
codex-acp --reasoning-streaming=false
codex-acp --mcp-approval-policy=allow
codex-acp --sandbox=workspace-write
codex-acp --debug
```

## Codex Session Metadata

Codex-specific session startup options belong under ACP `session/new.params._meta.codex`.

Supported keys include:

- `sandbox`
- `approvalPolicy`
- `approvalsReviewer`
- `baseInstructions`
- `developerInstructions`
- `modelProvider`
- `personality`
- `serviceTier`
- `ephemeral`
- `profile`
- `compactPrompt`
- `config`

Unknown keys are rejected with ACP `invalid_params`. ACP session IDs are generated by the backend; `session/new._meta.sessionId` is rejected.

Use ACP `session/set_config_option` with config ID `model` for model changes instead of bridge-specific model flags; the model ID must be one advertised by Codex `model/list`. Legacy `session/set_model` remains supported for older clients. Use ACP `mcpServers` for per-session MCP servers; supported transports are `stdio` and `http`, while `sse` is rejected.

## Distribution and releases

The npm package `codex-acp` selects one native binary package:

- `@baldaworks/codex-acp-darwin-x64`
- `@baldaworks/codex-acp-darwin-arm64`
- `@baldaworks/codex-acp-linux-x64`
- `@baldaworks/codex-acp-linux-arm64`
- `@baldaworks/codex-acp-win32-x64`

All packages use the corresponding upstream version; the initial fork baseline
is upstream `v1.9.2`. The fork does not start a separate version sequence.
Maintainer instructions for the initial npm publication with browser 2FA and
subsequent trusted publishing are in [docs/releasing.md](docs/releasing.md).

## Links

- Repository: https://github.com/baldaworks/codex-acp
- Issues: https://github.com/baldaworks/codex-acp/issues
- Releases: https://github.com/baldaworks/codex-acp/releases
- npm package: https://www.npmjs.com/package/codex-acp
