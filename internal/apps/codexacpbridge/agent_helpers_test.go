package codexacp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

type lifecycleBackendSpy struct {
	closeCalls     int
	waitCalls      int
	interruptCalls int
	lastThreadID   string
	lastTurnID     string
	lastCtx        context.Context
	initializeResp appServerInitializeResponse
}

func (s *lifecycleBackendSpy) InitializeResponse() appServerInitializeResponse {
	return s.initializeResp
}

func (s *lifecycleBackendSpy) Events() <-chan appServerEvent {
	return nil
}

func (s *lifecycleBackendSpy) ThreadList(context.Context, map[string]any) (appServerThreadListResponse, error) {
	return appServerThreadListResponse{}, nil
}

func (s *lifecycleBackendSpy) ThreadUnsubscribe(context.Context, string) (appServerThreadUnsubscribeResponse, error) {
	return appServerThreadUnsubscribeResponse{}, nil
}

func (s *lifecycleBackendSpy) ThreadStart(context.Context, map[string]any) (appServerThreadStartResponse, error) {
	return appServerThreadStartResponse{}, nil
}

func (s *lifecycleBackendSpy) ThreadResume(context.Context, map[string]any) (appServerThreadResumeResponse, error) {
	return appServerThreadResumeResponse{}, nil
}

func (s *lifecycleBackendSpy) ThreadSettingsUpdate(context.Context, map[string]any) error {
	return nil
}

func (s *lifecycleBackendSpy) TurnStart(context.Context, map[string]any) (appServerTurnStartResponse, error) {
	return appServerTurnStartResponse{}, nil
}

func (s *lifecycleBackendSpy) ModelList(context.Context, map[string]any) (appServerModelListResponse, error) {
	return appServerModelListResponse{}, nil
}

func (s *lifecycleBackendSpy) TurnInterrupt(ctx context.Context, threadID string, turnID string) error {
	s.interruptCalls++
	s.lastThreadID = threadID
	s.lastTurnID = turnID
	s.lastCtx = ctx
	return nil
}

func (s *lifecycleBackendSpy) RespondRequest(context.Context, *appServerRequest, any) error {
	return nil
}

func (s *lifecycleBackendSpy) RespondRequestError(context.Context, *appServerRequest, int, string, any) error {
	return nil
}

func (s *lifecycleBackendSpy) Close() error {
	s.closeCalls++
	return nil
}

func (s *lifecycleBackendSpy) Wait() error {
	s.waitCalls++
	return nil
}

func TestSetAgentVersionDefaultsWhenEmpty(t *testing.T) {
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, nil
	}, "agent", codexAppConfig{}, &logger)

	agent.setAgentVersion("  ")
	if got, want := agent.agentVersion, DefaultAgentVersion; got != want {
		t.Fatalf("agentVersion = %q, want %q", got, want)
	}

	agent.setAgentVersion("v-test-1")
	if got, want := agent.agentVersion, "v-test-1"; got != want {
		t.Fatalf("agentVersion = %q, want %q", got, want)
	}
}

func TestAuthenticateRejectsTerminalAndUnknownMethods(t *testing.T) {
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, nil
	}, "agent", codexAppConfig{}, &logger)

	for _, methodID := range []string{codexLoginAuthMethodID, "unknown"} {
		_, err := agent.Authenticate(context.Background(), acp.AuthenticateRequest{MethodId: methodID})
		if err == nil {
			t.Fatalf("Authenticate(%q) error = nil, want invalid params", methodID)
		}
		reqErr := &acp.RequestError{}
		if !errors.As(err, &reqErr) {
			t.Fatalf("Authenticate(%q) error type = %T, want *acp.RequestError", methodID, err)
		}
		if reqErr.Code != -32602 {
			t.Fatalf("Authenticate(%q) error code = %d, want -32602", methodID, reqErr.Code)
		}
	}
}

func TestCancel(t *testing.T) {
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, nil
	}, "agent", codexAppConfig{}, &logger)

	if err := agent.Cancel(context.Background(), acp.CancelNotification{SessionId: acp.SessionId("missing")}); err != nil {
		t.Fatalf("Cancel(missing) error = %v", err)
	}

	sessionID := acp.SessionId("s-cancel")
	backend := &lifecycleBackendSpy{}
	canceled := false
	agent.mu.Lock()
	agent.sessions[sessionID] = &codexProxySessionState{
		backend: backend,
		cancel: func() {
			canceled = true
		},
		threadID: "thread-x",
		turnID:   "turn-y",
	}
	agent.mu.Unlock()

	type contextKey string
	cancelCtx := context.WithValue(context.Background(), contextKey("cancel"), "token")
	if err := agent.Cancel(cancelCtx, acp.CancelNotification{SessionId: sessionID}); err != nil {
		t.Fatalf("Cancel(active) error = %v", err)
	}

	if !canceled {
		t.Fatal("cancel function was not called")
	}
	if got, want := backend.interruptCalls, 1; got != want {
		t.Fatalf("TurnInterrupt calls = %d, want %d", got, want)
	}
	if backend.lastThreadID != "thread-x" || backend.lastTurnID != "turn-y" {
		t.Fatalf("TurnInterrupt args = (%q,%q), want (%q,%q)", backend.lastThreadID, backend.lastTurnID, "thread-x", "turn-y")
	}
	if got := backend.lastCtx.Value(contextKey("cancel")); got != "token" {
		t.Fatalf("TurnInterrupt ctx value = %#v, want %q", got, "token")
	}
}

func TestCloseAllSessionBackends(t *testing.T) {
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, nil
	}, "agent", codexAppConfig{}, &logger)

	backendA := &lifecycleBackendSpy{}
	backendB := &lifecycleBackendSpy{}
	cancelACalled := false

	agent.mu.Lock()
	agent.sessions["a"] = &codexProxySessionState{
		backend:  backendA,
		cancel:   func() { cancelACalled = true },
		threadID: "thread-a",
		turnID:   "turn-a",
	}
	agent.sessions["b"] = &codexProxySessionState{
		backend:  backendB,
		threadID: "thread-b",
		turnID:   "turn-b",
	}
	agent.mu.Unlock()

	agent.closeAllSessionBackends()

	if !cancelACalled {
		t.Fatal("closeAllSessionBackends() did not call cancel func")
	}
	if backendA.closeCalls != 1 || backendA.waitCalls != 1 {
		t.Fatalf("backendA close/wait = (%d,%d), want (1,1)", backendA.closeCalls, backendA.waitCalls)
	}
	if backendB.closeCalls != 1 || backendB.waitCalls != 1 {
		t.Fatalf("backendB close/wait = (%d,%d), want (1,1)", backendB.closeCalls, backendB.waitCalls)
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()
	for id, state := range agent.sessions {
		if state.backend != nil || state.threadID != "" || state.turnID != "" {
			t.Fatalf("session %q not reset: %#v", id, state)
		}
	}
}

func TestRespondWithFallback(t *testing.T) {
	session := newFakeAppServerSession("ua-test/1", "thr", "turn")
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &logger)

	if err := agent.respondWithFallback(context.Background(), nil, nil); err != nil {
		t.Fatalf("respondWithFallback(nil,nil) error = %v", err)
	}

	requestID := json.RawMessage(`1`)
	cases := []struct {
		method string
	}{
		{method: "item/commandExecution/requestApproval"},
		{method: "item/fileChange/requestApproval"},
		{method: "item/permissions/requestApproval"},
		{method: "unsupported/method"},
	}
	for _, tc := range cases {
		req := &appServerRequest{ID: requestID, Method: tc.method}
		if err := agent.respondWithFallback(context.Background(), session, req); err != nil {
			t.Fatalf("respondWithFallback(%q) error = %v", tc.method, err)
		}
	}

	responses := session.responsesSnapshot()
	if len(responses) < 3 {
		t.Fatalf("responses len = %d, want at least 3", len(responses))
	}
	if got := stringValue(responses[0], "decision"); got != decisionCancel {
		t.Fatalf("command fallback decision = %q, want %q", got, decisionCancel)
	}
	if got := stringValue(responses[1], "decision"); got != decisionDecline {
		t.Fatalf("file fallback decision = %q, want %q", got, decisionDecline)
	}
	if got := stringValue(responses[2], "scope"); got != "turn" {
		t.Fatalf("permissions fallback scope = %q, want %q", got, "turn")
	}

	errorResponses := session.errorResponsesSnapshot()
	if len(errorResponses) == 0 {
		t.Fatal("expected unsupported method error response")
	}
	last := errorResponses[len(errorResponses)-1]
	if got, ok := int64Value(last, "code"); !ok || got != -32601 {
		t.Fatalf("unsupported method code = (%d,%t), want (-32601,true)", got, ok)
	}
	data := mapValue(last, "data")
	if got := stringValue(data, "method"); got != "unsupported/method" {
		t.Fatalf("unsupported method data.method = %q, want %q", got, "unsupported/method")
	}
}

func TestPermissionOptionKindAndLabel(t *testing.T) {
	if got := permissionOptionKind(decisionAccept); got != acp.PermissionOptionKindAllowOnce {
		t.Fatalf("permissionOptionKind(accept) = %q, want allow_once", got)
	}
	if got := permissionOptionKind(decisionAcceptForSession); got != acp.PermissionOptionKindAllowAlways {
		t.Fatalf("permissionOptionKind(acceptForSession) = %q, want allow_always", got)
	}
	if got := permissionOptionKind(decisionDecline); got != acp.PermissionOptionKindRejectOnce {
		t.Fatalf("permissionOptionKind(decline) = %q, want reject_once", got)
	}
	if got := permissionOptionKind(123); got != acp.PermissionOptionKindAllowAlways {
		t.Fatalf("permissionOptionKind(non-string) = %q, want allow_always", got)
	}

	if got := permissionOptionLabel(decisionAccept); got != "Allow once" {
		t.Fatalf("permissionOptionLabel(accept) = %q", got)
	}
	if got := permissionOptionLabel(decisionDecline); got != "Decline" {
		t.Fatalf("permissionOptionLabel(decline) = %q", got)
	}
	if got := permissionOptionLabel(" custom-choice "); got != "custom-choice" {
		t.Fatalf("permissionOptionLabel(custom) = %q, want %q", got, "custom-choice")
	}
	if got := permissionOptionLabel(123); got != "Allow" {
		t.Fatalf("permissionOptionLabel(non-string) = %q, want %q", got, "Allow")
	}
}

func TestInt64ValueAndIDMatchers(t *testing.T) {
	values := map[string]any{
		"f64": float64(6),
		"f32": float32(7),
		"i":   int(8),
		"i64": int64(9),
		"i32": int32(10),
		"bad": "x",
	}
	for _, key := range []string{"f64", "f32", "i", "i64", "i32"} {
		if _, ok := int64Value(values, key); !ok {
			t.Fatalf("int64Value(%q) ok = false, want true", key)
		}
	}
	if _, ok := int64Value(values, "bad"); ok {
		t.Fatal("int64Value(bad) ok = true, want false")
	}
	if _, ok := int64Value(nil, "x"); ok {
		t.Fatal("int64Value(nil) ok = true, want false")
	}

	params := map[string]any{"threadId": "thread-1", "turnId": "turn-1"}
	if !matchesThreadID(params, "thread-1") {
		t.Fatal("matchesThreadID() = false, want true")
	}
	if matchesThreadID(params, "other-thread") {
		t.Fatal("matchesThreadID() = true, want false for mismatch")
	}
	if !matchesThreadID(params, "") {
		t.Fatal("matchesThreadID(empty target) = false, want true")
	}
	if !matchesThreadID(map[string]any{}, "thread-1") {
		t.Fatal("matchesThreadID(missing key) = false, want true")
	}

	if !matchesTurnID(params, "turn-1") {
		t.Fatal("matchesTurnID() = false, want true")
	}
	if matchesTurnID(params, "other-turn") {
		t.Fatal("matchesTurnID() = true, want false for mismatch")
	}
	if !matchesTurnID(params, "") {
		t.Fatal("matchesTurnID(empty target) = false, want true")
	}
	if !matchesTurnID(map[string]any{}, "turn-1") {
		t.Fatal("matchesTurnID(missing key) = false, want true")
	}
}

func TestStatusAndTitleHelpers(t *testing.T) {
	if got := guardianReviewStatusToACPStatus("approved"); got != acp.ToolCallStatusCompleted {
		t.Fatalf("guardianReviewStatusToACPStatus(approved) = %q", got)
	}
	if got := guardianReviewStatusToACPStatus("denied"); got != acp.ToolCallStatusFailed {
		t.Fatalf("guardianReviewStatusToACPStatus(denied) = %q", got)
	}
	if got := guardianReviewStatusToACPStatus("unknown"); got != acp.ToolCallStatusInProgress {
		t.Fatalf("guardianReviewStatusToACPStatus(unknown) = %q", got)
	}

	if got := hookRunStatusToACPStatus("completed"); got != acp.ToolCallStatusCompleted {
		t.Fatalf("hookRunStatusToACPStatus(completed) = %q", got)
	}
	if got := hookRunStatusToACPStatus("blocked"); got != acp.ToolCallStatusFailed {
		t.Fatalf("hookRunStatusToACPStatus(blocked) = %q", got)
	}
	if got := hookRunStatusToACPStatus("pending"); got != acp.ToolCallStatusInProgress {
		t.Fatalf("hookRunStatusToACPStatus(pending) = %q", got)
	}

	if got := toolCallTitle("commandExecution", map[string]any{"command": "make test"}); got != "make test" {
		t.Fatalf("toolCallTitle(commandExecution with command) = %q", got)
	}
	if got := toolCallTitle("commandExecution", nil); got != "command execution" {
		t.Fatalf("toolCallTitle(commandExecution without command) = %q", got)
	}
	if got := toolCallTitle("fileChange", nil); got != "file change" {
		t.Fatalf("toolCallTitle(fileChange) = %q", got)
	}
	if got := toolCallTitle("mcpToolCall", map[string]any{"tool": "fetch"}); got != "fetch" {
		t.Fatalf("toolCallTitle(mcpToolCall) = %q", got)
	}
	if got := toolCallTitle("dynamicToolCall", map[string]any{}); got != "dynamic tool call" {
		t.Fatalf("toolCallTitle(dynamicToolCall) = %q", got)
	}
	if got := toolCallTitle("other-kind", nil); got != "other-kind" {
		t.Fatalf("toolCallTitle(default) = %q", got)
	}
}
