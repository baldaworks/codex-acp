package codexacp

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

const (
	decisionAccept                  = "accept"
	decisionAcceptForSession        = "acceptForSession"
	decisionDecline                 = "decline"
	decisionCancel                  = "cancel"
	decisionApproved                = "approved"
	decisionApprovedSession         = "approved_for_session"
	decisionDenied                  = "denied"
	decisionAbort                   = "abort"
	mcpContractMerge                = "merge"
	methodTurnStarted               = "turn/started"
	methodTurnCompleted             = "turn/completed"
	methodItemStarted               = "item/started"
	methodItemCompleted             = "item/completed"
	methodAgentMessageDelta         = "item/agentMessage/delta"
	methodReasoningTextDelta        = "item/reasoning/textDelta"
	methodReasoningSummaryTextDelta = "item/reasoning/summaryTextDelta"
	methodReasoningSummaryPartAdded = "item/reasoning/summaryPartAdded"

	sessionConfigIDModel                 = "model"
	sessionConfigIDReasoningEffort       = "reasoning_effort"
	sessionConfigCategoryThoughtLevel    = "thought_level"
	sessionConfigNameModel               = "Model"
	sessionConfigNameReasoningEffort     = "Reasoning Effort"
	sessionConfigTypeSelect              = "select"
	sessionConfigTypeBoolean             = "boolean"
	sessionConfigOptionModelRequired     = "model must be a string value"
	sessionConfigOptionValueUnsupported  = "session config option value is not supported"
	sessionConfigOptionIDUnsupported     = "session config option is not supported"
	sessionConfigOptionReasoningRequired = "reasoning effort must be a string value"

	metaItemIDKey        = "codex-acp-bridge/itemId"
	metaCompletedKey     = "codex-acp-bridge/completed"
	metaPhaseKey         = "codex-acp-bridge/phase"
	metaReasoningKindKey = "codex-acp-bridge/reasoningKind"
	metaSummaryIndexKey  = "codex-acp-bridge/summaryIndex"
	metaContentIndexKey  = "codex-acp-bridge/contentIndex"
	reasoningKindSummary = "summary"
	reasoningKindContent = "content"

	errBridgeShuttingDown        = "bridge is shutting down"
	errSessionBackendUnavailable = "session backend unavailable"
)

type codexACPConnection interface {
	SessionUpdate(ctx context.Context, params acp.SessionNotification) error
	RequestPermission(ctx context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
	UnstableCreateElicitation(ctx context.Context, params acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error)
}

type codexACPProxyAgent struct {
	agentName    string
	agentVersion string

	defaultConfig      codexAppConfig
	sessionFactory     appServerBackendFactory
	logger             *zerolog.Logger
	messageStreaming   bool
	reasoningStreaming bool
	reasoningThoughts  string
	reasoningSummary   string
	mcpApprovalPolicy  MCPApprovalPolicy

	connMu             sync.RWMutex
	conn               codexACPConnection
	clientCapabilities acp.ClientCapabilities

	mu           sync.Mutex
	sessions     map[acp.SessionId]*codexProxySessionState
	shuttingDown bool
}

type promptCompletion struct {
	stopReason acp.StopReason
	errorMeta  any
	usage      map[string]any
	err        error
}

type agentMessageItemState struct {
	phase      string
	phaseKnown bool
	streamed   bool
}

type reasoningLaneState struct {
	index    int64
	open     bool
	streamed bool
	hasText  bool
	text     string
}

type reasoningItemState struct {
	summary          reasoningLaneState
	content          reasoningLaneState
	summaryPublished bool
}

type planItemState struct {
	text string
}

type codexProxySessionState struct {
	cwd              string
	config           codexAppConfig
	threadID         string
	turnID           string
	model            string
	mode             string
	reasoningEffort  string
	reasoningSummary string
	mcpServers       map[string]acp.McpServer
	mcpStartup       map[string]sessionMCPStartup

	backend appServerSession
	cancel  context.CancelFunc
	done    chan promptCompletion

	workerCancel context.CancelFunc

	agentMessageItems   map[string]agentMessageItemState
	reasoningItems      map[string]reasoningItemState
	planItems           map[string]planItemState
	planOrder           []string
	pendingRequests     map[string]string
	pendingMCPToolCalls map[string]pendingMCPToolCall
	latestRateLimits    map[string]any
	latestUsage         map[string]any
}

type pendingMCPToolCall struct {
	itemID string
	tool   string
}

type sessionMCPStartup struct {
	status string
	err    string
}

func newCodexACPProxyAgent(
	sessionFactory appServerBackendFactory,
	agentName string,
	defaultConfig codexAppConfig,
	logger *zerolog.Logger,
) *codexACPProxyAgent {
	name := strings.TrimSpace(agentName)
	if name == "" {
		name = DefaultAgentName
	}
	version := DefaultAgentVersion
	return &codexACPProxyAgent{
		agentName:          name,
		agentVersion:       version,
		defaultConfig:      defaultConfig.withModel(defaultConfig.Model),
		sessionFactory:     sessionFactory,
		logger:             logger,
		reasoningStreaming: true,
		reasoningThoughts:  defaultReasoningThoughts,
		reasoningSummary:   defaultReasoningSummary,
		mcpApprovalPolicy:  defaultMCPApprovalPolicy,
		sessions:           make(map[acp.SessionId]*codexProxySessionState),
	}
}

func (a *codexACPProxyAgent) setBridgeOptions(opts Options) {
	a.messageStreaming = opts.MessageStreaming
	a.reasoningStreaming = opts.reasoningStreamingEnabled()
	a.reasoningThoughts = opts.reasoningThoughtsMode()
	a.reasoningSummary = opts.reasoningSummaryMode()
	a.mcpApprovalPolicy = opts.mcpApprovalPolicy()
}

func (a *codexACPProxyAgent) setConnection(conn codexACPConnection) {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	a.conn = conn
}

func (a *codexACPProxyAgent) setAgentVersion(version string) {
	next := strings.TrimSpace(version)
	if next == "" {
		next = DefaultAgentVersion
	}
	a.agentVersion = next
}

const codexLoginAuthMethodID = "codex-login"

func (a *codexACPProxyAgent) Authenticate(
	_ context.Context,
	params acp.AuthenticateRequest,
) (acp.AuthenticateResponse, error) {
	reason := "unknown authentication method"
	if params.MethodId == codexLoginAuthMethodID {
		reason = "terminal authentication must be run by the client"
	}
	return acp.AuthenticateResponse{}, acp.NewInvalidParams(map[string]any{
		"methodId": params.MethodId,
		"error":    reason,
	})
}

func (a *codexACPProxyAgent) Logout(_ context.Context, _ acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (a *codexACPProxyAgent) Initialize(_ context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error) {
	a.mu.Lock()
	a.clientCapabilities = params.ClientCapabilities
	a.mu.Unlock()
	authDescription := "Authenticate with the native Codex login flow"
	return acp.InitializeResponse{
		ProtocolVersion: acp.ProtocolVersionNumber,
		AgentInfo: &acp.Implementation{
			Name:    a.agentName,
			Version: a.agentVersion,
		},
		AgentCapabilities: acp.AgentCapabilities{
			McpCapabilities: acp.McpCapabilities{
				Http: true,
				Sse:  false,
			},
			PromptCapabilities: acp.PromptCapabilities{
				Audio:           false,
				Image:           true,
				EmbeddedContext: false,
			},
			SessionCapabilities: acp.SessionCapabilities{
				Close:  &acp.SessionCloseCapabilities{},
				List:   &acp.SessionListCapabilities{},
				Resume: &acp.SessionResumeCapabilities{},
			},
		},
		AuthMethods: []acp.AuthMethod{
			{
				Terminal: &acp.AuthMethodTerminalInline{
					Args:        []string{"login"},
					Description: &authDescription,
					Id:          codexLoginAuthMethodID,
					Name:        "Log in to Codex",
					Type:        "terminal",
				},
			},
		},
	}, nil
}

func (a *codexACPProxyAgent) Cancel(ctx context.Context, params acp.CancelNotification) error {
	a.mu.Lock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return nil
	}
	cancel := state.cancel
	backend := state.backend
	threadID := state.threadID
	turnID := state.turnID
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	a.clearPendingMCPToolCalls(params.SessionId)
	if backend != nil {
		_ = backend.TurnInterrupt(ctx, threadID, turnID)
	}
	return nil
}

func (a *codexACPProxyAgent) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return acp.ListSessionsResponse{}, err
	}

	backend, err := a.startSessionBackend(ctx, derefTrimmedString(params.Cwd))
	if err != nil {
		return acp.ListSessionsResponse{}, err
	}
	defer func() {
		_ = backend.Close()
		_ = backend.Wait()
	}()

	listResp, err := backend.ThreadList(ctx, buildThreadListParams(params.Cursor, params.Cwd))
	if err != nil {
		return acp.ListSessionsResponse{}, fmt.Errorf("thread/list: %w", err)
	}

	sessions := make([]acp.SessionInfo, 0, len(listResp.Data))
	for _, thread := range listResp.Data {
		sessions = append(sessions, sessionInfoFromAppServerThread(thread))
	}
	return acp.ListSessionsResponse{
		NextCursor: listResp.NextCursor,
		Sessions:   sessions,
	}, nil
}

func (a *codexACPProxyAgent) CloseSession(
	ctx context.Context,
	params acp.CloseSessionRequest,
) (acp.CloseSessionResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return acp.CloseSessionResponse{}, err
	}
	sessionID := acp.SessionId(strings.TrimSpace(string(params.SessionId)))
	if sessionID == "" {
		return acp.CloseSessionResponse{}, acp.NewInvalidParams("session not found")
	}

	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if ok {
		delete(a.sessions, sessionID)
	}
	a.mu.Unlock()

	if ok {
		a.closeLoadedSession(ctx, state)
		return acp.CloseSessionResponse{}, nil
	}

	backend, err := a.startSessionBackend(ctx, "")
	if err != nil {
		return acp.CloseSessionResponse{}, err
	}
	defer func() {
		_ = backend.Close()
		_ = backend.Wait()
	}()

	if err := unsubscribeThread(ctx, backend, string(sessionID)); err != nil {
		return acp.CloseSessionResponse{}, fmt.Errorf("thread/unsubscribe: %w", err)
	}
	return acp.CloseSessionResponse{}, nil
}

func (a *codexACPProxyAgent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return acp.NewSessionResponse{}, err
	}
	sessionConfig, err := sessionConfigFromMeta("session/new", params.Meta, a.defaultConfig)
	if err != nil {
		return acp.NewSessionResponse{}, acp.NewInvalidParams(err.Error())
	}
	var mcpServers map[string]acp.McpServer
	if len(params.McpServers) > 0 {
		mcpServers, err = validateMCPServers(params.McpServers)
		if err != nil {
			return acp.NewSessionResponse{}, acp.NewInvalidParams(err.Error())
		}
	}

	sessionState, err := a.startNewSession(ctx, strings.TrimSpace(params.Cwd), sessionConfig, mcpServers)
	if err != nil {
		return acp.NewSessionResponse{}, err
	}
	sessionState.reasoningSummary = a.reasoningSummary
	sessionID := acp.SessionId(sessionState.threadID)
	if err := a.registerSessionState(sessionID, sessionState); err != nil {
		a.closeSessionState(sessionState)
		return acp.NewSessionResponse{}, err
	}

	resp := acp.NewSessionResponse{SessionId: sessionID}
	configOptions, err := a.buildSessionModelConfigState(ctx, sessionID)
	if err != nil {
		a.logger.Warn().
			Err(err).
			Str("session_id", string(sessionID)).
			Msg("model/list unavailable; continuing without session model config")
	} else if len(configOptions) > 0 {
		resp.ConfigOptions = configOptions
	}
	resp.Modes = sessionModeState(sessionState.mode)
	if mcpMeta := a.sessionMCPMeta(sessionID, false); len(mcpMeta) > 0 {
		resp.Meta = map[string]any{
			"codex": map[string]any{
				"mcp": mcpMeta,
			},
		}
	}
	return resp, nil
}

func (a *codexACPProxyAgent) LoadSession(
	_ context.Context,
	_ acp.LoadSessionRequest,
) (acp.LoadSessionResponse, error) {
	return acp.LoadSessionResponse{}, acp.NewMethodNotFound(acp.AgentMethodSessionLoad)
}

func (a *codexACPProxyAgent) ResumeSession(
	ctx context.Context,
	params acp.ResumeSessionRequest,
) (acp.ResumeSessionResponse, error) {
	resp, err := a.restoreSession(
		ctx,
		params.SessionId,
		strings.TrimSpace(params.Cwd),
		params.McpServers,
		params.Meta,
		params.AdditionalDirectories,
		"session/resume",
	)
	if err != nil {
		return acp.ResumeSessionResponse{}, err
	}
	return resp.ResumeResponse(), nil
}

func (a *codexACPProxyAgent) startNewSession(
	ctx context.Context,
	cwd string,
	sessionConfig codexAppConfig,
	mcpServers map[string]acp.McpServer,
) (*codexProxySessionState, error) {
	backend, err := a.startSessionBackend(ctx, cwd)
	if err != nil {
		return nil, err
	}

	startResp, err := backend.ThreadStart(ctx, buildThreadStartParams(cwd, sessionConfig, sessionConfig.Model, mcpServers))
	if err != nil {
		_ = backend.Close()
		_ = backend.Wait()
		return nil, fmt.Errorf("thread/start: %w", err)
	}

	return newSessionStateFromThreadStart(cwd, sessionConfig, mcpServers, backend, startResp), nil
}

func (a *codexACPProxyAgent) restoreSession(
	ctx context.Context,
	sessionID acp.SessionId,
	cwd string,
	requestedMCPServers []acp.McpServer,
	meta map[string]any,
	additionalDirectories []string,
	method string,
) (sessionRestoreResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return sessionRestoreResponse{}, err
	}
	if len(additionalDirectories) > 0 {
		return sessionRestoreResponse{}, acp.NewInvalidParams("additionalDirectories is not supported")
	}

	sessionConfig, err := sessionConfigFromMeta(method, meta, a.defaultConfig)
	if err != nil {
		return sessionRestoreResponse{}, acp.NewInvalidParams(err.Error())
	}

	var mcpServers map[string]acp.McpServer
	if len(requestedMCPServers) > 0 {
		mcpServers, err = validateMCPServers(requestedMCPServers)
		if err != nil {
			return sessionRestoreResponse{}, acp.NewInvalidParams(err.Error())
		}
	}

	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		return sessionRestoreResponse{}, errors.New(errBridgeShuttingDown)
	}
	if _, exists := a.sessions[sessionID]; exists {
		a.mu.Unlock()
		return sessionRestoreResponse{}, acp.NewInvalidParams(fmt.Sprintf("session %q already exists", sessionID))
	}
	a.mu.Unlock()

	backend, err := a.startSessionBackend(ctx, cwd)
	if err != nil {
		return sessionRestoreResponse{}, err
	}

	resumeResp, err := backend.ThreadResume(ctx, buildThreadResumeParams(string(sessionID), cwd, sessionConfig, sessionConfig.Model, mcpServers))
	if err != nil {
		_ = backend.Close()
		_ = backend.Wait()
		return sessionRestoreResponse{}, fmt.Errorf("thread/resume: %w", err)
	}

	sessionState := newSessionStateFromThreadResume(cwd, sessionConfig, mcpServers, backend, resumeResp)
	sessionState.reasoningSummary = a.reasoningSummary
	if err := a.registerSessionState(sessionID, sessionState); err != nil {
		a.closeSessionState(sessionState)
		return sessionRestoreResponse{}, err
	}

	configOptions, err := a.buildSessionModelConfigState(ctx, sessionID)
	if err != nil {
		a.logger.Warn().
			Err(err).
			Str("session_id", string(sessionID)).
			Msg("model/list unavailable; continuing without session model config")
	}

	resp := sessionRestoreResponse{}
	if len(configOptions) > 0 {
		resp.configOptions = configOptions
	}
	resp.modes = sessionModeState(sessionState.mode)
	if mcpMeta := a.sessionMCPMeta(sessionID, false); len(mcpMeta) > 0 {
		resp.meta = map[string]any{
			"codex": map[string]any{
				"mcp": mcpMeta,
			},
		}
	}
	return resp, nil
}

type sessionRestoreResponse struct {
	configOptions []acp.SessionConfigOption
	modes         *acp.SessionModeState
	meta          map[string]any
}

func (r sessionRestoreResponse) ResumeResponse() acp.ResumeSessionResponse {
	return acp.ResumeSessionResponse{
		ConfigOptions: r.configOptions,
		Meta:          cloneAnyMap(r.meta),
		Modes:         r.modes,
	}
}

func (a *codexACPProxyAgent) startSessionBackend(ctx context.Context, cwd string) (appServerSession, error) {
	backend, err := a.sessionFactory(ctx, cwd)
	if err != nil {
		return nil, fmt.Errorf("create bridge backend backend: %w", err)
	}
	return backend, nil
}

func (a *codexACPProxyAgent) registerSessionState(sessionID acp.SessionId, state *codexProxySessionState) error {
	if state == nil || state.backend == nil {
		return errors.New(errSessionBackendUnavailable)
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	state.workerCancel = workerCancel

	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		workerCancel()
		return errors.New(errBridgeShuttingDown)
	}
	if _, exists := a.sessions[sessionID]; exists {
		a.mu.Unlock()
		workerCancel()
		return acp.NewInvalidParams(fmt.Sprintf("session %q already exists", sessionID))
	}
	a.sessions[sessionID] = state
	a.mu.Unlock()

	go a.runSessionEventLoop(workerCtx, sessionID, state.backend)
	return nil
}

func (a *codexACPProxyAgent) closeSessionState(state *codexProxySessionState) {
	if state == nil {
		return
	}
	if state.workerCancel != nil {
		state.workerCancel()
	}
	if state.backend != nil {
		_ = state.backend.Close()
		_ = state.backend.Wait()
	}
}

func (a *codexACPProxyAgent) closeLoadedSession(ctx context.Context, state *codexProxySessionState) {
	if state == nil {
		return
	}
	defer a.closeSessionState(state)

	if state.cancel != nil {
		state.cancel()
	}
	if state.backend == nil {
		return
	}
	_ = state.backend.TurnInterrupt(ctx, state.threadID, state.turnID)
	_ = unsubscribeThread(ctx, state.backend, state.threadID)
}

func newSessionStateFromThreadStart(
	cwd string,
	sessionConfig codexAppConfig,
	mcpServers map[string]acp.McpServer,
	backend appServerSession,
	startResp appServerThreadStartResponse,
) *codexProxySessionState {
	return newSessionState(
		cwd,
		sessionConfig,
		mcpServers,
		backend,
		startResp.Thread,
		startResp.Model,
		startResp.ReasoningEffort,
	)
}

func newSessionStateFromThreadResume(
	cwd string,
	sessionConfig codexAppConfig,
	mcpServers map[string]acp.McpServer,
	backend appServerSession,
	resumeResp appServerThreadResumeResponse,
) *codexProxySessionState {
	return newSessionState(
		cwd,
		sessionConfig,
		mcpServers,
		backend,
		resumeResp.Thread,
		resumeResp.Model,
		resumeResp.ReasoningEffort,
	)
}

func newSessionState(
	cwd string,
	sessionConfig codexAppConfig,
	mcpServers map[string]acp.McpServer,
	backend appServerSession,
	thread appServerThread,
	model string,
	reasoningEffort *string,
) *codexProxySessionState {
	state := &codexProxySessionState{
		cwd:                 strings.TrimSpace(cwd),
		config:              sessionConfig,
		threadID:            strings.TrimSpace(thread.ID),
		model:               sessionConfig.Model,
		mcpServers:          mcpServers,
		backend:             backend,
		pendingMCPToolCalls: make(map[string]pendingMCPToolCall),
	}
	if strings.TrimSpace(state.model) == "" {
		state.model = strings.TrimSpace(model)
	}
	if reasoningEffort != nil {
		state.reasoningEffort = strings.TrimSpace(*reasoningEffort)
	}
	return state
}

func sessionModeState(mode string) *acp.SessionModeState {
	currentMode := strings.TrimSpace(mode)
	availableModes := []acp.SessionMode{}
	if currentMode != "" {
		availableModes = append(availableModes, acp.SessionMode{
			Id:   acp.SessionModeId(currentMode),
			Name: currentMode,
		})
	}
	return &acp.SessionModeState{
		AvailableModes: availableModes,
		CurrentModeId:  acp.SessionModeId(currentMode),
	}
}

func (a *codexACPProxyAgent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return acp.PromptResponse{}, err
	}
	if err := a.ensureSessionBackend(ctx, params.SessionId); err != nil {
		return acp.PromptResponse{}, err
	}
	if err := a.ensureSessionThread(ctx, params.SessionId); err != nil {
		return acp.PromptResponse{}, err
	}

	a.mu.Lock()
	state, ok := a.sessions[params.SessionId]
	if !ok {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidParams("session not found")
	}
	if a.shuttingDown {
		a.mu.Unlock()
		return acp.PromptResponse{}, errors.New(errBridgeShuttingDown)
	}
	if state.done != nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, acp.NewInvalidRequest("prompt already active for session")
	}
	if state.backend == nil {
		a.mu.Unlock()
		return acp.PromptResponse{}, errors.New(errSessionBackendUnavailable)
	}
	promptCtx, cancel := context.WithCancel(ctx)
	state.cancel = cancel
	state.done = make(chan promptCompletion, 1)
	backend := state.backend
	threadID := state.threadID
	model := state.model
	reasoningEffort := state.reasoningEffort
	reasoningSummary := state.reasoningSummary
	if strings.TrimSpace(reasoningSummary) == "" {
		reasoningSummary = a.reasoningSummary
	}
	doneCh := state.done
	a.mu.Unlock()

	turnStartParams, err := buildTurnStartParams(threadID, params.Prompt, model, reasoningEffort, reasoningSummary)
	if err != nil {
		return acp.PromptResponse{}, acp.NewInvalidParams(err.Error())
	}

	defer func() {
		a.mu.Lock()
		if current := a.sessions[params.SessionId]; current != nil {
			if current.done == doneCh {
				current.cancel = nil
				current.done = nil
				current.turnID = ""
				current.pendingRequests = nil
			}
		}
		a.mu.Unlock()
	}()

	turnStart, err := backend.TurnStart(promptCtx, turnStartParams)
	if err != nil {
		return acp.PromptResponse{}, fmt.Errorf("turn/start: %w", err)
	}
	turnID := strings.TrimSpace(turnStart.Turn.ID)
	a.mu.Lock()
	if current := a.sessions[params.SessionId]; current != nil {
		current.turnID = turnID
		current.agentMessageItems = make(map[string]agentMessageItemState)
		current.reasoningItems = make(map[string]reasoningItemState)
		current.planItems = make(map[string]planItemState)
		current.planOrder = nil
		current.pendingRequests = make(map[string]string)
		current.latestUsage = nil
	}
	a.mu.Unlock()

	for {
		select {
		case <-promptCtx.Done():
			if errors.Is(promptCtx.Err(), context.Canceled) {
				return acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
			}
			return acp.PromptResponse{}, promptCtx.Err()
		case completion := <-doneCh:
			if completion.err != nil {
				return acp.PromptResponse{}, completion.err
			}
			resp := acp.PromptResponse{StopReason: completion.stopReason}
			meta := map[string]any{}
			if completion.errorMeta != nil {
				meta["error"] = cloneJSONValue(completion.errorMeta)
			}
			usage := completion.usage
			if usage == nil {
				usage = a.sessionUsage(params.SessionId)
			}
			if usage != nil {
				meta["usage"] = usage
				resp.Usage = acpUsageFromMap(usage)
			}
			if rateLimits := a.sessionRateLimits(params.SessionId); len(rateLimits) > 0 {
				meta["rateLimits"] = rateLimits
			}
			if mcpMeta := a.sessionMCPMeta(params.SessionId, true); len(mcpMeta) > 0 {
				meta["codex"] = map[string]any{
					"mcp": mcpMeta,
				}
			}
			if len(meta) > 0 {
				resp.Meta = meta
			}
			return resp, nil
		}
	}
}

func (a *codexACPProxyAgent) SetSessionConfigOption(
	ctx context.Context,
	params acp.SetSessionConfigOptionRequest,
) (acp.SetSessionConfigOptionResponse, error) {
	if err := a.rejectIfShuttingDown(); err != nil {
		return acp.SetSessionConfigOptionResponse{}, err
	}
	req := params.ValueId
	if req == nil {
		if params.Boolean != nil {
			switch strings.TrimSpace(string(params.Boolean.ConfigId)) {
			case sessionConfigIDModel:
				return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionModelRequired)
			case sessionConfigIDReasoningEffort:
				return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionReasoningRequired)
			default:
				return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionIDUnsupported)
			}
		}
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionValueUnsupported)
	}

	switch strings.TrimSpace(string(req.ConfigId)) {
	case sessionConfigIDModel:
		nextModel := strings.TrimSpace(string(req.Value))
		if nextModel == "" {
			return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionValueUnsupported)
		}
		configOptions, err := a.setSessionModel(ctx, req.SessionId, nextModel)
		if err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}
		return acp.SetSessionConfigOptionResponse{ConfigOptions: configOptions}, nil
	case sessionConfigIDReasoningEffort:
		nextEffort := strings.TrimSpace(string(req.Value))
		if nextEffort == "" {
			return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionValueUnsupported)
		}

		configOptions, err := a.setSessionReasoningEffort(ctx, req.SessionId, nextEffort)
		if err != nil {
			return acp.SetSessionConfigOptionResponse{}, err
		}
		return acp.SetSessionConfigOptionResponse{ConfigOptions: configOptions}, nil
	default:
		return acp.SetSessionConfigOptionResponse{}, acp.NewInvalidParams(sessionConfigOptionIDUnsupported)
	}
}

func (a *codexACPProxyAgent) SetSessionMode(_ context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	nextMode := strings.TrimSpace(string(params.ModeId))
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shuttingDown {
		return acp.SetSessionModeResponse{}, errors.New(errBridgeShuttingDown)
	}
	state, ok := a.sessions[params.SessionId]
	if !ok {
		return acp.SetSessionModeResponse{}, acp.NewInvalidParams("session not found")
	}
	if state.done != nil {
		return acp.SetSessionModeResponse{}, acp.NewInvalidRequest("cannot update session mode while prompt is active")
	}
	state.mode = nextMode
	return acp.SetSessionModeResponse{}, nil
}

func (a *codexACPProxyAgent) ensureSessionBackend(ctx context.Context, sessionID acp.SessionId) error {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return acp.NewInvalidParams("session not found")
	}
	if a.shuttingDown {
		a.mu.Unlock()
		return errors.New(errBridgeShuttingDown)
	}
	if state.backend != nil {
		if state.workerCancel != nil {
			a.mu.Unlock()
			return nil
		}
		backend := state.backend
		workerCtx, workerCancel := context.WithCancel(context.Background())
		state.workerCancel = workerCancel
		a.mu.Unlock()
		go a.runSessionEventLoop(workerCtx, sessionID, backend)
		return nil
	}
	sessionCWD := state.cwd
	a.mu.Unlock()

	backend, err := a.sessionFactory(ctx, sessionCWD)
	if err != nil {
		return fmt.Errorf("create bridge backend backend: %w", err)
	}

	a.mu.Lock()
	state, ok = a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		_ = backend.Close()
		_ = backend.Wait()
		return acp.NewInvalidParams("session not found")
	}
	if a.shuttingDown {
		a.mu.Unlock()
		_ = backend.Close()
		_ = backend.Wait()
		return errors.New(errBridgeShuttingDown)
	}
	if state.backend != nil {
		a.mu.Unlock()
		_ = backend.Close()
		_ = backend.Wait()
		return nil
	}
	workerCtx, workerCancel := context.WithCancel(context.Background())
	state.backend = backend
	state.workerCancel = workerCancel
	a.mu.Unlock()
	go a.runSessionEventLoop(workerCtx, sessionID, backend)
	return nil
}

func (a *codexACPProxyAgent) ensureSessionThread(ctx context.Context, sessionID acp.SessionId) error {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return acp.NewInvalidParams("session not found")
	}
	if state.backend == nil {
		a.mu.Unlock()
		return errors.New(errSessionBackendUnavailable)
	}
	if strings.TrimSpace(state.threadID) != "" {
		a.mu.Unlock()
		return nil
	}
	backend := state.backend
	cwd := state.cwd
	sessionConfig := state.config
	model := state.model
	mcpServers := state.mcpServers
	a.mu.Unlock()

	startResp, err := backend.ThreadStart(ctx, buildThreadStartParams(cwd, sessionConfig, model, mcpServers))
	if err != nil {
		return fmt.Errorf("thread/start: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	state, ok = a.sessions[sessionID]
	if !ok {
		return acp.NewInvalidParams("session not found")
	}
	state.threadID = strings.TrimSpace(startResp.Thread.ID)
	if strings.TrimSpace(state.model) == "" {
		state.model = strings.TrimSpace(startResp.Model)
	}
	if startResp.ReasoningEffort != nil {
		state.reasoningEffort = strings.TrimSpace(*startResp.ReasoningEffort)
	}
	return nil
}

func (a *codexACPProxyAgent) buildSessionModelConfigState(
	ctx context.Context,
	sessionID acp.SessionId,
) ([]acp.SessionConfigOption, error) {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return nil, acp.NewInvalidParams("session not found")
	}
	backend := state.backend
	currentModelID := strings.TrimSpace(state.model)
	currentReasoningEffort := strings.TrimSpace(state.reasoningEffort)
	a.mu.Unlock()
	if backend == nil {
		return nil, errors.New(errSessionBackendUnavailable)
	}

	models, err := listAppServerModels(ctx, backend)
	if err != nil {
		return nil, err
	}
	configOptions := a.buildSessionModelConfigStateFromModels(
		sessionID,
		models,
		currentModelID,
		currentReasoningEffort,
	)
	return configOptions, nil
}

func (a *codexACPProxyAgent) buildSessionModelConfigStateFromModels(
	sessionID acp.SessionId,
	models []appServerModel,
	currentModelID string,
	currentReasoningEffort string,
) []acp.SessionConfigOption {
	if len(models) == 0 {
		return nil
	}

	defaultModelID := ""
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		if model.IsDefault && defaultModelID == "" {
			defaultModelID = modelID
		}
	}
	if defaultModelID == "" && currentModelID == "" {
		for _, model := range models {
			if modelID := strings.TrimSpace(model.ID); modelID != "" {
				defaultModelID = modelID
				break
			}
		}
	}

	if currentModelID == "" {
		currentModelID = defaultModelID
	}

	var configOptions []acp.SessionConfigOption
	selectedModel, hasSelectedModel := selectAppServerModel(models, currentModelID)
	if hasSelectedModel {
		currentModelID = strings.TrimSpace(selectedModel.ID)
		if option, ok := modelConfigOption(models, currentModelID); ok {
			configOptions = append(configOptions, option)
		}
		if option, selectedEffort, ok := reasoningEffortConfigOption(selectedModel, currentReasoningEffort, ""); ok {
			configOptions = append(configOptions, option)
			a.mu.Lock()
			if state := a.sessions[sessionID]; state != nil {
				state.model = currentModelID
				state.reasoningEffort = selectedEffort
			}
			a.mu.Unlock()
		} else {
			a.mu.Lock()
			if state := a.sessions[sessionID]; state != nil {
				state.model = currentModelID
				state.reasoningEffort = ""
			}
			a.mu.Unlock()
		}
	}

	return configOptions
}

func (a *codexACPProxyAgent) setSessionModel(
	ctx context.Context,
	sessionID acp.SessionId,
	nextModel string,
) ([]acp.SessionConfigOption, error) {
	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		return nil, errors.New(errBridgeShuttingDown)
	}
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return nil, acp.NewInvalidParams("session not found")
	}
	if state.done != nil {
		a.mu.Unlock()
		return nil, acp.NewInvalidRequest("cannot update session model while prompt is active")
	}
	backend := state.backend
	threadID := state.threadID
	previousModel := state.model
	currentReasoningEffort := state.reasoningEffort
	currentReasoningSummary := state.reasoningSummary
	if strings.TrimSpace(currentReasoningSummary) == "" {
		currentReasoningSummary = a.reasoningSummary
	}
	a.mu.Unlock()

	if backend == nil {
		return nil, errors.New(errSessionBackendUnavailable)
	}

	models, err := listAppServerModels(ctx, backend)
	if err != nil {
		return nil, err
	}
	selectedModel, ok := findAppServerModel(models, nextModel)
	if !ok {
		return nil, acp.NewInvalidParams("session model is not supported")
	}
	selectedModelID := strings.TrimSpace(selectedModel.ID)

	a.mu.Lock()
	if state := a.sessions[sessionID]; state != nil && state.done == nil {
		state.model = selectedModelID
	}
	a.mu.Unlock()

	if err := persistSessionSettingsUpdate(ctx, backend, threadID, selectedModelID, "", currentReasoningSummary); err != nil {
		a.mu.Lock()
		if state := a.sessions[sessionID]; state != nil && state.done == nil && state.model == selectedModelID {
			state.model = previousModel
		}
		a.mu.Unlock()
		return nil, err
	}

	configOptions := a.buildSessionModelConfigStateFromModels(
		sessionID,
		models,
		selectedModelID,
		currentReasoningEffort,
	)
	return configOptions, nil
}

func (a *codexACPProxyAgent) setSessionReasoningEffort(
	ctx context.Context,
	sessionID acp.SessionId,
	nextEffort string,
) ([]acp.SessionConfigOption, error) {
	a.mu.Lock()
	state, ok := a.sessions[sessionID]
	if !ok {
		a.mu.Unlock()
		return nil, acp.NewInvalidParams("session not found")
	}
	backend := state.backend
	currentModelID := strings.TrimSpace(state.model)
	threadID := state.threadID
	previousEffort := state.reasoningEffort
	currentReasoningSummary := state.reasoningSummary
	if strings.TrimSpace(currentReasoningSummary) == "" {
		currentReasoningSummary = a.reasoningSummary
	}
	a.mu.Unlock()
	if backend == nil {
		return nil, errors.New(errSessionBackendUnavailable)
	}

	models, err := listAppServerModels(ctx, backend)
	if err != nil {
		return nil, err
	}
	model, ok := selectAppServerModel(models, currentModelID)
	if !ok {
		return nil, acp.NewInvalidParams(sessionConfigOptionIDUnsupported)
	}
	_, selectedEffort, ok := reasoningEffortConfigOption(model, "", nextEffort)
	if !ok {
		return nil, acp.NewInvalidParams(sessionConfigOptionIDUnsupported)
	}
	if selectedEffort != strings.TrimSpace(nextEffort) {
		return nil, acp.NewInvalidParams(sessionConfigOptionValueUnsupported)
	}

	a.mu.Lock()
	if state := a.sessions[sessionID]; state != nil {
		state.reasoningEffort = selectedEffort
		if strings.TrimSpace(state.model) == "" {
			state.model = strings.TrimSpace(model.ID)
		}
	}
	a.mu.Unlock()

	if err := persistSessionSettingsUpdate(ctx, backend, threadID, "", selectedEffort, currentReasoningSummary); err != nil {
		a.mu.Lock()
		if state := a.sessions[sessionID]; state != nil && state.done == nil && state.reasoningEffort == selectedEffort {
			state.reasoningEffort = previousEffort
		}
		a.mu.Unlock()
		return nil, err
	}

	configOptions := a.buildSessionModelConfigStateFromModels(sessionID, models, currentModelID, selectedEffort)
	return configOptions, nil
}

func persistSessionSettingsUpdate(
	ctx context.Context,
	backend appServerSession,
	threadID string,
	model string,
	reasoningEffort string,
	reasoningSummary string,
) error {
	if backend == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	if err := backend.ThreadSettingsUpdate(ctx, buildThreadSettingsUpdateParams(threadID, model, reasoningEffort, reasoningSummary)); err != nil {
		return fmt.Errorf("thread/settings/update: %w", err)
	}
	return nil
}

func selectAppServerModel(models []appServerModel, modelID string) (appServerModel, bool) {
	trimmedModelID := strings.TrimSpace(modelID)
	if trimmedModelID != "" {
		for _, model := range models {
			if strings.TrimSpace(model.ID) == trimmedModelID {
				return model, true
			}
		}
	}
	for _, model := range models {
		if model.IsDefault && strings.TrimSpace(model.ID) != "" {
			return model, true
		}
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) != "" {
			return model, true
		}
	}
	return appServerModel{}, false
}

func modelConfigOption(models []appServerModel, currentModelID string) (acp.SessionConfigOption, bool) {
	options := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		modelID := strings.TrimSpace(model.ID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		name := strings.TrimSpace(model.DisplayName)
		if name == "" {
			name = modelID
		}
		option := acp.SessionConfigSelectOption{
			Name:  name,
			Value: acp.SessionConfigValueId(modelID),
		}
		if model.Description != nil {
			description := strings.TrimSpace(*model.Description)
			if description != "" {
				option.Description = &description
			}
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return acp.SessionConfigOption{}, false
	}
	category := acp.SessionConfigOptionCategoryModel
	return acp.SessionConfigOption{
		Select: &acp.SessionConfigOptionSelect{
			Type:         sessionConfigTypeSelect,
			Id:           acp.SessionConfigId(sessionConfigIDModel),
			Name:         sessionConfigNameModel,
			Category:     &category,
			CurrentValue: acp.SessionConfigValueId(currentModelID),
			Options:      acp.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}, true
}

func reasoningEffortConfigOption(
	model appServerModel,
	currentEffort string,
	requestedEffort string,
) (acp.SessionConfigOption, string, bool) {
	options := make(acp.SessionConfigSelectOptionsUngrouped, 0, len(model.SupportedReasoningEfforts))
	seen := make(map[string]struct{}, len(model.SupportedReasoningEfforts))
	for _, effortOption := range model.SupportedReasoningEfforts {
		effort := strings.TrimSpace(effortOption.ReasoningEffort)
		if effort == "" {
			continue
		}
		if _, ok := seen[effort]; ok {
			continue
		}
		seen[effort] = struct{}{}
		option := acp.SessionConfigSelectOption{
			Name:  reasoningEffortName(effort),
			Value: acp.SessionConfigValueId(effort),
		}
		if description := strings.TrimSpace(effortOption.Description); description != "" {
			option.Description = &description
		}
		options = append(options, option)
	}
	if len(options) == 0 {
		return acp.SessionConfigOption{}, "", false
	}

	selectedEffort := chooseReasoningEffort(model, seen, currentEffort, requestedEffort)
	if selectedEffort == "" {
		return acp.SessionConfigOption{}, "", false
	}
	category := acp.SessionConfigOptionCategoryThoughtLevel
	return acp.SessionConfigOption{
		Select: &acp.SessionConfigOptionSelect{
			Type:         sessionConfigTypeSelect,
			Id:           acp.SessionConfigId(sessionConfigIDReasoningEffort),
			Name:         sessionConfigNameReasoningEffort,
			Category:     &category,
			CurrentValue: acp.SessionConfigValueId(selectedEffort),
			Options:      acp.SessionConfigSelectOptions{Ungrouped: &options},
		},
	}, selectedEffort, true
}

func chooseReasoningEffort(
	model appServerModel,
	supported map[string]struct{},
	currentEffort string,
	requestedEffort string,
) string {
	for _, candidate := range []string{
		strings.TrimSpace(requestedEffort),
		strings.TrimSpace(currentEffort),
		strings.TrimSpace(model.DefaultReasoningEffort),
	} {
		if candidate == "" {
			continue
		}
		if _, ok := supported[candidate]; ok {
			return candidate
		}
	}
	for _, effortOption := range model.SupportedReasoningEfforts {
		effort := strings.TrimSpace(effortOption.ReasoningEffort)
		if effort == "" {
			continue
		}
		if _, ok := supported[effort]; ok {
			return effort
		}
	}
	return ""
}

func reasoningEffortName(effort string) string {
	trimmed := strings.TrimSpace(effort)
	if trimmed == "" {
		return ""
	}
	return strings.ToUpper(trimmed[:1]) + trimmed[1:]
}

func listAppServerModels(ctx context.Context, backend appServerSession) ([]appServerModel, error) {
	models := make([]appServerModel, 0, 16)
	cursor := ""
	hasCursor := false
	seenCursors := make(map[string]struct{}, 4)

	for {
		params := map[string]any{}
		if hasCursor {
			params["cursor"] = cursor
		}

		resp, err := backend.ModelList(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("model/list: %w", err)
		}
		models = append(models, resp.Data...)

		if resp.NextCursor == nil {
			return models, nil
		}
		nextCursor := strings.TrimSpace(*resp.NextCursor)
		if nextCursor == "" {
			return models, nil
		}
		if _, seen := seenCursors[nextCursor]; seen {
			return nil, fmt.Errorf("model/list: repeated cursor %q", nextCursor)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
		hasCursor = true
	}
}

func (a *codexACPProxyAgent) runSessionEventLoop(ctx context.Context, sessionID acp.SessionId, backend appServerSession) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-backend.Events():
			if !ok {
				a.clearSessionBackend(sessionID, backend)
				a.completePrompt(sessionID, promptCompletion{err: errors.New("bridge backend event stream closed")})
				return
			}
			if event.Request != nil {
				if err := a.handleServerRequest(ctx, sessionID, event.Request); err != nil {
					a.completePrompt(sessionID, promptCompletion{err: err})
				}
				continue
			}
			if event.Notification == nil {
				continue
			}

			threadID, turnID, hasActivePrompt := a.currentSessionCorrelation(sessionID)
			done, stopReason, usage, errorMeta, err := a.handleNotification(ctx, sessionID, threadID, turnID, hasActivePrompt, event.Notification)
			if err != nil {
				a.completePrompt(sessionID, promptCompletion{err: err})
				continue
			}
			if usage != nil {
				a.setSessionUsage(sessionID, usage)
			}
			if done {
				a.completePrompt(sessionID, promptCompletion{
					stopReason: stopReason,
					errorMeta:  errorMeta,
					usage:      usage,
				})
			}
		}
	}
}

func (a *codexACPProxyAgent) currentSessionCorrelation(sessionID acp.SessionId) (threadID string, turnID string, hasActivePrompt bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return "", "", false
	}
	return state.threadID, state.turnID, state.done != nil
}

func (a *codexACPProxyAgent) completePrompt(sessionID acp.SessionId, completion promptCompletion) {
	a.mu.Lock()
	state := a.sessions[sessionID]
	if state == nil || state.done == nil {
		a.mu.Unlock()
		return
	}
	doneCh := state.done
	state.turnID = ""
	state.pendingRequests = nil
	a.mu.Unlock()

	select {
	case doneCh <- completion:
	default:
	}
}

func (a *codexACPProxyAgent) closeAllSessionBackends() {
	type backendEntry struct {
		backend      appServerSession
		cancel       context.CancelFunc
		workerCancel context.CancelFunc
	}
	a.mu.Lock()
	a.shuttingDown = true
	entries := make([]backendEntry, 0, len(a.sessions))
	for _, state := range a.sessions {
		entries = append(entries, backendEntry{
			backend:      state.backend,
			cancel:       state.cancel,
			workerCancel: state.workerCancel,
		})
		state.cancel = nil
		state.done = nil
		state.workerCancel = nil
		state.backend = nil
		state.threadID = ""
		state.turnID = ""
	}
	a.mu.Unlock()

	for _, entry := range entries {
		if entry.cancel != nil {
			entry.cancel()
		}
		if entry.workerCancel != nil {
			entry.workerCancel()
		}
		if entry.backend != nil {
			_ = entry.backend.Close()
			_ = entry.backend.Wait()
		}
	}
}

func (a *codexACPProxyAgent) beginShutdown() {
	a.mu.Lock()
	a.shuttingDown = true
	a.mu.Unlock()
}

func (a *codexACPProxyAgent) rejectIfShuttingDown() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shuttingDown {
		return errors.New(errBridgeShuttingDown)
	}
	return nil
}

func (a *codexACPProxyAgent) handleNotification(
	ctx context.Context,
	sessionID acp.SessionId,
	threadID string,
	turnID string,
	hasActivePrompt bool,
	note *appServerNotification,
) (done bool, stopReason acp.StopReason, usage map[string]any, errorMeta any, err error) {
	params, err := decodeJSONMap(note.Params)
	if err != nil {
		return false, "", nil, nil, nil
	}
	if !matchesThreadID(params, threadID) {
		return false, "", nil, nil, nil
	}
	if requiresActiveTurn(note.Method) {
		if !hasActivePrompt {
			return false, "", nil, nil, nil
		}
		if strings.TrimSpace(turnID) == "" && note.Method != methodTurnStarted {
			if nextTurnID := strings.TrimSpace(stringValue(params, "turnId")); nextTurnID != "" {
				a.syncTurnID(sessionID, nextTurnID)
				turnID = nextTurnID
			}
		}
		if strings.TrimSpace(turnID) == "" {
			return false, "", nil, nil, nil
		}
		if note.Method != methodTurnStarted && !matchesTurnID(params, turnID) {
			return false, "", nil, nil, nil
		}
	}
	if note.Method == methodTurnStarted && !hasActivePrompt {
		return false, "", nil, nil, nil
	}

	switch note.Method {
	case "thread/started":
		thread := mapValue(params, "thread")
		startedThreadID := stringValue(thread, "id")
		if startedThreadID != "" {
			a.syncThreadID(sessionID, startedThreadID)
		}
	case "error":
		willRetry, ok := boolValue(params, "willRetry")
		if ok && !willRetry {
			a.logTerminalPromptError("error", sessionID, threadID, turnID, acp.StopReasonEndTurn, params["error"])
			return true, acp.StopReasonEndTurn, usageFromTokenNotification(params), params["error"], nil
		}
	case "thread/status/changed":
	case "turn/diff/updated":
	case methodTurnStarted:
		turn := mapValue(params, "turn")
		startedTurnID := stringValue(turn, "id")
		if startedTurnID != "" {
			a.syncTurnID(sessionID, startedTurnID)
		}
		a.resetTurnState(sessionID)
	case methodAgentMessageDelta:
		if !a.messageStreaming {
			return false, "", nil, nil, nil
		}
		itemID := stringValue(params, "itemId")
		delta := rawStringValue(params, "delta")
		if itemID == "" || delta == "" {
			return false, "", nil, nil, nil
		}
		if err := a.sendAgentMessageDelta(ctx, sessionID, itemID, delta); err != nil {
			return false, "", nil, nil, err
		}
	case methodReasoningTextDelta:
		if !a.reasoningStreaming || !a.reasoningThoughtsIncludeContent() {
			return false, "", nil, nil, nil
		}
		if err := a.handleReasoningDelta(ctx, sessionID, note.Method, params); err != nil {
			return false, "", nil, nil, err
		}
	case methodReasoningSummaryTextDelta:
		if !a.reasoningThoughtsIncludeSummary() {
			return false, "", nil, nil, nil
		}
		if err := a.handleReasoningDelta(ctx, sessionID, note.Method, params); err != nil {
			return false, "", nil, nil, err
		}
	case methodReasoningSummaryPartAdded:
		if !a.reasoningThoughtsIncludeSummary() {
			return false, "", nil, nil, nil
		}
		if err := a.handleReasoningSummaryPartAdded(ctx, sessionID, params); err != nil {
			return false, "", nil, nil, err
		}
	case "item/plan/delta":
		itemID := stringValue(params, "itemId")
		delta := rawStringValue(params, "delta")
		if itemID == "" || delta == "" {
			return false, "", nil, nil, nil
		}
		entries := a.appendPlanDelta(sessionID, itemID, delta)
		if len(entries) == 0 {
			return false, "", nil, nil, nil
		}
		if err := a.sendUpdate(ctx, sessionID, acp.UpdatePlan(entries...)); err != nil {
			return false, "", nil, nil, err
		}
	case "turn/plan/updated":
		entries := planEntriesFromNotification(params)
		a.resetPlanPreviewState(sessionID)
		if err := a.sendUpdate(ctx, sessionID, acp.UpdatePlan(entries...)); err != nil {
			return false, "", nil, nil, err
		}
	case methodItemStarted:
		item := mapValue(params, "item")
		if len(item) == 0 {
			return false, "", nil, nil, nil
		}
		itemType := stringValue(item, "type")
		itemID := stringValue(item, "id")
		if itemType == "" || itemID == "" {
			return false, "", nil, nil, nil
		}
		if itemType == "agentMessage" {
			a.noteAgentMessageStarted(sessionID, item)
			return false, "", nil, nil, nil
		}
		if itemType == "reasoning" {
			a.noteReasoningStarted(sessionID, item)
			return false, "", nil, nil, nil
		}
		if itemType == itemTypeMCPToolCall {
			a.notePendingMCPToolCall(sessionID, stringValue(item, "server"), itemID, stringValue(item, "tool"))
		}
		if !isToolLifecycleItemType(itemType) {
			return false, "", nil, nil, nil
		}
		title := toolCallTitle(itemType, item)
		update := acp.StartToolCall(
			toolCallID(itemID),
			title,
			acp.WithStartKind(toAppServerToolKind(itemType)),
			acp.WithStartStatus(toACPToolCallStatus(stringValue(item, "status"))),
			acp.WithStartRawInput(item),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case methodItemCompleted:
		item := mapValue(params, "item")
		if len(item) == 0 {
			return false, "", nil, nil, nil
		}
		itemType := stringValue(item, "type")
		if itemType == "agentMessage" {
			if err := a.handleCompletedAgentMessage(ctx, sessionID, item); err != nil {
				return false, "", nil, nil, err
			}
			return false, "", nil, nil, nil
		}
		if itemType == "reasoning" {
			if err := a.handleCompletedReasoning(ctx, sessionID, item); err != nil {
				return false, "", nil, nil, err
			}
			return false, "", nil, nil, nil
		}
		if itemType == "plan" {
			if err := a.handleCompletedPlan(ctx, sessionID, item); err != nil {
				return false, "", nil, nil, err
			}
			return false, "", nil, nil, nil
		}
		if !isToolLifecycleItemType(itemType) {
			return false, "", nil, nil, nil
		}
		itemID := stringValue(item, "id")
		if itemID == "" {
			return false, "", nil, nil, nil
		}
		if itemType == itemTypeMCPToolCall {
			a.clearPendingMCPToolCall(sessionID, stringValue(item, "server"))
		}
		status := toACPToolCallStatus(stringValue(item, "status"))
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(status),
			acp.WithUpdateRawOutput(item),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		itemID := stringValue(params, "itemId")
		delta := rawStringValue(params, "delta")
		if itemID == "" || delta == "" {
			return false, "", nil, nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(delta))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "item/fileChange/patchUpdated":
		itemID := stringValue(params, "itemId")
		patchText := fileChangePatchUpdatedText(params)
		if itemID == "" || patchText == "" {
			return false, "", nil, nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(patchText))}),
			acp.WithUpdateRawOutput(params),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "item/commandExecution/terminalInteraction":
		itemID := stringValue(params, "itemId")
		stdin := rawStringValue(params, "stdin")
		if itemID == "" || stdin == "" {
			return false, "", nil, nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(stdin))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "item/autoApprovalReview/started":
		targetItemID := stringValue(params, "targetItemId")
		if targetItemID == "" {
			return false, "", nil, nil, nil
		}
		review := mapValue(params, "review")
		title := "auto approval review"
		if status := stringValue(review, "status"); status != "" {
			title = fmt.Sprintf("auto approval review (%s)", status)
		}
		start := acp.StartToolCall(
			guardianToolCallID(targetItemID),
			title,
			acp.WithStartKind(acp.ToolKindOther),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(params),
		)
		if err := a.sendUpdate(ctx, sessionID, start); err != nil {
			return false, "", nil, nil, err
		}
		if summary := guardianReviewSummary(review); summary != "" {
			update := acp.UpdateToolCall(
				guardianToolCallID(targetItemID),
				acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
			if err := a.sendUpdate(ctx, sessionID, update); err != nil {
				return false, "", nil, nil, err
			}
		}
	case "item/autoApprovalReview/completed":
		targetItemID := stringValue(params, "targetItemId")
		if targetItemID == "" {
			return false, "", nil, nil, nil
		}
		review := mapValue(params, "review")
		update := acp.UpdateToolCall(
			guardianToolCallID(targetItemID),
			acp.WithUpdateStatus(guardianReviewStatusToACPStatus(stringValue(review, "status"))),
			acp.WithUpdateRawOutput(params),
		)
		if summary := guardianReviewSummary(review); summary != "" {
			update = acp.UpdateToolCall(
				guardianToolCallID(targetItemID),
				acp.WithUpdateStatus(guardianReviewStatusToACPStatus(stringValue(review, "status"))),
				acp.WithUpdateRawOutput(params),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
		}
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "hook/started":
		run := mapValue(params, "run")
		runID := stringValue(run, "id")
		if runID == "" {
			return false, "", nil, nil, nil
		}
		start := acp.StartToolCall(
			hookToolCallID(runID),
			hookRunTitle(run),
			acp.WithStartKind(acp.ToolKindExecute),
			acp.WithStartStatus(acp.ToolCallStatusInProgress),
			acp.WithStartRawInput(run),
		)
		if err := a.sendUpdate(ctx, sessionID, start); err != nil {
			return false, "", nil, nil, err
		}
	case "hook/completed":
		run := mapValue(params, "run")
		runID := stringValue(run, "id")
		if runID == "" {
			return false, "", nil, nil, nil
		}
		update := acp.UpdateToolCall(
			hookToolCallID(runID),
			acp.WithUpdateStatus(hookRunStatusToACPStatus(stringValue(run, "status"))),
			acp.WithUpdateRawOutput(run),
		)
		if summary := hookRunSummary(run); summary != "" {
			update = acp.UpdateToolCall(
				hookToolCallID(runID),
				acp.WithUpdateStatus(hookRunStatusToACPStatus(stringValue(run, "status"))),
				acp.WithUpdateRawOutput(run),
				acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(summary))}),
			)
		}
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "item/mcpToolCall/progress":
		itemID := stringValue(params, "itemId")
		message := rawStringValue(params, "message")
		if itemID == "" || message == "" {
			return false, "", nil, nil, nil
		}
		update := acp.UpdateToolCall(
			toolCallID(itemID),
			acp.WithUpdateStatus(acp.ToolCallStatusInProgress),
			acp.WithUpdateContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock(message))}),
		)
		if err := a.sendUpdate(ctx, sessionID, update); err != nil {
			return false, "", nil, nil, err
		}
	case "serverRequest/resolved":
		requestID, ok := requestIDFromAny(params["requestId"])
		if ok {
			a.resolvePendingRequest(sessionID, requestID)
		}
		a.clearPendingMCPToolCalls(sessionID)
	case "mcpServer/startupStatus/updated":
		name := stringValue(params, "name")
		status := stringValue(params, "status")
		errText := stringValue(params, "error")
		a.setSessionMCPStartupStatus(sessionID, name, status, errText)
	case "mcpServer/oauthLogin/completed":
	case "model/rerouted":
	case "model/verification":
	case "configWarning":
	case "deprecationNotice":
	case "account/login/completed":
	case "account/updated":
	case "app/list/updated":
	case "skills/changed":
	case "externalAgentConfig/import/completed":
	case "guardianWarning":
	case "warning":
	case "thread/compacted":
	case "thread/archived":
	case "thread/unarchived":
	case "thread/closed":
	case "thread/name/updated":
	case "windows/worldWritableWarning":
	case "windowsSandbox/setupCompleted":
	case "thread/realtime/started":
	case "thread/realtime/itemAdded":
	case "thread/realtime/outputAudio/delta":
	case "thread/realtime/transcriptUpdated":
	case "thread/realtime/transcript/delta":
	case "thread/realtime/transcript/done":
	case "thread/realtime/sdp":
	case "thread/realtime/error":
	case "thread/realtime/closed":
	case "fs/changed":
	case "fuzzyFileSearch/sessionUpdated":
	case "fuzzyFileSearch/sessionCompleted":
	case "command/exec/outputDelta":
	case "account/rateLimits/updated":
		rateLimits := mapValue(params, "rateLimits")
		if len(rateLimits) > 0 {
			a.setSessionRateLimits(sessionID, rateLimits)
		}
	case "thread/tokenUsage/updated":
		usage = usageFromTokenNotification(params)
		if update := sessionUsageUpdateFromTokenNotification(params); update != nil {
			if err := a.sendUpdate(ctx, sessionID, acp.SessionUpdate{UsageUpdate: update}); err != nil {
				return false, "", nil, nil, err
			}
		}
	case methodTurnCompleted:
		if sessionID != "" {
			a.clearPendingRequests(sessionID)
		}
		turn := mapValue(params, "turn")
		status := stringValue(turn, "status")
		stopReason := stopReasonFromTurnStatus(status)
		errorMeta := turnErrorMeta(turn, status)
		if errorMeta != nil {
			a.logTerminalPromptError(methodTurnCompleted, sessionID, threadID, turnID, stopReason, errorMeta)
		}
		return true, stopReason, usageFromTokenNotification(params), errorMeta, nil
	}
	return false, "", usage, nil, nil
}

func (a *codexACPProxyAgent) handleServerRequest(ctx context.Context, sessionID acp.SessionId, req *appServerRequest) error {
	if req == nil {
		return nil
	}

	a.mu.Lock()
	state := a.sessions[sessionID]
	if state == nil || state.backend == nil {
		a.mu.Unlock()
		return acp.NewInvalidParams(errSessionBackendUnavailable)
	}
	backend := state.backend
	a.mu.Unlock()
	requestID := canonicalRequestID(req.ID)
	if requestID != "" {
		a.markPendingRequest(sessionID, requestID, req.Method)
	}

	switch req.Method {
	case "item/commandExecution/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Command approval", acp.ToolKindExecute, params, listValue(params, "availableDecisions"), []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decision})
	case "item/fileChange/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "File change approval", acp.ToolKindEdit, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decision})
	case "item/permissions/requestApproval":
		params, _ := decodeJSONMap(req.Params)
		requestedPermissions := mapValue(params, "permissions")
		decision, err := a.requestDecision(ctx, sessionID, "Permissions approval", acp.ToolKindOther, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		switch decisionName {
		case decisionAcceptForSession:
			return backend.RespondRequest(ctx, req, map[string]any{
				"permissions": requestedPermissions,
				"scope":       "session",
			})
		case decisionAccept:
			return backend.RespondRequest(ctx, req, map[string]any{
				"permissions": requestedPermissions,
				"scope":       "turn",
			})
		default:
			return backend.RespondRequest(ctx, req, map[string]any{
				"permissions": map[string]any{},
				"scope":       "turn",
			})
		}
	case "item/tool/call":
		params, _ := decodeJSONMap(req.Params)
		toolName := stringValue(params, "tool")
		title := "Tool call request"
		if toolName != "" {
			title = fmt.Sprintf("Tool call request: %s", toolName)
		}
		decision, err := a.requestDecision(ctx, sessionID, title, acp.ToolKindExecute, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		statusText := "declined by user"
		switch decisionName {
		case decisionAccept, decisionAcceptForSession:
			statusText = "approved by user, not executed by ACP bridge"
		case decisionCancel:
			statusText = "cancelled by user"
		}
		return backend.RespondRequest(ctx, req, map[string]any{
			"success": false,
			"contentItems": []any{
				map[string]any{
					"type": "inputText",
					"text": "Dynamic tool call " + statusText + ".",
				},
			},
		})
	case "item/tool/requestUserInput":
		params, _ := decodeJSONMap(req.Params)
		answers := map[string]any{}
		for _, rawQuestion := range listValue(params, "questions") {
			question, ok := rawQuestion.(map[string]any)
			if !ok {
				continue
			}
			questionID := stringValue(question, "id")
			if questionID == "" {
				continue
			}
			title := "User input request"
			if header := stringValue(question, "header"); header != "" {
				title = "User input: " + header
			}
			decisions := questionDecisionOptions(question)
			selected, err := a.requestDecision(ctx, sessionID, title, acp.ToolKindOther, mergeRequestInput(params, map[string]any{"question": question}), decisions, nil)
			if err != nil {
				return err
			}
			answerValues := []string{}
			selectedText, _ := selected.(string)
			if selectedText != "" && selectedText != decisionCancel && selectedText != decisionDecline {
				answerValues = append(answerValues, selectedText)
			}
			answers[questionID] = map[string]any{"answers": answerValues}
		}
		return backend.RespondRequest(ctx, req, map[string]any{"answers": answers})
	case "mcpServer/elicitation/request":
		params, _ := decodeJSONMap(req.Params)
		if stringValue(params, "mode") == mcpElicitationModeOpenAIForm {
			a.logger.Warn().Str("server", stringValue(params, "serverName")).Msg("unsupported MCP openai/form elicitation")
			return backend.RespondRequest(ctx, req, mcpElicitationResponse(decisionCancel, ""))
		}
		if isMCPToolApproval(params) {
			return a.handleMCPToolApproval(ctx, sessionID, backend, req, params)
		}
		return backend.RespondRequest(ctx, req, a.forwardMCPElicitation(ctx, req, params))
	case "applyPatchApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Patch approval", acp.ToolKindEdit, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return backend.RespondRequest(ctx, req, map[string]any{
			"decision": legacyApprovalDecision(decision),
		})
	case "execCommandApproval":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "Exec command approval", acp.ToolKindExecute, params, nil, []any{decisionAccept, decisionAcceptForSession, decisionDecline, decisionCancel})
		if err != nil {
			return err
		}
		return backend.RespondRequest(ctx, req, map[string]any{
			"decision": legacyApprovalDecision(decision),
		})
	case "account/chatgptAuthTokens/refresh":
		params, _ := decodeJSONMap(req.Params)
		decision, err := a.requestDecision(ctx, sessionID, "ChatGPT token refresh", acp.ToolKindOther, params, nil, []any{decisionAccept, decisionCancel})
		if err != nil {
			return err
		}
		decisionName, _ := decision.(string)
		if decisionName == decisionCancel || decisionName == decisionDecline {
			return backend.RespondRequestError(ctx, req, -32000, "chatgpt token refresh declined", nil)
		}
		resp, ok := chatgptAuthTokensFromEnv()
		if !ok {
			return backend.RespondRequestError(
				ctx,
				req,
				-32001,
				"chatgpt token refresh unavailable: set CODEX_CHATGPT_ACCESS_TOKEN and CODEX_CHATGPT_ACCOUNT_ID",
				nil,
			)
		}
		return backend.RespondRequest(ctx, req, resp)
	default:
		return a.respondWithFallback(ctx, backend, req)
	}
}

const (
	mcpElicitationModeForm       = "form"
	mcpElicitationModeOpenAIForm = "openai/form"

	mcpApprovalOptionAllowOnce    acp.PermissionOptionId = "mcp-allow-once"
	mcpApprovalOptionAllowSession acp.PermissionOptionId = "mcp-allow-session"
	mcpApprovalOptionAllowAlways  acp.PermissionOptionId = "mcp-allow-always"
	mcpApprovalOptionDecline      acp.PermissionOptionId = "mcp-decline"
)

func isMCPToolApproval(params map[string]any) bool {
	mode := stringValue(params, "mode")
	return (mode == mcpElicitationModeForm || mode == mcpElicitationModeOpenAIForm) &&
		stringValue(mapValue(params, "_meta"), "codex_approval_kind") == "mcp_tool_call"
}

func (a *codexACPProxyAgent) handleMCPToolApproval(
	ctx context.Context,
	sessionID acp.SessionId,
	backend appServerSession,
	req *appServerRequest,
	params map[string]any,
) error {
	policy := a.mcpApprovalPolicy
	serverName := stringValue(params, "serverName")
	toolName := a.pendingMCPToolName(sessionID, serverName)
	switch policy {
	case MCPApprovalPolicyAllow:
		a.logMCPApprovalDecision(policy, decisionAccept, sessionID, params, toolName)
		a.clearPendingMCPToolCall(sessionID, serverName)
		return backend.RespondRequest(ctx, req, mcpElicitationResponse(decisionAccept, ""))
	case MCPApprovalPolicyDeny:
		a.logMCPApprovalDecision(policy, decisionDecline, sessionID, params, toolName)
		a.clearPendingMCPToolCall(sessionID, serverName)
		return backend.RespondRequest(ctx, req, mcpElicitationResponse(decisionDecline, ""))
	case MCPApprovalPolicyAsk:
		return backend.RespondRequest(ctx, req, a.askMCPToolApproval(ctx, sessionID, req, params))
	default:
		return backend.RespondRequest(ctx, req, mcpElicitationResponse(decisionCancel, ""))
	}
}

func (a *codexACPProxyAgent) askMCPToolApproval(ctx context.Context, sessionID acp.SessionId, req *appServerRequest, params map[string]any) map[string]any {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return mcpElicitationResponse(decisionCancel, "")
	}

	options := mcpApprovalOptions(mapValue(params, "_meta"))
	request := acp.RequestPermissionRequest{
		SessionId: sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: a.mcpApprovalToolCallID(sessionID, req, params),
			Title:      acp.Ptr("MCP tool-call approval"),
			Kind:       acp.Ptr(acp.ToolKindOther),
			Content:    mcpApprovalContent(params),
			RawInput:   params,
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: options,
	}
	response, err := conn.RequestPermission(ctx, request)
	if err != nil || response.Outcome.Selected == nil {
		return mcpElicitationResponse(decisionCancel, "")
	}

	switch response.Outcome.Selected.OptionId {
	case mcpApprovalOptionAllowOnce:
		return mcpElicitationResponse(decisionAccept, "")
	case mcpApprovalOptionAllowSession:
		return mcpElicitationResponse(decisionAccept, "session")
	case mcpApprovalOptionAllowAlways:
		return mcpElicitationResponse(decisionAccept, "always")
	case mcpApprovalOptionDecline:
		return mcpElicitationResponse(decisionDecline, "")
	default:
		return mcpElicitationResponse(decisionCancel, "")
	}
}

func (a *codexACPProxyAgent) mcpApprovalToolCallID(sessionID acp.SessionId, req *appServerRequest, params map[string]any) acp.ToolCallId {
	serverName := stringValue(params, "serverName")
	a.mu.Lock()
	itemID := ""
	if state := a.sessions[sessionID]; state != nil {
		itemID = state.pendingMCPToolCalls[serverName].itemID
		delete(state.pendingMCPToolCalls, serverName)
	}
	a.mu.Unlock()
	if itemID != "" {
		return toolCallID(itemID)
	}
	requestID := ""
	if req != nil {
		requestID = canonicalRequestID(req.ID)
	}
	return syntheticToolCallID("mcp-elicitation", requestID)
}

func mcpApprovalOptions(meta map[string]any) []acp.PermissionOption {
	options := []acp.PermissionOption{
		{OptionId: mcpApprovalOptionAllowOnce, Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce},
	}
	if mcpApprovalPersistSupported(meta, "session") {
		options = append(options, acp.PermissionOption{OptionId: mcpApprovalOptionAllowSession, Name: "Allow for this session", Kind: acp.PermissionOptionKindAllowAlways})
	}
	if mcpApprovalPersistSupported(meta, "always") {
		options = append(options, acp.PermissionOption{OptionId: mcpApprovalOptionAllowAlways, Name: "Allow and don't ask again", Kind: acp.PermissionOptionKindAllowAlways})
	}
	return append(options, acp.PermissionOption{OptionId: mcpApprovalOptionDecline, Name: "Decline", Kind: acp.PermissionOptionKindRejectOnce})
}

func mcpApprovalPersistSupported(meta map[string]any, scope string) bool {
	switch value := meta["persist"].(type) {
	case string:
		return value == scope
	case []any:
		for _, item := range value {
			if item == scope {
				return true
			}
		}
	}
	return false
}

func mcpElicitationResponse(action, persist string) map[string]any {
	response := map[string]any{"action": action, "content": nil, "_meta": nil}
	if persist != "" {
		response["_meta"] = map[string]any{"persist": persist}
	}
	return response
}

func mcpApprovalContent(params map[string]any) []acp.ToolCallContent {
	message := strings.TrimSpace(stringValue(params, "message"))
	if message == "" {
		return nil
	}
	return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(message))}
}

func (a *codexACPProxyAgent) logMCPApprovalDecision(policy MCPApprovalPolicy, decision string, sessionID acp.SessionId, params map[string]any, toolName string) {
	a.logger.Info().
		Str("policy", string(policy)).
		Str("decision", decision).
		Str("session_id", string(sessionID)).
		Str("thread_id", stringValue(params, "threadId")).
		Str("server", stringValue(params, "serverName")).
		Str("tool", toolName).
		Msg("mcp approval decision")
}

func (a *codexACPProxyAgent) forwardMCPElicitation(ctx context.Context, req *appServerRequest, params map[string]any) map[string]any {
	mode := stringValue(params, "mode")
	if !a.supportsMCPElicitation(mode) {
		a.logger.Warn().Str("mode", mode).Str("server", stringValue(params, "serverName")).Msg("MCP elicitation is unsupported by ACP client")
		return mcpElicitationResponse(decisionCancel, "")
	}
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return mcpElicitationResponse(decisionCancel, "")
	}
	request, ok := mcpElicitationRequest(req, params)
	if !ok {
		a.logger.Warn().Str("mode", mode).Str("server", stringValue(params, "serverName")).Msg("invalid MCP elicitation payload")
		return mcpElicitationResponse(decisionCancel, "")
	}
	response, err := conn.UnstableCreateElicitation(ctx, request)
	if err != nil {
		return mcpElicitationResponse(decisionCancel, "")
	}
	return mcpElicitationResponseFromACP(response)
}

func (a *codexACPProxyAgent) supportsMCPElicitation(mode string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.clientCapabilities.Elicitation == nil {
		return false
	}
	switch mode {
	case mcpElicitationModeForm:
		return a.clientCapabilities.Elicitation.Form != nil
	case "url":
		return a.clientCapabilities.Elicitation.Url != nil
	default:
		return false
	}
}

func mcpElicitationRequest(req *appServerRequest, params map[string]any) (acp.UnstableCreateElicitationRequest, bool) {
	mode := stringValue(params, "mode")
	message := stringValue(params, "message")
	if message == "" {
		return acp.UnstableCreateElicitationRequest{}, false
	}
	meta := cloneAnyMap(mapValue(params, "_meta"))
	elicitationID := "mcp-elicitation"
	if req != nil {
		if requestID := canonicalRequestID(req.ID); requestID != "" {
			elicitationID = requestID
		}
	}
	switch mode {
	case mcpElicitationModeForm:
		rawSchema, err := json.Marshal(params["requestedSchema"])
		if err != nil {
			return acp.UnstableCreateElicitationRequest{}, false
		}
		var schema acp.UnstableElicitationSchema
		if err := json.Unmarshal(rawSchema, &schema); err != nil {
			return acp.UnstableCreateElicitationRequest{}, false
		}
		return acp.UnstableCreateElicitationRequest{Form: &acp.UnstableCreateElicitationForm{Meta: meta, Message: message, Mode: mode, RequestedSchema: schema}}, true
	case "url":
		url := stringValue(params, "url")
		if url == "" {
			return acp.UnstableCreateElicitationRequest{}, false
		}
		return acp.UnstableCreateElicitationRequest{Url: &acp.UnstableCreateElicitationUrl{Meta: meta, ElicitationId: acp.UnstableElicitationId(elicitationID), Message: message, Mode: mode, Url: url}}, true
	default:
		return acp.UnstableCreateElicitationRequest{}, false
	}
}

func mcpElicitationResponseFromACP(response acp.UnstableCreateElicitationResponse) map[string]any {
	switch {
	case response.Accept != nil:
		return map[string]any{"action": decisionAccept, "content": response.Accept.Content, "_meta": response.Accept.Meta}
	case response.Decline != nil:
		return map[string]any{"action": decisionDecline, "content": nil, "_meta": response.Decline.Meta}
	case response.Cancel != nil:
		return map[string]any{"action": decisionCancel, "content": nil, "_meta": response.Cancel.Meta}
	default:
		return mcpElicitationResponse(decisionCancel, "")
	}
}

func (a *codexACPProxyAgent) respondWithFallback(ctx context.Context, backend appServerSession, req *appServerRequest) error {
	if backend == nil || req == nil {
		return nil
	}
	switch req.Method {
	case "item/commandExecution/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decisionCancel})
	case "item/fileChange/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{"decision": decisionDecline})
	case "item/permissions/requestApproval":
		return backend.RespondRequest(ctx, req, map[string]any{
			"permissions": map[string]any{},
			"scope":       "turn",
		})
	default:
		return backend.RespondRequestError(ctx, req, -32601, "unsupported server request", map[string]any{"method": req.Method})
	}
}

func (a *codexACPProxyAgent) requestDecision(
	ctx context.Context,
	sessionID acp.SessionId,
	title string,
	toolKind acp.ToolKind,
	rawInput map[string]any,
	availableDecisions []any,
	defaultDecisions []any,
) (any, error) {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return nil, errors.New("acp connection is not initialized")
	}

	decisions := availableDecisions
	if len(decisions) == 0 {
		decisions = defaultDecisions
	}
	options, byOptionID := permissionOptions(decisions)
	if len(options) == 0 {
		options, byOptionID = permissionOptions(defaultDecisions)
	}

	req := acp.RequestPermissionRequest{
		SessionId: sessionID,
		ToolCall: acp.ToolCallUpdate{
			ToolCallId: permissionToolCallID(rawInput),
			Title:      acp.Ptr(title),
			Kind:       acp.Ptr(toolKind),
			Content:    permissionRequestContent(toolKind, rawInput),
			RawInput:   rawInput,
			Status:     acp.Ptr(acp.ToolCallStatusPending),
		},
		Options: options,
	}
	resp, err := conn.RequestPermission(ctx, req)
	if err != nil {
		return decisionCancel, nil
	}
	if resp.Outcome.Selected == nil || resp.Outcome.Selected.OptionId == "" {
		return decisionCancel, nil
	}
	decision := byOptionID[resp.Outcome.Selected.OptionId]
	if decision == nil {
		return decisionCancel, nil
	}
	return decision, nil
}

func permissionRequestContent(toolKind acp.ToolKind, rawInput map[string]any) []acp.ToolCallContent {
	var content strings.Builder
	if reason := strings.TrimSpace(stringValue(rawInput, "reason")); reason != "" {
		content.WriteString(reason)
	}
	if toolKind == acp.ToolKindExecute {
		if command := strings.TrimSpace(stringValue(rawInput, "command")); command != "" {
			if content.Len() > 0 {
				content.WriteString("\n\n")
			}
			content.WriteString("Command:\n```sh\n")
			content.WriteString(strings.ReplaceAll(command, "```", "` ` `"))
			content.WriteString("\n```")
		}
		if cwd := strings.TrimSpace(stringValue(rawInput, "cwd")); cwd != "" {
			if content.Len() > 0 {
				content.WriteString("\n\n")
			}
			content.WriteString("Working directory: `")
			content.WriteString(strings.ReplaceAll(cwd, "`", "'"))
			content.WriteString("`")
		}
	}
	if content.Len() == 0 {
		return nil
	}
	return []acp.ToolCallContent{acp.ToolContent(acp.TextBlock(content.String()))}
}

func permissionOptions(decisions []any) ([]acp.PermissionOption, map[acp.PermissionOptionId]any) {
	options := make([]acp.PermissionOption, 0, len(decisions))
	byOptionID := make(map[acp.PermissionOptionId]any, len(decisions))
	for i, decision := range decisions {
		optionID := acp.PermissionOptionId(fmt.Sprintf("opt-%d", i+1))
		option := acp.PermissionOption{
			OptionId: optionID,
			Name:     permissionOptionLabel(decision),
			Kind:     permissionOptionKind(decision),
		}
		options = append(options, option)
		byOptionID[optionID] = decision
	}
	return options, byOptionID
}

func permissionOptionKind(decision any) acp.PermissionOptionKind {
	switch v := decision.(type) {
	case string:
		switch strings.TrimSpace(v) {
		case decisionAccept:
			return acp.PermissionOptionKindAllowOnce
		case decisionAcceptForSession:
			return acp.PermissionOptionKindAllowAlways
		case decisionDecline, decisionCancel:
			return acp.PermissionOptionKindRejectOnce
		case decisionApproved:
			return acp.PermissionOptionKindAllowOnce
		case decisionApprovedSession:
			return acp.PermissionOptionKindAllowAlways
		case decisionDenied, decisionAbort:
			return acp.PermissionOptionKindRejectOnce
		default:
			return acp.PermissionOptionKindAllowOnce
		}
	default:
		return acp.PermissionOptionKindAllowAlways
	}
}

func permissionOptionLabel(decision any) string {
	switch v := decision.(type) {
	case string:
		switch strings.TrimSpace(v) {
		case decisionAccept:
			return "Allow once"
		case decisionAcceptForSession:
			return "Allow for session"
		case decisionDecline:
			return "Decline"
		case decisionCancel:
			return "Cancel"
		case decisionApproved:
			return "Approve once"
		case decisionApprovedSession:
			return "Approve for session"
		case decisionDenied:
			return "Deny"
		case decisionAbort:
			return "Abort"
		default:
			return strings.TrimSpace(v)
		}
	default:
		return "Allow"
	}
}

func questionDecisionOptions(question map[string]any) []any {
	questionOptions := listValue(question, "options")
	if len(questionOptions) == 0 {
		return []any{decisionAccept, decisionDecline, decisionCancel}
	}
	decisions := make([]any, 0, len(questionOptions)+1)
	for _, raw := range questionOptions {
		opt, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		label := stringValue(opt, "label")
		if label == "" {
			continue
		}
		decisions = append(decisions, label)
	}
	decisions = append(decisions, decisionCancel)
	return decisions
}

func mergeRequestInput(base map[string]any, extra map[string]any) map[string]any {
	out := cloneAnyMap(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func legacyApprovalDecision(decision any) string {
	decisionName, _ := decision.(string)
	switch strings.TrimSpace(decisionName) {
	case decisionAcceptForSession:
		return decisionApprovedSession
	case decisionDecline:
		return decisionDenied
	case decisionCancel:
		return decisionAbort
	default:
		return decisionApproved
	}
}

func chatgptAuthTokensFromEnv() (map[string]any, bool) {
	accessToken := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_ACCESS_TOKEN"))
	accountID := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_ACCOUNT_ID"))
	if accessToken == "" || accountID == "" {
		return nil, false
	}
	resp := map[string]any{
		"accessToken":      accessToken,
		"chatgptAccountId": accountID,
	}
	if planType := strings.TrimSpace(os.Getenv("CODEX_CHATGPT_PLAN_TYPE")); planType != "" {
		resp["chatgptPlanType"] = planType
	}
	return resp, true
}

func (a *codexACPProxyAgent) sendUpdate(ctx context.Context, sessionID acp.SessionId, update acp.SessionUpdate) error {
	a.connMu.RLock()
	conn := a.conn
	a.connMu.RUnlock()
	if conn == nil {
		return errors.New("acp connection is not initialized")
	}
	return conn.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    update,
	})
}

func decodeJSONMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func mapValue(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	typed, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return typed
}

func listValue(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	typed, ok := v.([]any)
	if !ok {
		return nil
	}
	return typed
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func rawStringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func boolValue(m map[string]any, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	typed, ok := v.(bool)
	if !ok {
		return false, false
	}
	return typed, true
}

func int64Value(m map[string]any, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch typed := v.(type) {
	case float64:
		return int64(typed), true
	case float32:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	default:
		return 0, false
	}
}

func matchesThreadID(params map[string]any, threadID string) bool {
	if strings.TrimSpace(threadID) == "" {
		return true
	}
	if got := stringValue(params, "threadId"); got != "" && got != strings.TrimSpace(threadID) {
		return false
	}
	return true
}

func matchesTurnID(params map[string]any, turnID string) bool {
	if strings.TrimSpace(turnID) == "" {
		return true
	}
	if got := stringValue(params, "turnId"); got != "" && got != strings.TrimSpace(turnID) {
		return false
	}
	return true
}

func requiresActiveTurn(method string) bool {
	switch strings.TrimSpace(method) {
	case "error":
		return true
	case methodTurnStarted:
		return true
	case methodAgentMessageDelta:
		return true
	case methodReasoningTextDelta, methodReasoningSummaryTextDelta:
		return true
	case methodReasoningSummaryPartAdded:
		return true
	case "item/plan/delta":
		return true
	case "turn/plan/updated":
		return true
	case "turn/diff/updated":
		return true
	case "item/started":
		return true
	case "item/completed":
		return true
	case "item/commandExecution/outputDelta", "item/fileChange/outputDelta":
		return true
	case "item/fileChange/patchUpdated":
		return true
	case "item/commandExecution/terminalInteraction":
		return true
	case "item/autoApprovalReview/started", "item/autoApprovalReview/completed":
		return true
	case "hook/started", "hook/completed":
		return true
	case "item/mcpToolCall/progress":
		return true
	case "model/rerouted":
		return true
	case "model/verification":
		return true
	case "thread/tokenUsage/updated":
		return true
	case methodTurnCompleted:
		return true
	default:
		return false
	}
}

func planEntriesFromNotification(params map[string]any) []acp.PlanEntry {
	planSteps := listValue(params, "plan")
	if len(planSteps) == 0 {
		return nil
	}
	entries := make([]acp.PlanEntry, 0, len(planSteps))
	for _, rawStep := range planSteps {
		stepMap, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		stepText := stringValue(stepMap, "step")
		if stepText == "" {
			continue
		}
		entries = append(entries, acp.PlanEntry{
			Content:  stepText,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   toACPPlanStatus(stringValue(stepMap, "status")),
		})
	}
	return entries
}

func fileChangePatchUpdatedText(params map[string]any) string {
	changes := listValue(params, "changes")
	if len(changes) == 0 {
		return ""
	}
	diffs := make([]string, 0, len(changes))
	for _, rawChange := range changes {
		change, ok := rawChange.(map[string]any)
		if !ok {
			continue
		}
		diff := rawStringValue(change, "diff")
		if diff == "" {
			continue
		}
		diffs = append(diffs, diff)
	}
	return strings.Join(diffs, "\n")
}

func usageFromTokenNotification(params map[string]any) map[string]any {
	tokenUsage := mapValue(params, "tokenUsage")
	if len(tokenUsage) == 0 {
		return nil
	}
	last := mapValue(tokenUsage, "last")
	if len(last) == 0 {
		return nil
	}
	usage := map[string]any{}
	if v, ok := last["inputTokens"]; ok {
		usage["inputTokens"] = v
	}
	if v, ok := last["outputTokens"]; ok {
		usage["outputTokens"] = v
	}
	if v, ok := last["totalTokens"]; ok {
		usage["totalTokens"] = v
	}
	if v, ok := last["cachedInputTokens"]; ok {
		usage["cachedReadTokens"] = v
	}
	if v, ok := last["reasoningOutputTokens"]; ok {
		usage["thoughtTokens"] = v
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func sessionUsageUpdateFromTokenNotification(params map[string]any) *acp.SessionUsageUpdate {
	tokenUsage := mapValue(params, "tokenUsage")
	if len(tokenUsage) == 0 {
		return nil
	}
	size, sizeOK := int64Value(tokenUsage, "modelContextWindow")
	total := mapValue(tokenUsage, "total")
	used, usedOK := int64Value(total, "totalTokens")
	if !sizeOK || !usedOK {
		return nil
	}
	return &acp.SessionUsageUpdate{
		SessionUpdate: "usage_update",
		Size:          int(size),
		Used:          int(used),
	}
}

func acpUsageFromMap(usage map[string]any) *acp.Usage {
	if len(usage) == 0 {
		return nil
	}
	out := &acp.Usage{}
	found := false
	if v, ok := int64Value(usage, "inputTokens"); ok {
		out.InputTokens = int(v)
		found = true
	}
	if v, ok := int64Value(usage, "outputTokens"); ok {
		out.OutputTokens = int(v)
		found = true
	}
	if v, ok := int64Value(usage, "totalTokens"); ok {
		out.TotalTokens = int(v)
		found = true
	}
	if v, ok := int64Value(usage, "cachedReadTokens"); ok {
		cachedReadTokens := int(v)
		out.CachedReadTokens = &cachedReadTokens
		found = true
	}
	if v, ok := int64Value(usage, "thoughtTokens"); ok {
		thoughtTokens := int(v)
		out.ThoughtTokens = &thoughtTokens
		found = true
	}
	if !found {
		return nil
	}
	return out
}

func stopReasonFromTurnStatus(status string) acp.StopReason {
	switch strings.TrimSpace(status) {
	case "interrupted":
		return acp.StopReasonCancelled
	default:
		return acp.StopReasonEndTurn
	}
}

func turnErrorMeta(turn map[string]any, status string) any {
	if strings.TrimSpace(status) != statusFailed || len(turn) == 0 {
		return nil
	}
	return turn["error"]
}

func (a *codexACPProxyAgent) logTerminalPromptError(source string, sessionID acp.SessionId, threadID string, turnID string, stopReason acp.StopReason, errorMeta any) {
	if errorMeta == nil || a.logger == nil {
		return
	}
	event := a.logger.Warn().
		Str("source", source).
		Str("session_id", string(sessionID)).
		Str("thread_id", strings.TrimSpace(threadID)).
		Str("turn_id", strings.TrimSpace(turnID)).
		Str("stop_reason", string(stopReason)).
		Interface("error_meta", cloneJSONValue(errorMeta))
	if errMap, ok := errorMeta.(map[string]any); ok {
		if msg := strings.TrimSpace(stringValue(errMap, "message")); msg != "" {
			event = event.Str("error_message", msg)
		}
		if code := strings.TrimSpace(terminalErrorCodeForLog(errMap)); code != "" {
			event = event.Str("error_code", code)
		}
	}
	event.Msg("prompt completed with terminal error")
}

func terminalErrorCodeForLog(errMeta map[string]any) string {
	if len(errMeta) == 0 {
		return ""
	}
	for _, key := range []string{"code", "kind", "type", "codexErrorInfo"} {
		value, ok := errMeta[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case map[string]any:
			if len(typed) == 1 {
				for nested := range typed {
					if text := strings.TrimSpace(nested); text != "" {
						return text
					}
				}
			}
		}
	}
	return ""
}

func findAppServerModel(models []appServerModel, modelID string) (appServerModel, bool) {
	trimmedModelID := strings.TrimSpace(modelID)
	if trimmedModelID == "" {
		return appServerModel{}, false
	}
	for _, model := range models {
		if strings.TrimSpace(model.ID) == trimmedModelID {
			return model, true
		}
	}
	return appServerModel{}, false
}

func toolCallID(itemID string) acp.ToolCallId {
	trimmed := strings.TrimSpace(itemID)
	if trimmed == "" {
		return acp.ToolCallId("codex-item-unknown")
	}
	return acp.ToolCallId("codex-item-" + trimmed)
}

func permissionToolCallID(rawInput map[string]any) acp.ToolCallId {
	if itemID := stringValue(rawInput, "itemId"); itemID != "" {
		return toolCallID(itemID)
	}
	if toolName := stringValue(rawInput, "tool"); toolName != "" {
		return acp.ToolCallId("codex-tool-" + toolName)
	}
	if patchID := stringValue(rawInput, "patchId"); patchID != "" {
		return acp.ToolCallId("codex-patch-" + patchID)
	}
	if command := stringValue(rawInput, "command"); command != "" {
		return acp.ToolCallId("codex-cmd-" + command)
	}
	if serverName := stringValue(rawInput, "serverName"); serverName != "" {
		return acp.ToolCallId("codex-mcp-" + serverName)
	}
	return acp.ToolCallId("codex-permission-unknown")
}

func guardianToolCallID(targetItemID string) acp.ToolCallId {
	return syntheticToolCallID("guardian", targetItemID)
}

func hookToolCallID(runID string) acp.ToolCallId {
	return syntheticToolCallID("hook", runID)
}

func syntheticToolCallID(prefix string, value string) acp.ToolCallId {
	trimmedPrefix := strings.TrimSpace(prefix)
	trimmedValue := strings.TrimSpace(value)
	if trimmedPrefix == "" {
		trimmedPrefix = "synthetic"
	}
	if trimmedValue == "" {
		trimmedValue = "unknown"
	}
	return acp.ToolCallId("codex-" + trimmedPrefix + "-" + trimmedValue)
}

func guardianReviewStatusToACPStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case "approved":
		return acp.ToolCallStatusCompleted
	case "denied", "aborted":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func guardianReviewSummary(review map[string]any) string {
	if len(review) == 0 {
		return ""
	}
	status := stringValue(review, "status")
	riskLevel := stringValue(review, "riskLevel")
	rationale := stringValue(review, "rationale")
	parts := make([]string, 0, 3)
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if riskLevel != "" {
		parts = append(parts, "risk="+riskLevel)
	}
	if rationale != "" {
		parts = append(parts, rationale)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func hookRunStatusToACPStatus(status string) acp.ToolCallStatus {
	switch strings.TrimSpace(status) {
	case "completed":
		return acp.ToolCallStatusCompleted
	case "failed", "blocked", "stopped":
		return acp.ToolCallStatusFailed
	default:
		return acp.ToolCallStatusInProgress
	}
}

func hookRunTitle(run map[string]any) string {
	eventName := stringValue(run, "eventName")
	if eventName == "" {
		return "hook"
	}
	return "hook " + eventName
}

func hookRunSummary(run map[string]any) string {
	if len(run) == 0 {
		return ""
	}
	status := stringValue(run, "status")
	statusMessage := stringValue(run, "statusMessage")
	parts := make([]string, 0, 2)
	if status != "" {
		parts = append(parts, "status="+status)
	}
	if statusMessage != "" {
		parts = append(parts, statusMessage)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func (a *codexACPProxyAgent) resetTurnState(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.agentMessageItems = make(map[string]agentMessageItemState)
		state.reasoningItems = make(map[string]reasoningItemState)
		state.planItems = make(map[string]planItemState)
		state.planOrder = nil
		state.pendingRequests = make(map[string]string)
		state.pendingMCPToolCalls = make(map[string]pendingMCPToolCall)
		state.latestUsage = nil
	}
}

func optionalRawStringValue(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func (a *codexACPProxyAgent) reasoningThoughtsEnabled() bool {
	return a.reasoningThoughts != reasoningThoughtsOff
}

func (a *codexACPProxyAgent) reasoningThoughtsIncludeSummary() bool {
	return a.reasoningThoughts == reasoningThoughtsSummary || a.reasoningThoughts == reasoningThoughtsBoth
}

func (a *codexACPProxyAgent) reasoningThoughtsIncludeContent() bool {
	return a.reasoningThoughts == reasoningThoughtsContent || a.reasoningThoughts == reasoningThoughtsBoth
}

func (a *codexACPProxyAgent) noteAgentMessageStarted(sessionID acp.SessionId, item map[string]any) {
	itemID := stringValue(item, "id")
	if itemID == "" {
		return
	}
	phase, phaseKnown := optionalRawStringValue(item, "phase")
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	if state.agentMessageItems == nil {
		state.agentMessageItems = make(map[string]agentMessageItemState)
	}
	itemState := state.agentMessageItems[itemID]
	if phaseKnown {
		itemState.phase = phase
		itemState.phaseKnown = true
	}
	state.agentMessageItems[itemID] = itemState
}

func (a *codexACPProxyAgent) noteReasoningStarted(sessionID acp.SessionId, item map[string]any) {
	itemID := stringValue(item, "id")
	if itemID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	if state.reasoningItems == nil {
		state.reasoningItems = make(map[string]reasoningItemState)
	}
	if _, ok := state.reasoningItems[itemID]; !ok {
		state.reasoningItems[itemID] = reasoningItemState{}
	}
}

func (a *codexACPProxyAgent) markAgentMessageStreamed(sessionID acp.SessionId, itemID string) agentMessageItemState {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return agentMessageItemState{}
	}
	if state.agentMessageItems == nil {
		state.agentMessageItems = make(map[string]agentMessageItemState)
	}
	itemState := state.agentMessageItems[itemID]
	itemState.streamed = true
	state.agentMessageItems[itemID] = itemState
	return itemState
}

func (a *codexACPProxyAgent) completeAgentMessageState(sessionID acp.SessionId, itemID string, phase string, phaseKnown bool) agentMessageItemState {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return agentMessageItemState{}
	}
	itemState := state.agentMessageItems[itemID]
	if phaseKnown {
		itemState.phase = phase
		itemState.phaseKnown = true
	}
	delete(state.agentMessageItems, itemID)
	return itemState
}

func (a *codexACPProxyAgent) advanceReasoningLane(sessionID acp.SessionId, itemID string, kind string, index int64, streamed bool) (int64, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return 0, "", false
	}
	if state.reasoningItems == nil {
		state.reasoningItems = make(map[string]reasoningItemState)
	}
	itemState := state.reasoningItems[itemID]
	var lane reasoningLaneState
	switch kind {
	case reasoningKindSummary:
		lane = itemState.summary
	case reasoningKindContent:
		lane = itemState.content
	default:
		return 0, "", false
	}
	previousIndex := lane.index
	previousText := lane.text
	shouldClose := lane.open && lane.index != index && lane.hasText
	if !lane.open || lane.index != index {
		lane.index = index
		lane.open = true
		lane.hasText = false
		lane.text = ""
	}
	if streamed {
		lane.streamed = true
		lane.hasText = true
	}
	switch kind {
	case reasoningKindSummary:
		itemState.summary = lane
	case reasoningKindContent:
		itemState.content = lane
	}
	state.reasoningItems[itemID] = itemState
	return previousIndex, previousText, shouldClose
}

func (a *codexACPProxyAgent) appendReasoningLaneText(sessionID acp.SessionId, itemID string, kind string, index int64, delta string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	if state.reasoningItems == nil {
		state.reasoningItems = make(map[string]reasoningItemState)
	}
	itemState := state.reasoningItems[itemID]
	var lane reasoningLaneState
	switch kind {
	case reasoningKindSummary:
		lane = itemState.summary
	case reasoningKindContent:
		lane = itemState.content
	default:
		return
	}
	if !lane.open || lane.index != index {
		lane.index = index
		lane.open = true
		lane.hasText = false
		lane.text = ""
	}
	lane.text += delta
	lane.hasText = lane.hasText || delta != ""
	switch kind {
	case reasoningKindSummary:
		itemState.summary = lane
	case reasoningKindContent:
		itemState.content = lane
	}
	state.reasoningItems[itemID] = itemState
}

func (a *codexACPProxyAgent) markReasoningSummaryPublished(sessionID acp.SessionId, itemID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || state.reasoningItems == nil {
		return
	}
	itemState := state.reasoningItems[itemID]
	itemState.summaryPublished = true
	state.reasoningItems[itemID] = itemState
}

func (a *codexACPProxyAgent) completeReasoningState(sessionID acp.SessionId, itemID string) reasoningItemState {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return reasoningItemState{}
	}
	itemState := state.reasoningItems[itemID]
	delete(state.reasoningItems, itemID)
	return itemState
}

func reasoningThoughtChunkMeta(itemID string, kind string, index int64, completed bool) map[string]any {
	meta := map[string]any{
		metaCompletedKey: completed,
	}
	if itemID != "" {
		meta[metaItemIDKey] = itemID
	}
	switch kind {
	case reasoningKindSummary:
		meta[metaReasoningKindKey] = reasoningKindSummary
		meta[metaSummaryIndexKey] = index
	case reasoningKindContent:
		meta[metaReasoningKindKey] = reasoningKindContent
		meta[metaContentIndexKey] = index
	}
	return meta
}

func reasoningThoughtChunkUpdate(text string, itemID string, kind string, index int64, completed bool) acp.SessionUpdate {
	messageID := reasoningThoughtMessageID(itemID, kind, index)
	return acp.SessionUpdate{
		AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{
			Meta:          reasoningThoughtChunkMeta(itemID, kind, index, completed),
			Content:       acp.TextBlock(text),
			MessageId:     &messageID,
			SessionUpdate: "agent_thought_chunk",
		},
	}
}

func reasoningThoughtMessageID(itemID string, kind string, index int64) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("codex-acp-bridge:reasoning:%s:%s:%d", itemID, kind, index)))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func (a *codexACPProxyAgent) sendReasoningChunk(ctx context.Context, sessionID acp.SessionId, text string, itemID string, kind string, index int64, completed bool) error {
	return a.sendUpdate(ctx, sessionID, reasoningThoughtChunkUpdate(text, itemID, kind, index, completed))
}

func (a *codexACPProxyAgent) handleReasoningDelta(ctx context.Context, sessionID acp.SessionId, method string, params map[string]any) error {
	delta := rawStringValue(params, "delta")
	itemID := stringValue(params, "itemId")
	if itemID == "" || delta == "" {
		return nil
	}
	var (
		kind  string
		index int64
		ok    bool
	)
	switch method {
	case methodReasoningSummaryTextDelta:
		if !a.reasoningThoughtsIncludeSummary() {
			return nil
		}
		kind = reasoningKindSummary
		index, ok = int64Value(params, "summaryIndex")
	case methodReasoningTextDelta:
		if !a.reasoningThoughtsIncludeContent() {
			return nil
		}
		kind = reasoningKindContent
		index, ok = int64Value(params, "contentIndex")
	default:
		return nil
	}
	if !ok {
		return nil
	}
	if !a.reasoningStreaming {
		a.appendReasoningLaneText(sessionID, itemID, kind, index, delta)
		return nil
	}
	previousIndex, _, shouldClose := a.advanceReasoningLane(sessionID, itemID, kind, index, true)
	if shouldClose {
		if err := a.sendReasoningChunk(ctx, sessionID, "", itemID, kind, previousIndex, true); err != nil {
			return err
		}
	}
	return a.sendReasoningChunk(ctx, sessionID, delta, itemID, kind, index, false)
}

func (a *codexACPProxyAgent) handleReasoningSummaryPartAdded(ctx context.Context, sessionID acp.SessionId, params map[string]any) error {
	itemID := stringValue(params, "itemId")
	index, ok := int64Value(params, "summaryIndex")
	if itemID == "" || !ok {
		return nil
	}
	previousIndex, previousText, shouldClose := a.advanceReasoningLane(sessionID, itemID, reasoningKindSummary, index, false)
	if !shouldClose {
		return nil
	}
	if !a.reasoningStreaming {
		text := completedReasoningText([]string{previousText})
		if text == "" {
			return nil
		}
		if err := a.sendReasoningChunk(ctx, sessionID, text, itemID, reasoningKindSummary, previousIndex, true); err != nil {
			return err
		}
		a.markReasoningSummaryPublished(sessionID, itemID)
		return nil
	}
	return a.sendReasoningChunk(ctx, sessionID, "", itemID, reasoningKindSummary, previousIndex, true)
}

func reasoningItemTexts(item map[string]any, key string) []string {
	values := listValue(item, key)
	if len(values) == 0 {
		return nil
	}
	texts := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		texts = append(texts, text)
	}
	return texts
}

func (a *codexACPProxyAgent) emitCompletedReasoningTexts(ctx context.Context, sessionID acp.SessionId, itemID string, kind string, texts []string) error {
	text := completedReasoningText(texts)
	if text == "" {
		return nil
	}
	return a.sendReasoningChunk(ctx, sessionID, text, itemID, kind, 0, true)
}

func completedReasoningText(texts []string) string {
	parts := make([]string, 0, len(texts))
	for _, text := range texts {
		text = strings.TrimSpace(strings.ReplaceAll(text, "<!-- -->", ""))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func hasCompletedReasoningText(texts []string) bool {
	for _, text := range texts {
		if text != "" {
			return true
		}
	}
	return false
}

func (a *codexACPProxyAgent) handleCompletedReasoning(ctx context.Context, sessionID acp.SessionId, item map[string]any) error {
	itemID := stringValue(item, "id")
	if itemID == "" {
		return nil
	}
	state := a.completeReasoningState(sessionID, itemID)
	if !a.reasoningThoughtsEnabled() {
		return nil
	}
	summaryTexts := reasoningItemTexts(item, "summary")
	contentTexts := reasoningItemTexts(item, "content")
	if a.reasoningStreaming {
		if a.reasoningThoughtsIncludeSummary() {
			if state.summary.hasText {
				if err := a.sendReasoningChunk(ctx, sessionID, "", itemID, reasoningKindSummary, state.summary.index, true); err != nil {
					return err
				}
			} else if !state.summary.streamed {
				kind := reasoningKindSummary
				texts := summaryTexts
				if !hasCompletedReasoningText(texts) && !a.reasoningThoughtsIncludeContent() {
					kind = reasoningKindContent
					texts = contentTexts
				}
				if err := a.emitCompletedReasoningTexts(ctx, sessionID, itemID, kind, texts); err != nil {
					return err
				}
			}
		}
		if a.reasoningThoughtsIncludeContent() {
			if state.content.hasText {
				if err := a.sendReasoningChunk(ctx, sessionID, "", itemID, reasoningKindContent, state.content.index, true); err != nil {
					return err
				}
			} else if !state.content.streamed {
				if err := a.emitCompletedReasoningTexts(ctx, sessionID, itemID, reasoningKindContent, contentTexts); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if a.reasoningThoughtsIncludeSummary() {
		if state.summaryPublished {
			if text := completedReasoningText([]string{state.summary.text}); text != "" {
				if err := a.sendReasoningChunk(ctx, sessionID, text, itemID, reasoningKindSummary, state.summary.index, true); err != nil {
					return err
				}
			}
		} else {
			kind := reasoningKindSummary
			texts := summaryTexts
			if !hasCompletedReasoningText(texts) && !a.reasoningThoughtsIncludeContent() {
				kind = reasoningKindContent
				texts = contentTexts
			}
			if err := a.emitCompletedReasoningTexts(ctx, sessionID, itemID, kind, texts); err != nil {
				return err
			}
		}
	}
	if a.reasoningThoughtsIncludeContent() {
		if err := a.emitCompletedReasoningTexts(ctx, sessionID, itemID, reasoningKindContent, contentTexts); err != nil {
			return err
		}
	}
	return nil
}

func agentMessageChunkMeta(itemID string, completed bool, phase string, phaseKnown bool) map[string]any {
	meta := map[string]any{
		metaCompletedKey: completed,
	}
	if itemID != "" {
		meta[metaItemIDKey] = itemID
	}
	if phaseKnown {
		meta[metaPhaseKey] = phase
	}
	return meta
}

func agentMessageChunkUpdate(text string, itemID string, completed bool, phase string, phaseKnown bool) acp.SessionUpdate {
	return acp.SessionUpdate{
		AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{
			Meta:          agentMessageChunkMeta(itemID, completed, phase, phaseKnown),
			Content:       acp.TextBlock(text),
			SessionUpdate: "agent_message_chunk",
		},
	}
}

func (a *codexACPProxyAgent) sendAgentMessageDelta(ctx context.Context, sessionID acp.SessionId, itemID string, delta string) error {
	itemState := a.markAgentMessageStreamed(sessionID, itemID)
	return a.sendUpdate(ctx, sessionID, agentMessageChunkUpdate(delta, itemID, false, itemState.phase, itemState.phaseKnown))
}

func (a *codexACPProxyAgent) handleCompletedAgentMessage(ctx context.Context, sessionID acp.SessionId, item map[string]any) error {
	itemID := stringValue(item, "id")
	phase, phaseKnown := optionalRawStringValue(item, "phase")
	text := rawStringValue(item, "text")
	if a.messageStreaming {
		itemState := a.completeAgentMessageState(sessionID, itemID, phase, phaseKnown)
		if !phaseKnown && itemState.phaseKnown {
			phase = itemState.phase
			phaseKnown = true
		}
		if itemState.streamed {
			return a.sendUpdate(ctx, sessionID, agentMessageChunkUpdate("", itemID, true, phase, phaseKnown))
		}
		return a.sendUpdate(ctx, sessionID, agentMessageChunkUpdate(text, itemID, true, phase, phaseKnown))
	}

	if phase == "commentary" {
		return nil
	}
	if phase != "" && phase != "final_answer" {
		if a.logger != nil {
			a.logger.Debug().
				Str("itemId", itemID).
				Str("phase", phase).
				Msg("skipping agent message with unsupported phase")
		}
		return nil
	}
	if text == "" {
		return nil
	}
	return a.sendUpdate(ctx, sessionID, acp.UpdateAgentMessageText(text))
}

func planPreviewEntries(order []string, items map[string]planItemState) []acp.PlanEntry {
	if len(order) == 0 || len(items) == 0 {
		return nil
	}
	entries := make([]acp.PlanEntry, 0, len(order))
	for _, itemID := range order {
		itemState, ok := items[itemID]
		if !ok {
			continue
		}
		if strings.TrimSpace(itemState.text) == "" {
			continue
		}
		entries = append(entries, acp.PlanEntry{
			Content:  itemState.text,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   acp.PlanEntryStatusInProgress,
		})
	}
	return entries
}

func (a *codexACPProxyAgent) appendPlanDelta(sessionID acp.SessionId, itemID string, delta string) []acp.PlanEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		if strings.TrimSpace(delta) == "" {
			return nil
		}
		return []acp.PlanEntry{{
			Content:  delta,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   acp.PlanEntryStatusInProgress,
		}}
	}
	if state.planItems == nil {
		state.planItems = make(map[string]planItemState)
	}
	if _, ok := state.planItems[itemID]; !ok {
		state.planOrder = append(state.planOrder, itemID)
	}
	itemState := state.planItems[itemID]
	itemState.text += delta
	state.planItems[itemID] = itemState
	return planPreviewEntries(state.planOrder, state.planItems)
}

func (a *codexACPProxyAgent) setCompletedPlanItem(sessionID acp.SessionId, itemID string, text string) []acp.PlanEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []acp.PlanEntry{{
			Content:  text,
			Priority: acp.PlanEntryPriorityMedium,
			Status:   acp.PlanEntryStatusInProgress,
		}}
	}
	if state.planItems == nil {
		state.planItems = make(map[string]planItemState)
	}
	if _, ok := state.planItems[itemID]; !ok {
		state.planOrder = append(state.planOrder, itemID)
	}
	state.planItems[itemID] = planItemState{text: text}
	return planPreviewEntries(state.planOrder, state.planItems)
}

func (a *codexACPProxyAgent) resetPlanPreviewState(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	state.planItems = make(map[string]planItemState)
	state.planOrder = nil
}

func (a *codexACPProxyAgent) handleCompletedPlan(ctx context.Context, sessionID acp.SessionId, item map[string]any) error {
	itemID := stringValue(item, "id")
	text := rawStringValue(item, "text")
	if itemID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	entries := a.setCompletedPlanItem(sessionID, itemID, text)
	if len(entries) == 0 {
		return nil
	}
	return a.sendUpdate(ctx, sessionID, acp.UpdatePlan(entries...))
}

func (a *codexACPProxyAgent) setSessionRateLimits(sessionID acp.SessionId, rateLimits map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.latestRateLimits = cloneAnyMap(rateLimits)
	}
}

func (a *codexACPProxyAgent) sessionRateLimits(sessionID acp.SessionId) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		return cloneAnyMap(state.latestRateLimits)
	}
	return nil
}

func (a *codexACPProxyAgent) setSessionUsage(sessionID acp.SessionId, usage map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.latestUsage = cloneAnyMap(usage)
	}
}

func (a *codexACPProxyAgent) sessionUsage(sessionID acp.SessionId) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		return cloneAnyMap(state.latestUsage)
	}
	return nil
}

func (a *codexACPProxyAgent) setSessionMCPStartupStatus(sessionID acp.SessionId, name string, status string, errText string) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || len(state.mcpServers) == 0 {
		return
	}
	if _, ok := state.mcpServers[trimmedName]; !ok {
		return
	}
	if state.mcpStartup == nil {
		state.mcpStartup = make(map[string]sessionMCPStartup, len(state.mcpServers))
	}
	state.mcpStartup[trimmedName] = sessionMCPStartup{
		status: strings.TrimSpace(status),
		err:    strings.TrimSpace(errText),
	}
}

func (a *codexACPProxyAgent) sessionMCPMeta(sessionID acp.SessionId, includeStartup bool) map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || len(state.mcpServers) == 0 {
		return nil
	}
	meta := map[string]any{
		"contract":  mcpContractMerge,
		"requested": requestedMCPServersMeta(state.mcpServers),
	}
	if includeStartup {
		if startup := mcpStartupStatusMeta(state.mcpServers, state.mcpStartup); len(startup) > 0 {
			meta["startupStatus"] = startup
		}
	}
	return meta
}

func requestedMCPServersMeta(servers map[string]acp.McpServer) []any {
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		transport := mcpTransportName(servers[name])
		entry := map[string]any{"name": name}
		if transport != "" {
			entry["transport"] = transport
		}
		out = append(out, entry)
	}
	return out
}

func mcpStartupStatusMeta(servers map[string]acp.McpServer, startup map[string]sessionMCPStartup) map[string]any {
	if len(servers) == 0 || len(startup) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string]any)
	for _, name := range names {
		status, ok := startup[name]
		if !ok {
			continue
		}
		entry := map[string]any{}
		if status.status != "" {
			entry["status"] = status.status
		}
		if status.err != "" {
			entry["error"] = status.err
		}
		if len(entry) == 0 {
			continue
		}
		out[name] = entry
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mcpTransportName(server acp.McpServer) string {
	switch {
	case server.Stdio != nil:
		return "stdio"
	case server.Http != nil:
		return "http"
	case server.Sse != nil:
		return "sse"
	default:
		return ""
	}
}

func cloneAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, entry := range typed {
			out[key] = cloneJSONValue(entry)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for idx, entry := range typed {
			out[idx] = cloneJSONValue(entry)
		}
		return out
	default:
		return typed
	}
}

func (a *codexACPProxyAgent) clearSessionBackend(sessionID acp.SessionId, backend appServerSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || state.backend != backend {
		return
	}
	state.backend = nil
	state.workerCancel = nil
	state.threadID = ""
	state.turnID = ""
	state.pendingRequests = nil
}

func unsubscribeThread(ctx context.Context, backend appServerSession, threadID string) error {
	if backend == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	resp, err := backend.ThreadUnsubscribe(ctx, threadID)
	if err != nil {
		return err
	}
	switch strings.TrimSpace(resp.Status) {
	case "", "unsubscribed", "notSubscribed", "notLoaded":
		return nil
	default:
		return fmt.Errorf("unexpected unsubscribe status %q", resp.Status)
	}
}

func sessionInfoFromAppServerThread(thread appServerThread) acp.SessionInfo {
	info := acp.SessionInfo{
		Cwd:       strings.TrimSpace(thread.Cwd),
		SessionId: acp.SessionId(strings.TrimSpace(thread.ID)),
	}
	if title := strings.TrimSpace(thread.Name); title != "" {
		info.Title = &title
	}
	if updatedAt := formatSessionUpdatedAt(thread.UpdatedAt); updatedAt != nil {
		info.UpdatedAt = updatedAt
	}
	return info
}

func formatSessionUpdatedAt(updatedAtUnix int64) *string {
	if updatedAtUnix <= 0 {
		return nil
	}
	formatted := time.Unix(updatedAtUnix, 0).UTC().Format(time.RFC3339)
	return &formatted
}

func derefTrimmedString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func (a *codexACPProxyAgent) syncThreadID(sessionID acp.SessionId, nextThreadID string) {
	trimmed := strings.TrimSpace(nextThreadID)
	if trimmed == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.threadID = trimmed
	}
}

func (a *codexACPProxyAgent) syncTurnID(sessionID acp.SessionId, nextTurnID string) {
	trimmed := strings.TrimSpace(nextTurnID)
	if trimmed == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.turnID = trimmed
	}
}

func (a *codexACPProxyAgent) markPendingRequest(sessionID acp.SessionId, requestID string, method string) {
	trimmedRequestID := strings.TrimSpace(requestID)
	if trimmedRequestID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	if state.pendingRequests == nil {
		state.pendingRequests = make(map[string]string)
	}
	state.pendingRequests[trimmedRequestID] = strings.TrimSpace(method)
}

func (a *codexACPProxyAgent) resolvePendingRequest(sessionID acp.SessionId, requestID string) bool {
	trimmedRequestID := strings.TrimSpace(requestID)
	if trimmedRequestID == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil || state.pendingRequests == nil {
		return false
	}
	if _, ok := state.pendingRequests[trimmedRequestID]; !ok {
		return false
	}
	delete(state.pendingRequests, trimmedRequestID)
	return true
}

func (a *codexACPProxyAgent) clearPendingRequests(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.sessions[sessionID]
	if state == nil {
		return
	}
	state.pendingRequests = make(map[string]string)
	state.pendingMCPToolCalls = make(map[string]pendingMCPToolCall)
}

func (a *codexACPProxyAgent) notePendingMCPToolCall(sessionID acp.SessionId, serverName, itemID, toolName string) {
	serverName = strings.TrimSpace(serverName)
	itemID = strings.TrimSpace(itemID)
	if serverName == "" || itemID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		if state.pendingMCPToolCalls == nil {
			state.pendingMCPToolCalls = make(map[string]pendingMCPToolCall)
		}
		state.pendingMCPToolCalls[serverName] = pendingMCPToolCall{itemID: itemID, tool: strings.TrimSpace(toolName)}
	}
}

func (a *codexACPProxyAgent) pendingMCPToolName(sessionID acp.SessionId, serverName string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		return state.pendingMCPToolCalls[strings.TrimSpace(serverName)].tool
	}
	return ""
}

func (a *codexACPProxyAgent) clearPendingMCPToolCall(sessionID acp.SessionId, serverName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		delete(state.pendingMCPToolCalls, strings.TrimSpace(serverName))
	}
}

func (a *codexACPProxyAgent) clearPendingMCPToolCalls(sessionID acp.SessionId) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if state := a.sessions[sessionID]; state != nil {
		state.pendingMCPToolCalls = make(map[string]pendingMCPToolCall)
	}
}

func requestIDFromAny(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return canonicalRequestID(raw), true
}

func isToolLifecycleItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case itemTypeCommandExecution, itemTypeFileChange, itemTypeMCPToolCall, itemTypeDynamicToolCall,
		"collabAgentToolCall", "webSearch", "imageView", "imageGeneration":
		return true
	default:
		return false
	}
}

func toolCallTitle(itemType string, item map[string]any) string {
	switch strings.TrimSpace(itemType) {
	case itemTypeCommandExecution:
		if command := stringValue(item, "command"); command != "" {
			return command
		}
		return "command execution"
	case itemTypeFileChange:
		return "file change"
	case itemTypeMCPToolCall:
		tool := stringValue(item, "tool")
		if tool != "" {
			return tool
		}
		return "mcp tool call"
	case itemTypeDynamicToolCall:
		tool := stringValue(item, "tool")
		if tool != "" {
			return tool
		}
		return "dynamic tool call"
	default:
		return itemType
	}
}
