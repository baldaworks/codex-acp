package codexacp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/baldaworks/codex-acp/internal/apps/appio"
	"github.com/baldaworks/codex-acp/internal/logging"
	appversion "github.com/baldaworks/codex-acp/internal/version"
	acp "github.com/coder/acp-go-sdk"
)

// DefaultAgentName is the fallback ACP agent name used when backend identity is unavailable.
const DefaultAgentName = "codex-acp"

// DefaultAgentVersion is the fallback ACP agent version used when backend identity is unavailable.
var DefaultAgentVersion = appversion.String()

type appServerBackendFactory func(ctx context.Context, cwd string) (appServerSession, error)

type connectionInput struct {
	ready  <-chan struct{}
	reader io.Reader
}

func (r connectionInput) Read(p []byte) (int, error) {
	<-r.ready
	return r.reader.Read(p)
}

// RunProxy starts the Codex backend and exposes it as an ACP agent over stdio.
func RunProxy(ctx context.Context, workingDir string, opts Options, stdin io.Reader, stdout, stderr io.Writer) error {
	if stdin == nil {
		return errors.New("stdin is required")
	}
	if stdout == nil {
		return errors.New("stdout is required")
	}
	if stderr == nil {
		return errors.New("stderr is required")
	}
	if err := opts.validate(); err != nil {
		return err
	}

	lockedStderr := appio.NewSyncWriter(stderr)
	logger := logging.Ctx(ctx)

	command := buildCodexAppCommand(opts)
	cmdName, cmdArgs := splitCommandForLog(command)
	requestedAgentName := strings.TrimSpace(opts.Name)
	bridgeClientName := requestedAgentName
	if bridgeClientName == "" {
		bridgeClientName = DefaultAgentName
	}
	logger.Debug().
		Str("working_dir", workingDir).
		Str("agent_name", bridgeClientName).
		Str("cmd", cmdName).
		Strs("args", cmdArgs).
		Msg("starting codex acp bridge")
	logger.Info().
		Str("mcp_approval_policy", string(opts.mcpApprovalPolicy())).
		Msg("mcp approval policy configured")

	backendFactory := func(factoryCtx context.Context, sessionCWD string) (appServerSession, error) {
		return connectAppServerBackend(ctx, factoryCtx, workingDir, sessionCWD, command, bridgeClientName, lockedStderr, logger, opts)
	}
	sharedBackend := newSharedAppServerBackend(backendFactory)
	defer func() { _ = sharedBackend.Close() }()
	sessionFactory := sharedBackend.Session
	var identity appServerIdentity
	if !opts.DeferBackend {
		var err error
		identity, err = validateAppServerFactory(ctx, sessionFactory, workingDir)
		if err != nil {
			logger.Error().Err(err).Msg("codex backend initialization failed")
			return err
		}
	}
	agentName, agentVersion := resolveAgentIdentity(requestedAgentName, identity)
	logger.Debug().
		Str("resolved_agent_name", agentName).
		Str("resolved_agent_version", agentVersion).
		Msg("resolved acp agent identity")

	proxy := newCodexACPProxyAgent(sessionFactory, agentName, opts.appConfig(), logger)
	proxy.setBridgeOptions(opts)
	proxy.setAgentVersion(agentVersion)
	connectionReady := make(chan struct{})
	conn := acp.NewAgentSideConnection(proxy, stdout, connectionInput{
		ready:  connectionReady,
		reader: stdin,
	})
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	proxy.setConnection(conn)
	close(connectionReady)

	select {
	case <-conn.Done():
		logger.Debug().Msg("acp client disconnected")
		proxy.beginShutdown()
		proxy.closeAllSessionBackends()
		return nil
	case <-ctx.Done():
		logger.Debug().Err(ctx.Err()).Msg("proxy context canceled")
		proxy.beginShutdown()
		proxy.closeAllSessionBackends()
		return ctx.Err()
	}
}

func buildCodexAppCommand(opts Options) []string {
	command := append([]string{"codex"}, opts.CodexArgs...)
	if sandbox := strings.TrimSpace(opts.Sandbox); sandbox != "" {
		command = append(command, "--sandbox="+sandbox)
	}
	return append(command, "app-server")
}

func validateAppServerFactory(ctx context.Context, factory appServerBackendFactory, cwd string) (appServerIdentity, error) {
	backend, err := factory(ctx, cwd)
	if err != nil {
		return appServerIdentity{}, err
	}
	defer func() {
		_ = backend.Close()
		_ = backend.Wait()
	}()
	return parseAppServerIdentity(backend.InitializeResponse().UserAgent), nil
}

func splitCommandForLog(command []string) (string, []string) {
	if len(command) == 0 {
		return "", nil
	}
	args := append([]string(nil), command[1:]...)
	return command[0], args
}
