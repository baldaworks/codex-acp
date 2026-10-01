package cobracmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	codexacpbridge "github.com/baldaworks/codex-acp/internal/apps/codexacpbridge"
	"github.com/baldaworks/codex-acp/internal/logging"
	appversion "github.com/baldaworks/codex-acp/internal/version"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestCommandUsesBridgeComponentLogger(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	origLogger := log.Logger
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
		log.Logger = origLogger
	})

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}
	runProxy = func(ctx context.Context, _ string, _ codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		logging.Ctx(ctx).Info().Msg("probe")
		return nil
	}

	var logs bytes.Buffer
	log.Logger = zerolog.New(&logs).Level(zerolog.DebugLevel)

	cmd := New()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := logs.String()
	if !strings.Contains(got, `"component":"codex.acp.bridge"`) {
		t.Fatalf("logs missing bridge component: %q", got)
	}
	if strings.Contains(got, `"component":"codex.acp.proxy"`) {
		t.Fatalf("logs contain old proxy component: %q", got)
	}
}

func TestCommandExposesBridgeFlags(t *testing.T) {
	cmd := New()
	for _, removedFlag := range []string{
		"codex-sandbox",
		"codex-approval-policy",
		"codex-profile",
		"codex-base-instructions",
		"codex-developer-instructions",
		"codex-compact-prompt",
		"codex-config",
	} {
		if got := cmd.Flags().Lookup(removedFlag); got != nil {
			t.Fatalf("flag %q unexpectedly present", removedFlag)
		}
	}
	for _, expectedFlag := range []string{"name", "defer-backend", "message-streaming", "reasoning-streaming", "reasoning-thoughts", "reasoning-summary", "codex-args", "mcp-approval-policy", "sandbox", "debug"} {
		if got := cmd.Flags().Lookup(expectedFlag); got == nil {
			t.Fatalf("flag %q missing", expectedFlag)
		}
	}
	if got := cmd.Commands(); len(got) == 0 {
		t.Fatal("commands = 0, want version subcommand")
	}
}

func TestCommandPassesDeferBackendToRunProxy(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
	})

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}

	var gotOpts codexacpbridge.Options
	runProxy = func(_ context.Context, _ string, opts codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		gotOpts = opts
		return nil
	}

	cmd := New()
	cmd.SetArgs([]string{"--defer-backend"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !gotOpts.DeferBackend {
		t.Fatal("DeferBackend = false, want true")
	}
}

func TestCommandPassesCodexArgsAndSandboxToRunProxy(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
	})

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}

	var gotOpts codexacpbridge.Options
	runProxy = func(_ context.Context, _ string, opts codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		gotOpts = opts
		return nil
	}

	cmd := New()
	cmd.SetArgs([]string{"--sandbox=danger-full-access", "--codex-args=--search", "--mcp-approval-policy=allow"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(gotOpts.CodexArgs) != 1 {
		t.Fatalf("CodexArgs len = %d, want 1", len(gotOpts.CodexArgs))
	}
	if gotOpts.CodexArgs[0] != "--search" {
		t.Fatalf("CodexArgs[0] = %q, want %q", gotOpts.CodexArgs[0], "--search")
	}
	if gotOpts.Sandbox != "danger-full-access" {
		t.Fatalf("Sandbox = %q, want %q", gotOpts.Sandbox, "danger-full-access")
	}
	if gotOpts.MCPApprovalPolicy != codexacpbridge.MCPApprovalPolicyAllow {
		t.Fatalf("MCPApprovalPolicy = %q, want %q", gotOpts.MCPApprovalPolicy, "allow")
	}
}

func TestVersionCommandPrintsBuildVersion(t *testing.T) {
	cmd := New()
	var stdout bytes.Buffer
	cmd.SetArgs([]string{"version"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := stdout.String(); got != appversion.String()+"\n" {
		t.Fatalf("stdout = %q, want %q", got, appversion.String()+"\n")
	}
}

func TestLoginCommandDelegatesStreamsAndContext(t *testing.T) {
	type ctxKey string
	const key ctxKey = "login-test"

	stdin := strings.NewReader("input")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var gotContextValue string
	var gotStdin io.Reader
	var gotStdout io.Writer
	var gotStderr io.Writer

	cmd := loginCommand(func(ctx context.Context, in io.Reader, out, errOut io.Writer) error {
		gotContextValue, _ = ctx.Value(key).(string)
		gotStdin = in
		gotStdout = out
		gotStderr = errOut
		return nil
	})
	cmd.SetIn(stdin)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	ctx := context.WithValue(context.Background(), key, "propagated")
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if gotContextValue != "propagated" {
		t.Fatalf("login context value = %q, want propagated", gotContextValue)
	}
	if gotStdin != stdin {
		t.Fatal("login stdin was not passed through")
	}
	if gotStdout != &stdout {
		t.Fatal("login stdout was not passed through")
	}
	if gotStderr != &stderr {
		t.Fatal("login stderr was not passed through")
	}
}

func TestLoginCommandWrapsRunnerError(t *testing.T) {
	wantErr := errors.New("login unavailable")
	cmd := loginCommand(func(context.Context, io.Reader, io.Writer, io.Writer) error {
		return wantErr
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want wrapped %v", err, wantErr)
	}
	if got := err.Error(); !strings.Contains(got, "codex login") {
		t.Fatalf("Execute() error = %q, want login context", got)
	}
}

func TestLoginCommandReportsMissingCodex(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	cmd := loginCommand(runCodexLogin)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Execute() error = %v, want wrapped exec.ErrNotFound", err)
	}
	if got := err.Error(); !strings.Contains(got, "codex login") {
		t.Fatalf("Execute() error = %q, want login context", got)
	}
}

func TestLoginCommandPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := loginCommand(func(ctx context.Context, _ io.Reader, _, _ io.Writer) error {
		return ctx.Err()
	})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteContext() error = %v, want context canceled", err)
	}
}

func TestCommandPassesExecuteContextToRunProxy(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
	})

	type ctxKey string
	const key ctxKey = "signal-test"

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}

	var gotValue string
	runProxy = func(ctx context.Context, _ string, _ codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		gotValue, _ = ctx.Value(key).(string)
		return nil
	}

	cmd := New()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	ctx := context.WithValue(context.Background(), key, "propagated")
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if gotValue != "propagated" {
		t.Fatalf("runProxy context value = %q, want %q", gotValue, "propagated")
	}
}

func TestCommandPassesReasoningFlagsToRunProxy(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
	})

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}

	var gotOpts codexacpbridge.Options
	runProxy = func(_ context.Context, _ string, opts codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		gotOpts = opts
		return nil
	}

	cmd := New()
	cmd.SetArgs([]string{"--reasoning-thoughts=both", "--reasoning-summary=detailed", "--reasoning-streaming=false"})
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotOpts.ReasoningThoughts != "both" {
		t.Fatalf("ReasoningThoughts = %q, want both", gotOpts.ReasoningThoughts)
	}
	if gotOpts.ReasoningSummary != "detailed" {
		t.Fatalf("ReasoningSummary = %q, want detailed", gotOpts.ReasoningSummary)
	}
	if gotOpts.ReasoningStreaming {
		t.Fatal("ReasoningStreaming = true, want false")
	}
}
