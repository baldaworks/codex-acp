package codexacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

const defaultBackendShutdownGracePeriod = 2 * time.Second
const signalForwardingGracePeriod = 100 * time.Millisecond

type appServerRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *appServerRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("bridge backend rpc error (%d): %s", e.Code, e.Message)
}

type appServerEnvelope struct {
	Method string             `json:"method,omitempty"`
	ID     json.RawMessage    `json:"id,omitempty"`
	Params json.RawMessage    `json:"params,omitempty"`
	Result json.RawMessage    `json:"result,omitempty"`
	Error  *appServerRPCError `json:"error,omitempty"`
}

type appServerInitializeResponse struct {
	UserAgent string `json:"userAgent"`
}

type appServerThread struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd,omitempty"`
	Name      string `json:"name,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

type appServerThreadStartResponse struct {
	Thread          appServerThread `json:"thread"`
	Model           string          `json:"model,omitempty"`
	ReasoningEffort *string         `json:"reasoningEffort,omitempty"`
}

type appServerThreadResumeResponse struct {
	Thread          appServerThread `json:"thread"`
	Model           string          `json:"model,omitempty"`
	ReasoningEffort *string         `json:"reasoningEffort,omitempty"`
}

type appServerTurnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type appServerModel struct {
	ID                        string                           `json:"id"`
	DisplayName               string                           `json:"displayName"`
	Description               *string                          `json:"description,omitempty"`
	IsDefault                 bool                             `json:"isDefault"`
	DefaultReasoningEffort    string                           `json:"defaultReasoningEffort,omitempty"`
	SupportedReasoningEfforts []appServerReasoningEffortOption `json:"supportedReasoningEfforts,omitempty"`
}

type appServerReasoningEffortOption struct {
	Description     string `json:"description"`
	ReasoningEffort string `json:"reasoningEffort"`
}

type appServerModelListResponse struct {
	Data       []appServerModel `json:"data"`
	NextCursor *string          `json:"nextCursor,omitempty"`
}

type appServerThreadListResponse struct {
	Data       []appServerThread `json:"data"`
	NextCursor *string           `json:"nextCursor,omitempty"`
}

type appServerThreadUnsubscribeResponse struct {
	Status string `json:"status"`
}

type appServerRPCResponse struct {
	result json.RawMessage
	err    error
}

type appServerNotification struct {
	Method string
	Params json.RawMessage
}

type appServerRequest struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

type appServerEvent struct {
	Notification *appServerNotification
	Request      *appServerRequest
}

type appServerBackend struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	logger *zerolog.Logger

	writeMu sync.Mutex

	pendingMu sync.Mutex
	pending   map[string]chan appServerRPCResponse
	nextID    uint64

	events   chan appServerEvent
	closing  chan struct{}
	done     chan struct{}
	readDone chan struct{}

	finalizeOnce sync.Once
	waitErr      error
	closeOnce    sync.Once

	shutdownGracePeriod time.Duration

	initializeResp appServerInitializeResponse
}

type appServerSession interface {
	InitializeResponse() appServerInitializeResponse
	Events() <-chan appServerEvent
	ThreadList(ctx context.Context, params map[string]any) (appServerThreadListResponse, error)
	ThreadUnsubscribe(ctx context.Context, threadID string) (appServerThreadUnsubscribeResponse, error)
	ThreadStart(ctx context.Context, params map[string]any) (appServerThreadStartResponse, error)
	ThreadResume(ctx context.Context, params map[string]any) (appServerThreadResumeResponse, error)
	ThreadSettingsUpdate(ctx context.Context, params map[string]any) error
	TurnStart(ctx context.Context, params map[string]any) (appServerTurnStartResponse, error)
	ModelList(ctx context.Context, params map[string]any) (appServerModelListResponse, error)
	TurnInterrupt(ctx context.Context, threadID string, turnID string) error
	RespondRequest(ctx context.Context, req *appServerRequest, result any) error
	RespondRequestError(ctx context.Context, req *appServerRequest, code int, message string, data any) error
	Close() error
	Wait() error
}

func connectAppServerBackend(
	lifetimeCtx context.Context,
	initCtx context.Context,
	workingDir string,
	sessionCWD string,
	command []string,
	clientName string,
	stderr io.Writer,
	logger *zerolog.Logger,
	opts Options,
) (*appServerBackend, error) {
	if len(command) == 0 {
		return nil, errors.New("empty codex command")
	}
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}

	cmd := exec.CommandContext(lifetimeCtx, command[0], command[1:]...)
	cmd.Dir = strings.TrimSpace(sessionCWD)
	if cmd.Dir == "" {
		cmd.Dir = workingDir
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = io.Discard
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("bridge backend stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("bridge backend stdout pipe: %w", err)
	}
	backend := &appServerBackend{
		cmd:                 cmd,
		stdin:               stdin,
		logger:              logger,
		pending:             make(map[string]chan appServerRPCResponse),
		events:              make(chan appServerEvent, 256),
		closing:             make(chan struct{}),
		done:                make(chan struct{}),
		readDone:            make(chan struct{}),
		shutdownGracePeriod: defaultBackendShutdownGracePeriod,
	}
	cmd.Cancel = func() error {
		return backend.Close()
	}
	logger.Debug().
		Str("cwd", cmd.Dir).
		Str("cmd", command[0]).
		Strs("args", command[1:]).
		Msg("starting codex bridge backend")
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start codex bridge backend: %w", err)
	}

	go backend.readLoop(stdout)
	go backend.waitLoop()

	initializeResp, err := backend.initialize(initCtx, clientName, opts)
	if err != nil {
		_ = backend.Close()
		_ = backend.Wait()
		return nil, err
	}
	backend.initializeResp = initializeResp
	return backend, nil
}

func (b *appServerBackend) InitializeResponse() appServerInitializeResponse {
	return b.initializeResp
}

func (b *appServerBackend) Events() <-chan appServerEvent {
	return b.events
}

func (b *appServerBackend) ThreadList(ctx context.Context, params map[string]any) (appServerThreadListResponse, error) {
	var resp appServerThreadListResponse
	if err := b.call(ctx, "thread/list", params, &resp); err != nil {
		return appServerThreadListResponse{}, err
	}
	return resp, nil
}

func (b *appServerBackend) ThreadUnsubscribe(ctx context.Context, threadID string) (appServerThreadUnsubscribeResponse, error) {
	var resp appServerThreadUnsubscribeResponse
	if strings.TrimSpace(threadID) == "" {
		return appServerThreadUnsubscribeResponse{}, nil
	}
	if err := b.call(ctx, "thread/unsubscribe", map[string]any{"threadId": strings.TrimSpace(threadID)}, &resp); err != nil {
		return appServerThreadUnsubscribeResponse{}, err
	}
	return resp, nil
}

func (b *appServerBackend) ThreadStart(ctx context.Context, params map[string]any) (appServerThreadStartResponse, error) {
	var resp appServerThreadStartResponse
	if err := b.call(ctx, "thread/start", params, &resp); err != nil {
		return appServerThreadStartResponse{}, err
	}
	if strings.TrimSpace(resp.Thread.ID) == "" {
		return appServerThreadStartResponse{}, errors.New("thread/start returned empty thread id")
	}
	if strings.TrimSpace(resp.Thread.SessionID) == "" {
		return appServerThreadStartResponse{}, errors.New("thread/start returned empty thread session id")
	}
	return resp, nil
}

func (b *appServerBackend) ThreadResume(ctx context.Context, params map[string]any) (appServerThreadResumeResponse, error) {
	var resp appServerThreadResumeResponse
	if err := b.call(ctx, "thread/resume", params, &resp); err != nil {
		return appServerThreadResumeResponse{}, err
	}
	if strings.TrimSpace(resp.Thread.ID) == "" {
		return appServerThreadResumeResponse{}, errors.New("thread/resume returned empty thread id")
	}
	if strings.TrimSpace(resp.Thread.SessionID) == "" {
		return appServerThreadResumeResponse{}, errors.New("thread/resume returned empty thread session id")
	}
	return resp, nil
}

func (b *appServerBackend) ThreadSettingsUpdate(ctx context.Context, params map[string]any) error {
	return b.call(ctx, "thread/settings/update", params, nil)
}

func (b *appServerBackend) TurnStart(ctx context.Context, params map[string]any) (appServerTurnStartResponse, error) {
	var resp appServerTurnStartResponse
	if err := b.call(ctx, "turn/start", params, &resp); err != nil {
		return appServerTurnStartResponse{}, err
	}
	if strings.TrimSpace(resp.Turn.ID) == "" {
		return appServerTurnStartResponse{}, errors.New("turn/start returned empty turn id")
	}
	return resp, nil
}

func (b *appServerBackend) ModelList(ctx context.Context, params map[string]any) (appServerModelListResponse, error) {
	var resp appServerModelListResponse
	if err := b.call(ctx, "model/list", params, &resp); err != nil {
		return appServerModelListResponse{}, err
	}
	return resp, nil
}

func (b *appServerBackend) TurnInterrupt(ctx context.Context, threadID string, turnID string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	return b.call(ctx, "turn/interrupt", map[string]any{
		"threadId": strings.TrimSpace(threadID),
		"turnId":   strings.TrimSpace(turnID),
	}, nil)
}

func (b *appServerBackend) RespondRequest(ctx context.Context, req *appServerRequest, result any) error {
	if req == nil || len(req.ID) == 0 {
		return errors.New("request id is required")
	}
	return b.sendResponse(ctx, req.ID, result)
}

func (b *appServerBackend) RespondRequestError(ctx context.Context, req *appServerRequest, code int, message string, data any) error {
	if req == nil || len(req.ID) == 0 {
		return errors.New("request id is required")
	}
	return b.sendError(ctx, req.ID, code, message, data)
}

func appServerInitializeParams(clientName string, opts Options) map[string]any {
	capabilities := map[string]any{
		"experimentalApi": true,
	}
	if optOut := appServerOptOutNotificationMethods(opts); len(optOut) > 0 {
		capabilities["optOutNotificationMethods"] = optOut
	}
	return map[string]any{
		"clientInfo": map[string]any{
			"name":    strings.TrimSpace(clientName),
			"title":   "Norma Codex ACP Bridge",
			"version": "dev",
		},
		"capabilities": capabilities,
	}
}

func appServerOptOutNotificationMethods(opts Options) []string {
	methods := make([]string, 0, 4)
	if !opts.MessageStreaming {
		methods = append(methods, methodAgentMessageDelta)
	}
	if !opts.reasoningThoughtsEnabled() {
		methods = append(methods,
			methodReasoningTextDelta,
			methodReasoningSummaryTextDelta,
			methodReasoningSummaryPartAdded,
		)
		return methods
	}
	if !opts.reasoningStreamingEnabled() {
		methods = append(methods, methodReasoningTextDelta)
		if !opts.reasoningThoughtsIncludeSummary() {
			methods = append(methods,
				methodReasoningSummaryTextDelta,
				methodReasoningSummaryPartAdded,
			)
		}
		return methods
	}
	if !opts.reasoningThoughtsIncludeContent() {
		methods = append(methods, methodReasoningTextDelta)
	}
	if !opts.reasoningThoughtsIncludeSummary() {
		methods = append(methods,
			methodReasoningSummaryTextDelta,
			methodReasoningSummaryPartAdded,
		)
	}
	return methods
}

func (b *appServerBackend) initialize(ctx context.Context, clientName string, opts Options) (appServerInitializeResponse, error) {
	params := appServerInitializeParams(clientName, opts)
	var resp appServerInitializeResponse
	if err := b.call(ctx, "initialize", params, &resp); err != nil {
		return appServerInitializeResponse{}, fmt.Errorf("initialize bridge backend: %w", err)
	}
	if err := b.sendNotification(ctx, "initialized", nil); err != nil {
		return appServerInitializeResponse{}, fmt.Errorf("send initialized notification: %w", err)
	}
	return resp, nil
}

func (b *appServerBackend) call(ctx context.Context, method string, params any, out any) error {
	id := atomic.AddUint64(&b.nextID, 1)
	idRaw := json.RawMessage(strconv.AppendUint(nil, id, 10))
	key := canonicalRequestID(idRaw)
	respCh := make(chan appServerRPCResponse, 1)

	b.pendingMu.Lock()
	b.pending[key] = respCh
	b.pendingMu.Unlock()
	defer func() {
		b.pendingMu.Lock()
		delete(b.pending, key)
		b.pendingMu.Unlock()
	}()

	if err := b.sendRequest(ctx, idRaw, method, params); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("bridge backend stopped")
	case resp := <-respCh:
		if resp.err != nil {
			return resp.err
		}
		if out == nil {
			return nil
		}
		if len(resp.result) == 0 || string(resp.result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.result, out); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

func (b *appServerBackend) sendRequest(ctx context.Context, id json.RawMessage, method string, params any) error {
	payload := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendResponse(ctx context.Context, id json.RawMessage, result any) error {
	payload := map[string]any{
		"id":     id,
		"result": result,
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendError(ctx context.Context, id json.RawMessage, code int, message string, data any) error {
	errPayload := map[string]any{
		"code":    code,
		"message": message,
	}
	if data != nil {
		errPayload["data"] = data
	}
	payload := map[string]any{
		"id":    id,
		"error": errPayload,
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendNotification(ctx context.Context, method string, params any) error {
	payload := map[string]any{
		"method": method,
	}
	if params != nil {
		payload["params"] = params
	}
	return b.sendJSON(ctx, payload)
}

func (b *appServerBackend) sendJSON(ctx context.Context, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal bridge backend payload: %w", err)
	}

	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return errors.New("bridge backend stopped")
	default:
	}

	if _, err := b.stdin.Write(append(raw, '\n')); err != nil {
		if b.isShuttingDown() || isClosedPipeWriteError(err) {
			return errors.New("bridge backend stopped")
		}
		return fmt.Errorf("write bridge backend payload: %w", err)
	}
	if b.logger.Debug().Enabled() {
		b.logger.Debug().Str("payload", string(raw)).Msg("bridge backend send")
	}
	return nil
}

func (b *appServerBackend) readLoop(stdout io.Reader) {
	defer close(b.readDone)

	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if parseErr := b.handleIncomingLine(line); parseErr != nil {
				b.logger.Warn().Err(parseErr).Str("line", strings.TrimSpace(string(line))).Msg("invalid bridge backend message")
			}
		}
		if err != nil {
			if b.isExpectedReadLoopStop(err) {
				return
			}
			b.logger.Warn().Err(err).Msg("bridge backend read loop stopped")
			return
		}
	}
}

func (b *appServerBackend) handleIncomingLine(line []byte) error {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil
	}

	var env appServerEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return err
	}
	if b.logger.Debug().Enabled() {
		b.logger.Debug().
			Str("method", env.Method).
			Str("id", canonicalRequestID(env.ID)).
			Str("payload", trimmed).
			Msg("bridge backend recv")
	}

	switch {
	case env.Method != "" && len(env.ID) > 0:
		b.emitEvent(appServerEvent{
			Request: &appServerRequest{
				ID:     env.ID,
				Method: env.Method,
				Params: env.Params,
			},
		})
	case env.Method != "":
		b.emitEvent(appServerEvent{
			Notification: &appServerNotification{
				Method: env.Method,
				Params: env.Params,
			},
		})
	case len(env.ID) > 0:
		key := canonicalRequestID(env.ID)
		b.pendingMu.Lock()
		respCh := b.pending[key]
		b.pendingMu.Unlock()
		if respCh == nil {
			return nil
		}
		if env.Error != nil {
			respCh <- appServerRPCResponse{err: env.Error}
			return nil
		}
		respCh <- appServerRPCResponse{result: env.Result}
	default:
	}
	return nil
}

func (b *appServerBackend) emitEvent(event appServerEvent) {
	select {
	case <-b.done:
		return
	default:
	}

	select {
	case <-b.done:
		return
	case b.events <- event:
		return
	case <-b.closing:
		select {
		case <-b.done:
			return
		case b.events <- event:
		default:
		}
	}
}

func (b *appServerBackend) waitLoop() {
	err := b.cmd.Wait()
	b.finalizeOnce.Do(func() {
		b.waitErr = err
		close(b.done)
		b.failPending(err)
		<-b.readDone
		close(b.events)
	})
}

func (b *appServerBackend) failPending(waitErr error) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	backendErr := errors.New("bridge backend stopped")
	if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) && !b.isShuttingDown() {
		backendErr = fmt.Errorf("bridge backend exited: %w", waitErr)
	}
	for key, ch := range b.pending {
		ch <- appServerRPCResponse{err: backendErr}
		delete(b.pending, key)
	}
}

func (b *appServerBackend) Close() error {
	var closeErr error
	b.closeOnce.Do(func() {
		close(b.closing)
		if b.cmd == nil || b.cmd.Process == nil {
			if err := b.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				closeErr = err
			}
			return
		}

		waitForExit := false
		if err := b.cmd.Process.Signal(os.Interrupt); err != nil {
			if !errors.Is(err, os.ErrProcessDone) && closeErr == nil {
				closeErr = err
			}
		} else {
			waitForExit = true
		}
		if waitForExit {
			_, exited := b.waitForDone(minDuration(b.shutdownGracePeriod, signalForwardingGracePeriod))
			if exited {
				return
			}
		}
		if err := b.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) && closeErr == nil {
			closeErr = err
		}

		if waitForExit {
			remaining := b.shutdownGracePeriod - minDuration(b.shutdownGracePeriod, signalForwardingGracePeriod)
			if _, exited := b.waitForDone(remaining); exited {
				return
			}
		}

		if err := b.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func (b *appServerBackend) Wait() error {
	<-b.done
	if b.waitErr == nil || errors.Is(b.waitErr, os.ErrProcessDone) {
		return nil
	}
	return b.waitErr
}

func canonicalRequestID(id json.RawMessage) string {
	return strings.TrimSpace(string(id))
}

func (b *appServerBackend) isExpectedReadLoopStop(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	return b.isShuttingDown() && isClosedPipeReadError(err)
}

func (b *appServerBackend) isShuttingDown() bool {
	select {
	case <-b.closing:
		return true
	default:
		return false
	}
}

func isClosedPipeReadError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "file already closed") || strings.Contains(msg, "use of closed file")
}

func isClosedPipeWriteError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "use of closed file")
}

func (b *appServerBackend) waitForDone(timeout time.Duration) (time.Duration, bool) {
	if timeout <= 0 {
		select {
		case <-b.done:
			return 0, true
		default:
			return 0, false
		}
	}

	start := time.Now()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-b.done:
		return time.Since(start), true
	case <-timer.C:
		return timeout, false
	}
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
