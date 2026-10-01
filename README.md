# codex-acp-bridge

[![test](https://github.com/normahq/codex-acp-bridge/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/normahq/codex-acp-bridge/actions/workflows/test.yml)
[![lint](https://github.com/normahq/codex-acp-bridge/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/normahq/codex-acp-bridge/actions/workflows/lint.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/normahq/codex-acp-bridge)](https://goreportcard.com/report/github.com/normahq/codex-acp-bridge)
[![coverage](https://codecov.io/gh/normahq/codex-acp-bridge/branch/main/graph/badge.svg)](https://codecov.io/gh/normahq/codex-acp-bridge)
[![npm version](https://img.shields.io/npm/v/%40normahq%2Fcodex-acp-bridge)](https://www.npmjs.com/package/@normahq/codex-acp-bridge)

Run Codex as an ACP agent.

`codex-acp-bridge` starts the local `codex app-server` backend and exposes it to Agent Client Protocol (ACP) clients over stdio. Use it when an ACP runner needs to talk to Codex through a stable command while keeping Codex authentication, session state, model selection, and tool behavior native to the Codex CLI.

It is not an OpenAI API proxy. It uses the authenticated Codex session on the machine where the bridge runs, so no OpenAI API key is required.

## Requirements

- `codex` CLI installed and available in `PATH`.
- Authenticated Codex session on the host running the bridge. Run `codex-acp-bridge login` or `codex login` to authenticate.
- Active Codex subscription.

## Quickstart

Run the bridge with `npx`:

```bash
npx -y @normahq/codex-acp-bridge@latest
```

Inspect the ACP handshake:

```bash
npx -y @baldaworks/acpdump -- npx -y @normahq/codex-acp-bridge@latest
```

Start an interactive ACP session:

```bash
npx -y @baldaworks/acpchat -- npx -y @normahq/codex-acp-bridge@latest
```

## Installation

Install globally if your ACP client expects a stable executable name:

```bash
npm install -g @normahq/codex-acp-bridge@latest
```

Then run:

```bash
codex-acp-bridge
```

## Zed ACP Registry

Install **Codex ACP Bridge** from Zed's ACP Registry:

1. Run `zed: acp registry` from the Zed command palette.
2. Search for **Codex ACP Bridge** and install it.
3. Start a Codex ACP Bridge thread from the Agent Panel or Threads Sidebar.
4. If Zed prompts for authentication, choose **Log in to Codex**. The bridge runs the native `codex login` terminal flow; Codex owns the browser/device interaction and credential storage.

You can also open `agent: open settings`, go to **External Agents**, select **Add Agent**, and choose **Install from Registry**. See [Zed's External Agents documentation](https://zed.dev/docs/ai/external-agents) for the current UI flow.

The registry launch uses `--defer-backend` so Zed can complete ACP discovery and offer the native login method before starting `codex app-server`. Codex is still required for sessions: the first backend-dependent request reports a normal ACP error if `codex` is unavailable or cannot start.

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

For protocol-level details, see [docs/usage.md](https://github.com/normahq/codex-acp-bridge/blob/main/docs/usage.md) and [docs/json-api.md](https://github.com/normahq/codex-acp-bridge/blob/main/docs/json-api.md).

## Runtime Options

```bash
codex-acp-bridge [flags]
```

Common flags:

- `--name`: ACP agent name reported in `initialize.agentInfo.name`. Default: `norma-codex-acp-bridge`.
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
codex-acp-bridge --name team-codex
codex-acp-bridge --defer-backend
codex-acp-bridge --message-streaming
codex-acp-bridge --reasoning-thoughts=both
codex-acp-bridge --reasoning-summary=detailed
codex-acp-bridge --reasoning-streaming=false
codex-acp-bridge --mcp-approval-policy=allow
codex-acp-bridge --sandbox=workspace-write
codex-acp-bridge --debug
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

## Links

- Repository: https://github.com/normahq/codex-acp-bridge
- Issues: https://github.com/normahq/codex-acp-bridge/issues
- Releases: https://github.com/normahq/codex-acp-bridge/releases
- npm package: https://www.npmjs.com/package/@normahq/codex-acp-bridge
