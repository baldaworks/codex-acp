//go:build integration

package codexacp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	acp "github.com/coder/acp-go-sdk"
)

var errPromptAlreadyActive = errors.New("acp prompt already active")

type integrationACPClientConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Env        []string
}

type integrationExtendedSessionNotification struct {
	acp.SessionNotification
}

type integrationPromptResult struct {
	Response acp.PromptResponse
	Err      error
}

type integrationPromptState struct {
	updates chan integrationExtendedSessionNotification
}

type integrationACPClient struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  *acp.ClientSideConnection

	activeMu sync.Mutex
	active   map[acp.SessionId]*integrationPromptState
	closed   atomic.Bool
	waitOnce sync.Once
	waitErr  error
}

var _ acp.Client = (*integrationACPClient)(nil)

func newIntegrationACPClient(ctx context.Context, cfg integrationACPClientConfig) (*integrationACPClient, error) {
	if len(cfg.Command) == 0 {
		return nil, errors.New("acp command is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.WorkingDir
	if len(cfg.Env) > 0 {
		cmd.Env = append([]string(nil), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("acp stdout pipe: %w", err)
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start acp process: %w", err)
	}

	client := &integrationACPClient{
		cmd:    cmd,
		stdin:  stdin,
		active: map[acp.SessionId]*integrationPromptState{},
	}
	client.conn = acp.NewClientSideConnection(client, stdin, stdout)

	return client, nil
}

func (c *integrationACPClient) Initialize(ctx context.Context) (acp.InitializeResponse, error) {
	return c.conn.Initialize(ctx, acp.InitializeRequest{ProtocolVersion: acp.ProtocolVersionNumber})
}

func (c *integrationACPClient) NewSession(ctx context.Context, cwd string, mcpServers []acp.McpServer) (acp.NewSessionResponse, error) {
	return c.NewSessionWithMeta(ctx, cwd, mcpServers, nil)
}

func (c *integrationACPClient) NewSessionWithMeta(
	ctx context.Context,
	cwd string,
	mcpServers []acp.McpServer,
	meta map[string]any,
) (acp.NewSessionResponse, error) {
	if mcpServers == nil {
		mcpServers = []acp.McpServer{}
	}

	return c.conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: mcpServers,
		Meta:       cloneAnyMap(meta),
	})
}

func (c *integrationACPClient) SetSessionModel(ctx context.Context, sessionID, model string) error {
	_, err := c.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: acp.SessionId(sessionID),
			ConfigId:  acp.SessionConfigId("model"),
			Value:     acp.SessionConfigValueId(model),
		},
	})
	return err
}

func (c *integrationACPClient) Prompt(
	ctx context.Context,
	sessionID, prompt string,
) (<-chan integrationExtendedSessionNotification, <-chan integrationPromptResult, error) {
	return c.PromptWithContent(ctx, sessionID, []acp.ContentBlock{acp.TextBlock(prompt)})
}

func (c *integrationACPClient) PromptWithContent(
	ctx context.Context,
	sessionID string,
	prompt []acp.ContentBlock,
) (<-chan integrationExtendedSessionNotification, <-chan integrationPromptResult, error) {
	sid := acp.SessionId(sessionID)
	updates := make(chan integrationExtendedSessionNotification, 256)
	resultCh := make(chan integrationPromptResult, 1)

	if err := c.activatePrompt(sid, updates); err != nil {
		close(updates)
		close(resultCh)
		return nil, nil, err
	}

	go func() {
		defer close(resultCh)
		defer c.clearActive(sid)

		resp, err := c.conn.Prompt(ctx, acp.PromptRequest{SessionId: sid, Prompt: prompt})
		if err != nil {
			resultCh <- integrationPromptResult{Err: err}
			return
		}
		resultCh <- integrationPromptResult{Response: resp}
	}()

	return updates, resultCh, nil
}

func (c *integrationACPClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		if err := c.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			_ = c.wait()
			c.closeAllActive()
			return fmt.Errorf("kill acp process: %w", err)
		}
		_ = c.wait()
	}
	c.closeAllActive()
	return nil
}

func (c *integrationACPClient) CloseStdin() error {
	if c.stdin == nil {
		return nil
	}
	return c.stdin.Close()
}

func (c *integrationACPClient) Signal(sig os.Signal) error {
	if c.cmd == nil || c.cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := c.cmd.Process.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

func (c *integrationACPClient) Wait() error {
	return c.wait()
}

func (c *integrationACPClient) PID() int {
	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *integrationACPClient) ReadTextFile(_ context.Context, _ acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errors.New("readTextFile is not supported in integration helper")
}

func (c *integrationACPClient) WriteTextFile(_ context.Context, _ acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errors.New("writeTextFile is not supported in integration helper")
}

func (c *integrationACPClient) RequestPermission(_ context.Context, _ acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func (c *integrationACPClient) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	c.activeMu.Lock()
	active := c.active[params.SessionId]
	c.activeMu.Unlock()
	if active == nil {
		return nil
	}

	notification := integrationExtendedSessionNotification{SessionNotification: params}
	select {
	case active.updates <- notification:
	default:
	}
	return nil
}

func (c *integrationACPClient) CreateTerminal(_ context.Context, _ acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errors.New("createTerminal is not supported in integration helper")
}

func (c *integrationACPClient) KillTerminal(
	_ context.Context,
	_ acp.KillTerminalRequest,
) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errors.New("killTerminal is not supported in integration helper")
}

func (c *integrationACPClient) TerminalOutput(_ context.Context, _ acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errors.New("terminalOutput is not supported in integration helper")
}

func (c *integrationACPClient) ReleaseTerminal(_ context.Context, _ acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errors.New("releaseTerminal is not supported in integration helper")
}

func (c *integrationACPClient) WaitForTerminalExit(
	_ context.Context,
	_ acp.WaitForTerminalExitRequest,
) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errors.New("waitForTerminalExit is not supported in integration helper")
}

func (c *integrationACPClient) activatePrompt(
	sessionID acp.SessionId,
	updates chan integrationExtendedSessionNotification,
) error {
	c.activeMu.Lock()
	defer c.activeMu.Unlock()
	if _, exists := c.active[sessionID]; exists {
		return errPromptAlreadyActive
	}
	c.active[sessionID] = &integrationPromptState{updates: updates}
	return nil
}

func (c *integrationACPClient) clearActive(sessionID acp.SessionId) {
	c.activeMu.Lock()
	active, ok := c.active[sessionID]
	if ok {
		delete(c.active, sessionID)
	}
	c.activeMu.Unlock()
	if ok {
		close(active.updates)
	}
}

func (c *integrationACPClient) closeAllActive() {
	c.activeMu.Lock()
	active := c.active
	c.active = map[acp.SessionId]*integrationPromptState{}
	c.activeMu.Unlock()
	for _, stream := range active {
		close(stream.updates)
	}
}

func (c *integrationACPClient) wait() error {
	c.waitOnce.Do(func() {
		if c.cmd != nil {
			c.waitErr = c.cmd.Wait()
		}
	})
	return c.waitErr
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
