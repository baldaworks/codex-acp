# codex-acp command

Install the canonical Go executable:

```bash
go install github.com/baldaworks/codex-acp/cmd/codex-acp@latest
codex-acp version
codex-acp --defer-backend
```

Use `@v1.9.3` for a pinned migration release. The command delegates to the
public `github.com/baldaworks/codex-acp/pkg/cobracmd` package and preserves
stdio, context cancellation and nonzero failure exit status.

See [installation](../../README.md), [usage and options](../../docs/usage.md),
[legacy compatibility](../../docs/migration.md) and [releases](../../docs/releasing.md).
