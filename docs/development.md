# Go installation and embedding

The canonical Go module is `github.com/baldaworks/codex-acp`.

## Install from source

```bash
go install github.com/baldaworks/codex-acp/cmd/codex-acp@latest
```

Use `@v1.9.3` to pin the migration release. Put `GOBIN`, or
`$(go env GOPATH)/bin` by default, on `PATH`. The installed executable uses the
same [configuration and flags](usage.md) as the npm and standalone versions.

## Embed the command

Import `github.com/baldaworks/codex-acp/pkg/cobracmd`. Both `New()` and `Command()`
return a `*cobra.Command` suitable for adding to another Cobra application's
root command. The caller owns command execution and context cancellation.

The legacy `github.com/normahq/codex-acp-bridge/pkg/cobracmd` package forwards to
a pinned canonical release. See [migration](migration.md) for compatibility.

## Local checks

```bash
go test -race ./...
go tool golangci-lint run
go mod verify
go tool govulncheck ./...
```

The security workflow runs on pushes, pull requests and weekly, using the pinned
Go tool from `go.mod`. It fails when the scanner reports reachable vulnerabilities.

For local and real-backend measurements, see [benchmarks](benchmarks.md).

See [protocol details](json-api.md) and [release maintenance](releasing.md).
