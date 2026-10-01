# Benchmarks

These benchmarks measure complete ACP calls over stdio and resident process
memory. They reuse the integration-test client and local Codex helper; real Codex
is an explicit opt-in. Production bridge code is unchanged.

## Run

Local, without a Codex account or model requests:

```bash
go test -tags integration ./internal/apps/codexacpbridge \
  -run '^$' -bench '^BenchmarkBridgeIntegration$' -benchtime=3x -count=1 -timeout=10m
```

Include your installed, authenticated Codex backend:

```bash
CODEX_ACP_BENCH_REAL=1 go test -tags integration ./internal/apps/codexacpbridge \
  -run '^$' -bench '^BenchmarkBridgeIntegration$' -benchtime=3x -count=1 -timeout=10m
```

The real run uses your Codex configuration and sends short prompts, consuming
model quota. Sessions request `ephemeral: true`. Memory samples require Linux
`/proc`; latency benchmarks also run without the memory samples on other systems
supported by the integration harness. To measure only latency, use
`-bench 'BenchmarkBridgeIntegration/(Local|Codex)/(NewSession|PromptRoundTrip)$'`.
Use a fixed iteration count for memory (`-benchtime=3x`) for comparable samples.
Elapsed time is used only for Go benchmark calibration; memory benchmarks
suppress `ns/op` and report resident bytes instead.

## Measured snapshot

Measured October 1, 2026 on Linux x64 / WSL2, AMD Ryzen 7 PRO 7840U, four exposed
CPUs, Go 1.26.5, Codex CLI 0.159.3. Local Codex configuration selected
`gpt-6.1-sol` with `medium` reasoning effort. The command above completed
successfully with three measured iterations per benchmark. Integration race
tests ran concurrently for part of the measurement; these are illustrative
machine snapshots, not an isolated performance baseline or latency guarantee.

### Latency

| ACP operation | Local helper | Real Codex |
| --- | ---: | ---: |
| Create session | 0.91 ms | 181.64 ms |
| Prompt round-trip | 0.53 ms | 5.31 s |

Initialization and binary compilation are excluded. Session creation is measured
after starting the backend and creating/closing a warmup session; closing the
measured session is excluded from its timing. Prompt round-trip starts after
session creation and ends at successful `end_turn`, draining all ACP updates.
The prompt is “Reply with exactly OK. Do not use tools.” All prompt iterations
share one session. Real prompt time includes network, provider scheduling and
model generation; it does not isolate bridge overhead. The local helper performs
no inference, but includes its existing JSON event logging to disk.

### Memory

Mean process RSS over three fresh-process samples per row:

| Backend | Open sessions | Bridge RSS (MiB) | Backend tree RSS (MiB) | Bridge growth/session (KiB) | Backend growth/session (MiB) |
| --- | ---: | ---: | ---: | ---: | ---: |
| Local helper | 1 | 7.93 | 6.99 | 106.67 | 0.004 |
| Local helper | 10 | 8.27 | 7.18 | 38.93 | 0.008 |
| Local helper | 100 | 11.05 | 7.74 | 32.03 | 0.006 |
| Real Codex | 1 | 8.21 | 304.43 | 245.33 | 11.10 |
| Real Codex | 10 | 10.02 | 414.97 | 214.67 | 12.19 |
| Real Codex | 100 | 13.22 | 1121.95 | 54.27 | 8.26 |

Each sample starts a new bridge/backend, initializes it, creates and closes one
warmup session, waits 100 ms, then records the baseline with no open sessions.
It creates 1, 10 or 100 simultaneous idle sessions, waits another 100 ms and
records RSS. Each sample shuts down its processes before the next one starts.
Backend RSS sums all descendant processes of the bridge, excluding the bridge
and benchmark client. Children are discovered across every OS thread.

Growth/session is `(RSS with N sessions - warmed baseline RSS) / N`, averaged
across samples. It includes allocator retention, page rounding, caches and
background activity; it is not an exact session object size or a linear scaling
promise. No prompts run during memory sampling. RSS counts resident pages,
including shared pages, so summed backend RSS can count shared mappings more
than once. Negative growth is possible from process noise and is not clamped.
These measurements are not Go `B/op` allocation counts or unique physical memory.

## Reading the output

- `NewSession` and `PromptRoundTrip`: `ns/op` for completed operations; failures
  fail the benchmark instead of producing a successful measurement.
- `bridge-RSS-B` / `backend-RSS-B`: total resident bytes with the reported number
  of open sessions.
- `bridge-growth-B/session` / `backend-growth-B/session`: resident growth from
  the warmed baseline divided by session count.

Use a quiet machine and repeat runs to compare changes. Pin the Codex version,
model, reasoning settings and Go toolchain for comparisons. The local helper is
suited to tracking bridge changes; real Codex results also reflect backend and
provider behavior. [Development checks](development.md) verify the underlying
integration harness separately.
