# Usage

This command runs the Codex bridge backend and exposes it as an ACP agent over stdio.

Command:

```bash
npx -y codex-acp@latest
# or when installed globally:
codex-acp
```

Install with Go if preferred:

```bash
go install github.com/baldaworks/codex-acp/cmd/codex-acp@latest
```

Legacy entrypoints remain available:

```bash
npx -y @normahq/codex-acp-bridge@latest
go install github.com/normahq/codex-acp-bridge/cmd/codex-acp-bridge@latest
codex-acp-bridge --defer-backend
```

Both entrypoints use the canonical implementation and flags. Protocol metadata
keeps its historical `codex-acp-bridge/*` names. See [migration](migration.md)
for Go API adapters, version synchronization and preserved historical releases.

## ACP client setup and authentication

Install globally with `npm install -g codex-acp@latest`, then run `codex-acp login`
to delegate authentication to native `codex login`. An authenticated Codex session
with account access is required on the machine running the agent.

Configure the client command as `codex-acp` and arguments as `["--defer-backend"]`.
This permits ACP discovery and terminal login before starting the backend. The
client launches the command over stdio; running it alone does not open a chat UI.
Codex CLI remains required for backend-dependent operations.

For a pinned Go install, use `@v1.9.3` instead of `@latest`, and place `GOBIN`
(or `$(go env GOPATH)/bin`) on `PATH`. See [Go embedding](development.md).

## Why this exists

- ACP runners need a stable ACP endpoint.
- `codex-acp` provides a stable command name for Codex ACP integration.
- The bridge uses Codex app-server backend runtime semantics.

## Usage

```bash
# Start bridge with defaults
codex-acp

# Set ACP agent name seen by ACP clients in initialize.agentInfo.name
codex-acp --name team-codex

# Stream app-server agent messages live
codex-acp --message-streaming

# Disable live reasoning token streaming; completed summary parts still emit incremental thoughts
codex-acp --reasoning-streaming=false
```

## ACP Tooling Examples

Use `acpdump` to inspect ACP initialize/session behavior:

```bash
npx -y @baldaworks/acpdump -- npx -y codex-acp@latest
npx -y @baldaworks/acpdump --json -- npx -y codex-acp@latest
```

Use `acpchat` for an interactive ACP prompt session:

```bash
npx -y @baldaworks/acpchat -- npx -y codex-acp@latest
```

If tools are installed globally:

```bash
acpdump -- codex-acp
acpchat -- codex-acp
```

## Flags

- `--defer-backend`:
  Defer Codex backend validation until a session operation. ACP initialization
  succeeds without Codex; backend-dependent requests return an error if it is
  unavailable. Default: `false`.

- `--name`:
  ACP agent name reported in `initialize.agentInfo.name`.
  Default: `codex-acp`.
- `--message-streaming`:
  Stream app-server `item/agentMessage/delta` notifications as ACP `agent_message_chunk` updates.
  Default: `false`.
- `--reasoning-streaming`:
  Stream app-server reasoning text deltas live when enabled. When disabled, suppress raw/content token deltas and summary token deltas, but keep incremental summary-part thought publication and final completion fallback.
  Default: `true`.
- `--reasoning-summary`:
  Request app-server reasoning summaries: `auto`, `concise`, `detailed`, or `none`.
  Default: `auto`. This controls the `summary` field sent to `turn/start` and persisted with `thread/settings/update`; it does not select which lane is projected into ACP.
- `--reasoning-thoughts`:
  Select which reasoning lane is projected as ACP thoughts: `off`, `summary`, `content`, or `both`.
  Default: `summary`. If app-server completes a reasoning item with no summary text but with raw content, summary mode emits that completed content as a fallback thought.
- `--mcp-approval-policy`:
  Process-wide policy for MCP tool-call approval forms: `ask`, `allow`, or `deny`.
  Default: `ask`. This policy is independent of `--sandbox`: it controls only MCP
  tool-call approval elicitation, not Codex filesystem or network sandboxing.
  `allow` accepts without an ACP permission request, `deny` declines, and `ask`
  offers ACP permission choices, including advertised persistence scopes.
  Ordinary MCP `form` and `url` elicitation is forwarded only when the ACP client
  advertises that capability. The v1 bridge cancels `openai/form` elicitation
  with a diagnostic.
- `--codex-args`:
  Repeatable additional global Codex argument inserted before the `app-server`
  subcommand.
- `--sandbox`:
  Codex sandbox mode: `read-only`, `workspace-write`, or `danger-full-access`.
  The bridge runs `codex --sandbox=<mode> app-server` and applies the same mode
  as the default `thread/start.sandbox` and `thread/resume.sandbox` value.
  Per-session `_meta.codex.sandbox` overrides this default.
- `--debug`:
  Enable debug logging for the bridge process.

## Supported operations

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

For request and response examples, see the [JSON API reference](json-api.md).

## Behavior

- Starts one Codex app-server backend for the bridge lifetime. Independent ACP
  sessions share that process and remain isolated as separate Codex threads.
- Applies ACP `session/new.params.cwd` per thread through `thread/start` and
  `thread/resume`; otherwise the bridge process working directory is used.
- Negotiates app-server notification opt-outs during `initialize`:
  - `--message-streaming=false` opts out `item/agentMessage/delta`.
  - `--reasoning-streaming=false` opts out `item/reasoning/textDelta` only; summary notifications stay enabled when summary thoughts are enabled so completed summary parts can publish incrementally.
  - `--reasoning-thoughts=summary` opts out raw `item/reasoning/textDelta`.
  - `--reasoning-thoughts=content` opts out `item/reasoning/summaryTextDelta` and `item/reasoning/summaryPartAdded`.
  - `--reasoning-thoughts=off` opts out all reasoning delta notifications.
- Sends `turn/start.summary` using `--reasoning-summary` for each prompt. When a thread already exists and model or reasoning effort changes, persists the same summary mode through `thread/settings/update.summary`.
- Opens ACP agent-side stdio connection for clients.
- Creates one Codex thread per ACP session on the shared backend.
- `session/new` returns the app-server thread id (`thread.id`) as the ACP `sessionId`.
- Supports ACP `session/list` using backend `thread/list`.
  - `session/list` returns resumable Codex threads, not just sessions created by the current bridge process.
  - `session/list` maps ACP `cwd` filtering directly to backend `thread/list.cwd`.
- Supports ACP `session/close` using backend `thread/unsubscribe`.
  - `session/close` is a transient detach operation; it does not archive or delete the underlying Codex thread.
  - closed sessions remain resumable and listable until backend retention removes them.
- Supports ACP `session/resume` using direct app-server `thread/resume`.
  - `session/resume` restores session state only; it does not replay prior ACP message/thought/tool updates.
  - `thread.sessionId` remains a backend session-tree identifier; it is not the ACP resume handle.
  - ACP `session/load` is not implemented because the bridge does not replay prior conversation history as required by the protocol.
- Reads per-session Codex defaults from `session/new.params._meta.codex` (strictly validated).
- Supports ACP cancellation via `session/cancel`.
- Optional agent message streaming:
  - when `--message-streaming=true`, every app-server `agentMessage` item is streamed as ACP `agent_message_chunk`
  - streamed message chunks carry `_meta["codex-acp-bridge/itemId"]`, `_meta["codex-acp-bridge/completed"]`, and `_meta["codex-acp-bridge/phase"]`
  - `item/completed` closes the logical message; it does not complete the ACP turn
- Optional lane-aware reasoning thoughts:
  - `summary` projects `item/reasoning/summaryTextDelta`; with `--reasoning-streaming=false`, the bridge buffers summary text locally and publishes completed parts on `item/reasoning/summaryPartAdded`
  - `content` projects `item/reasoning/textDelta`
  - `both` projects both lanes and keeps them distinct via `_meta`
  - thought chunks carry `_meta["codex-acp-bridge/itemId"]`, `_meta["codex-acp-bridge/reasoningKind"]`, index metadata, and `_meta["codex-acp-bridge/completed"]`
- Supports per-session MCP servers via ACP `session/new` `mcpServers` parameter.
  - Supported transports: `stdio`, `http`.
  - `sse` is not supported.
  - Each `mcpServers[]` entry must define exactly one transport.
  - Bridge maps these values to `config.mcp_servers.<id>.*` in backend thread start params.
  - Merge contract: ACP `mcpServers` entries override same-name servers in `config.mcp_servers`; other configured MCP servers remain active.
  - MCP startup visibility:
    - `session/new._meta.codex.mcp` includes `contract` and requested server descriptors.
    - `session/prompt._meta.codex.mcp.startupStatus` includes latest startup status/error for requested servers.
- Supports stable `session/set_config_option` for ACP session state, with `session/set_model` and `session/set_mode` kept for compatibility.
  - `session/set_config_option` with config ID `model` accepts only model IDs advertised by app-server `model/list`, updates model selection used by subsequent `turn/start` calls, and persists it to app-server thread settings when the thread already exists.
  - `session/set_model` uses the same model validation and persistence path for older clients.
  - `session/set_mode` is stored in ACP session state only; current bridge implementation does not forward mode into backend `thread/start` or `turn/start` payload fields.
- Supports ACP session configuration options for reasoning effort.
  - `session/new.configOptions` includes a `reasoning_effort` select option when app-server `model/list` advertises reasoning efforts for the current model.
  - `session/set_config_option` with `configId=reasoning_effort` updates the effort used by subsequent `turn/start.effort` payloads and persists it to app-server thread settings when the thread already exists.
  - Supported values are model-advertised and may include values such as `minimal`, `low`, `medium`, `high`, or `xhigh`.
- Populates ACP `session/new.configOptions` with model and reasoning settings from app-server `model/list` when available.
- Also populates ACP `session/new.models` for clients that still use the legacy model state.
- Model selection is ACP-native; prefer `session/set_config_option` with config ID `model`.
- `session/prompt._meta.error` preserves raw provider/app-server terminal error details for `error(willRetry=false)` and `turn/completed(status=failed)` when the backend provides them.
- Prompt content support:
  - Text and image prompt blocks are supported (`PromptCapabilities.image=true`).
  - Baseline ACP resource links are supported. Local `file://` URIs are decoded
    into ordered text input containing the resource name, local path, and MIME
    type; the bridge does not read the referenced file.
  - Audio prompt blocks are not supported in `session/prompt` (`PromptCapabilities.audio=false`).
  - Embedded resource blocks are not supported (`PromptCapabilities.embeddedContext=false`).

## `session/new._meta.codex` Mapping

Supported keys and mappings:

- `sandbox` -> `thread/start.sandbox`
- `approvalPolicy` -> `thread/start.approvalPolicy`
- `approvalsReviewer` -> `thread/start.approvalsReviewer`
- `baseInstructions` -> `thread/start.baseInstructions`
- `developerInstructions` -> `thread/start.developerInstructions`
- `modelProvider` -> `thread/start.modelProvider`
- `personality` -> `thread/start.personality`
- `serviceTier` -> `thread/start.serviceTier`
- `ephemeral` -> `thread/start.ephemeral`
- `profile` -> `thread/start.config.profile`
- `compactPrompt` -> `thread/start.config.compact_prompt`
- `config` -> merged into `thread/start.config`

`config.model_reasoning_effort` remains available as a Codex-specific startup default. Prefer ACP `session/set_config_option` for interactive reasoning-effort changes after session creation.

Validation and precedence:

- `session/new._meta.sessionId` is rejected; ACP session ids are backend-generated and durable.
- Unknown `codex` keys are rejected with ACP `invalid_params`.
- `profile` overrides `config.profile`.
- `compactPrompt` overrides `config.compact_prompt`.
- ACP `mcpServers` mapping overrides same-name entries in `config.mcp_servers` (merge semantics; non-overlapping entries are retained).

Example `session/new` request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/new",
  "params": {
    "cwd": "/workspace",
    "_meta": {
      "codex": {
        "sandbox": "workspace-write",
        "approvalPolicy": "on-request",
        "approvalsReviewer": "user",
        "profile": "dev",
        "compactPrompt": "compact"
      }
    },
    "mcpServers": []
  }
}
```

## Exit behavior

- Returns non-zero if backend setup fails.
- Returns zero when ACP client disconnects normally.
