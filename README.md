# codex-acp

[![npm version](https://img.shields.io/npm/v/codex-acp)](https://www.npmjs.com/package/codex-acp)
[![test](https://github.com/baldaworks/codex-acp/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/baldaworks/codex-acp/actions/workflows/test.yml)
[![lint](https://github.com/baldaworks/codex-acp/actions/workflows/lint.yml/badge.svg?branch=main)](https://github.com/baldaworks/codex-acp/actions/workflows/lint.yml)
[![security](https://github.com/baldaworks/codex-acp/actions/workflows/security.yml/badge.svg?branch=main)](https://github.com/baldaworks/codex-acp/actions/workflows/security.yml)

**Bring Codex to your ACP-compatible editor or client.**

Use your existing Codex login to chat, work with files, and connect tools through
[Agent Client Protocol](https://agentclientprotocol.com/). Choose your model and
reasoning effort from your client's session controls.

## Features

- **Single Go binary.** About **4 MiB**: published v1.9.3 binaries are
  3.8–4.2 MiB across platforms (3.9 MiB on Linux x64). The standalone binary
  needs no Node.js, Python, or shared-library runtime; Codex CLI is required.
- **Your Codex account.** Sign in with the native Codex login flow.
- **Models and reasoning controls.** Select available models and supported
  reasoning effort levels without restarting the agent.
- **Resumable sessions.** List and resume Codex sessions, keep multiple sessions
  open, and cancel a running request.
- **Images and file references.** Send text, images, and local file links from
  clients that support them.
- **Connected tools.** Attach MCP servers to a session and handle tool approval
  requests in clients that support permissions.
- **Streaming.** Receive reasoning updates and optionally stream response text.

## Get started

You need the Codex CLI on `PATH`, a Codex account with access, and an
ACP-compatible client. Install and sign in:

```bash
npm install -g codex-acp@latest
codex-acp login
```

Configure your ACP client to launch this command:

```bash
codex-acp --defer-backend
```

If your client has separate command and arguments fields, set **command** to
`codex-acp` and **arguments** to `["--defer-backend"]`. The client starts the
agent and provides the chat interface.

Prefer to skip the global install? Configure the client to launch:

```bash
npx -y codex-acp@latest --defer-backend
```

Prefer the standalone binary? Download an archive from
[Releases](https://github.com/baldaworks/codex-acp/releases), extract it, and put
`codex-acp` (or `codex-acp.exe` on Windows) on `PATH`. No npm or Go installation
is needed for the standalone binary.

## Try it in your terminal

[acpchat](https://github.com/baldaworks/acpchat) provides an interactive client:

```bash
npx -y @baldaworks/acpchat -- npx -y codex-acp@latest
```

## Platforms

| Platform | Architectures |
| --- | --- |
| macOS | Apple Silicon (arm64), Intel (x64) |
| Linux | arm64, x64 |
| Windows | x64 |

npm selects the native binary for your platform automatically.

## Performance

A measured Linux x64 run with **100 idle Codex sessions** used **13.2 MiB** of
RSS for `codex-acp`, plus **1.1 GiB** for the Codex backend process tree.
The local test backend completed an ACP prompt round-trip in **0.53 ms**;
a real Codex prompt took **5.31 s**, including model generation.

These are measurements from one machine, not performance guarantees.
See [benchmarks and methodology](docs/benchmarks.md) for session creation,
per-session memory growth, environment details and reproduction commands.

## Documentation and support

- [Usage and configuration](docs/usage.md)
- [Protocol reference](docs/json-api.md)
- [Go installation and embedding](docs/development.md)
- [Migration from codex-acp-bridge](docs/migration.md)
- [Releases](https://github.com/baldaworks/codex-acp/releases)
- [Report an issue](https://github.com/baldaworks/codex-acp/issues)

Originally developed as
[normahq/codex-acp-bridge](https://github.com/normahq/codex-acp-bridge).
[MIT licensed](LICENSE).
