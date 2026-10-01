package codexacp

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const (
	bridgeAppServerBinary     = "codex"
	bridgeAppServerSubcommand = "app-server"
)

type appServerSessionSpy struct {
	initializeResponse appServerInitializeResponse
	closeCalls         int
	waitCalls          int
}

type bridgeTestClient struct{}

func (bridgeTestClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) SessionUpdate(context.Context, acp.SessionNotification) error {
	return nil
}

func (bridgeTestClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.New("not implemented")
}

func (bridgeTestClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.New("not implemented")
}

var _ acp.Client = bridgeTestClient{}

func (s *appServerSessionSpy) InitializeResponse() appServerInitializeResponse {
	return s.initializeResponse
}

func (s *appServerSessionSpy) Events() <-chan appServerEvent {
	return nil
}

func (s *appServerSessionSpy) ThreadList(context.Context, map[string]any) (appServerThreadListResponse, error) {
	return appServerThreadListResponse{}, nil
}

func (s *appServerSessionSpy) ThreadUnsubscribe(context.Context, string) (appServerThreadUnsubscribeResponse, error) {
	return appServerThreadUnsubscribeResponse{}, nil
}

func (s *appServerSessionSpy) ThreadStart(context.Context, map[string]any) (appServerThreadStartResponse, error) {
	return appServerThreadStartResponse{}, nil
}

func (s *appServerSessionSpy) ThreadResume(context.Context, map[string]any) (appServerThreadResumeResponse, error) {
	return appServerThreadResumeResponse{}, nil
}

func (s *appServerSessionSpy) ThreadSettingsUpdate(context.Context, map[string]any) error {
	return nil
}

func (s *appServerSessionSpy) TurnStart(context.Context, map[string]any) (appServerTurnStartResponse, error) {
	return appServerTurnStartResponse{}, nil
}

func (s *appServerSessionSpy) ModelList(context.Context, map[string]any) (appServerModelListResponse, error) {
	return appServerModelListResponse{}, nil
}

func (s *appServerSessionSpy) TurnInterrupt(context.Context, string, string) error {
	return nil
}

func (s *appServerSessionSpy) RespondRequest(context.Context, *appServerRequest, any) error {
	return nil
}

func (s *appServerSessionSpy) RespondRequestError(context.Context, *appServerRequest, int, string, any) error {
	return nil
}

func (s *appServerSessionSpy) Close() error {
	s.closeCalls++
	return nil
}

func (s *appServerSessionSpy) Wait() error {
	s.waitCalls++
	return nil
}

func TestRunProxyRequiresIOStreams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		stdin   io.Reader
		stdout  io.Writer
		stderr  io.Writer
		wantErr string
	}{
		{
			name:    "nil stdin",
			stdin:   nil,
			stdout:  io.Discard,
			stderr:  io.Discard,
			wantErr: "stdin is required",
		},
		{
			name:    "nil stdout",
			stdin:   strings.NewReader(""),
			stdout:  nil,
			stderr:  io.Discard,
			wantErr: "stdout is required",
		},
		{
			name:    "nil stderr",
			stdin:   strings.NewReader(""),
			stdout:  io.Discard,
			stderr:  nil,
			wantErr: "stderr is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RunProxy(context.Background(), "/tmp", Options{}, tc.stdin, tc.stdout, tc.stderr)
			if err == nil {
				t.Fatalf("RunProxy() error = nil, want %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("RunProxy() error = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestRunProxyRejectsInvalidMCPApprovalPolicy(t *testing.T) {
	err := RunProxy(context.Background(), "/tmp", Options{MCPApprovalPolicy: "prompt"}, strings.NewReader(""), io.Discard, io.Discard)
	if got, want := err.Error(), "invalid value \"prompt\" for --mcp-approval-policy:\nexpected ask, allow, or deny"; got != want {
		t.Fatalf("RunProxy() error = %q, want %q", got, want)
	}
}

func TestRunProxyEagerModeRequiresCodex(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := RunProxy(
		context.Background(),
		t.TempDir(),
		Options{},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("RunProxy() error = nil, want missing Codex error")
	}
	if !strings.Contains(err.Error(), `exec: "codex": executable file not found`) {
		t.Fatalf("RunProxy() error = %q, want missing Codex context", err)
	}
}

func TestRunProxyDeferredModeInitializesWithoutCodexAndDefersFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	clientToAgentReader, clientToAgentWriter := io.Pipe()
	agentToClientReader, agentToClientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientToAgentReader.Close()
		_ = clientToAgentWriter.Close()
		_ = agentToClientReader.Close()
		_ = agentToClientWriter.Close()
	})
	runResult := make(chan error, 1)
	go func() {
		runResult <- RunProxy(
			context.Background(),
			t.TempDir(),
			Options{DeferBackend: true},
			clientToAgentReader,
			agentToClientWriter,
			io.Discard,
		)
	}()

	clientConn := acp.NewClientSideConnection(bridgeTestClient{}, clientToAgentWriter, agentToClientReader)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initResponse, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
	})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if got, want := initResponse.AgentInfo.Name, DefaultAgentName; got != want {
		t.Fatalf("Initialize().AgentInfo.Name = %q, want %q", got, want)
	}
	if got, want := initResponse.AgentInfo.Version, DefaultAgentVersion; got != want {
		t.Fatalf("Initialize().AgentInfo.Version = %q, want %q", got, want)
	}

	_, err = clientConn.ListSessions(ctx, acp.ListSessionsRequest{})
	if err == nil {
		t.Fatal("ListSessions() error = nil, want missing Codex error")
	}
	if !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("ListSessions() error = %q, want missing Codex context", err)
	}

	if err := clientToAgentWriter.Close(); err != nil {
		t.Fatalf("close client input: %v", err)
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("RunProxy() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("RunProxy() did not stop after disconnect: %v", ctx.Err())
	}
}

func TestBuildCodexAppCommand(t *testing.T) {
	got := buildCodexAppCommand(Options{Name: "ignored"})
	if len(got) != 2 {
		t.Fatalf("buildCodexAppCommand() len = %d, want 2", len(got))
	}
	if got[0] != bridgeAppServerBinary || got[1] != bridgeAppServerSubcommand {
		t.Fatalf("buildCodexAppCommand() = %#v, want [%q, %q]", got, bridgeAppServerBinary, bridgeAppServerSubcommand)
	}
}

func TestBuildCodexAppCommandAppendsForwardedArgs(t *testing.T) {
	got := buildCodexAppCommand(Options{
		CodexArgs: []string{"--sandbox=workspace-write", "--search"},
	})
	if len(got) != 4 {
		t.Fatalf("buildCodexAppCommand() len = %d, want 4", len(got))
	}
	if got[0] != bridgeAppServerBinary || got[1] != "--sandbox=workspace-write" || got[2] != "--search" || got[3] != bridgeAppServerSubcommand {
		t.Fatalf("buildCodexAppCommand() = %#v, want [%q, \"--sandbox=workspace-write\", \"--search\", %q]", got, bridgeAppServerBinary, bridgeAppServerSubcommand)
	}
}

func TestBuildCodexAppCommandAppliesSandboxBeforeSubcommand(t *testing.T) {
	got := buildCodexAppCommand(Options{
		CodexArgs: []string{"--search"},
		Sandbox:   "danger-full-access",
	})
	want := []string{"codex", "--search", "--sandbox=danger-full-access", "app-server"}
	if len(got) != len(want) {
		t.Fatalf("buildCodexAppCommand() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("buildCodexAppCommand() = %#v, want %#v", got, want)
		}
	}
}

func TestValidateAppServerFactoryReturnsIdentityAndFinalizesBackend(t *testing.T) {
	t.Parallel()

	backend := &appServerSessionSpy{
		initializeResponse: appServerInitializeResponse{UserAgent: " codex_cli/1.2.3 "},
	}

	var receivedCWD string
	identity, err := validateAppServerFactory(context.Background(), func(_ context.Context, cwd string) (appServerSession, error) {
		receivedCWD = cwd
		return backend, nil
	}, "test-cwd")
	if err != nil {
		t.Fatalf("validateAppServerFactory() error = %v", err)
	}

	if receivedCWD != "test-cwd" {
		t.Fatalf("factory cwd = %q, want %q", receivedCWD, "test-cwd")
	}
	if got, want := identity.userAgent, "codex_cli/1.2.3"; got != want {
		t.Fatalf("identity.userAgent = %q, want %q", got, want)
	}
	if got, want := backend.closeCalls, 1; got != want {
		t.Fatalf("backend close calls = %d, want %d", got, want)
	}
	if got, want := backend.waitCalls, 1; got != want {
		t.Fatalf("backend wait calls = %d, want %d", got, want)
	}
}

func TestValidateAppServerFactoryPropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("factory failed")
	_, err := validateAppServerFactory(context.Background(), func(context.Context, string) (appServerSession, error) {
		return nil, wantErr
	}, "cwd")
	if !errors.Is(err, wantErr) {
		t.Fatalf("validateAppServerFactory() error = %v, want %v", err, wantErr)
	}
}

func TestSplitCommandForLog(t *testing.T) {
	cmd, args := splitCommandForLog(nil)
	if cmd != "" || args != nil {
		t.Fatalf("splitCommandForLog(nil) = (%q, %#v), want (\"\", nil)", cmd, args)
	}

	command := []string{"codex", "app-server", "--stdio"}
	cmd, args = splitCommandForLog(command)
	if cmd != "codex" {
		t.Fatalf("command name = %q, want %q", cmd, "codex")
	}
	if len(args) != 2 || args[0] != bridgeAppServerSubcommand || args[1] != "--stdio" {
		t.Fatalf("command args = %#v, want [%q, \"--stdio\"]", args, bridgeAppServerSubcommand)
	}

	command[1] = "mutated-bridge-command"
	if args[0] != bridgeAppServerSubcommand {
		t.Fatalf("args alias original slice: args[0] = %q, want %q", args[0], bridgeAppServerSubcommand)
	}
}
