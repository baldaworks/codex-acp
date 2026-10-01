package codexacp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
)

const (
	testModelGPT54 = "gpt-5.4"
	testModelGPT55 = "gpt-5.5"
)

const (
	testApprovalOnRequest      = "on-request"
	testApprovalsReviewerGuard = "guardian_subagent"
	testCWD                    = "/tmp/work"
	testInputTypeText          = "text"
	testMCPTransportStdio      = "stdio"
	testPersonalityPragmatic   = "pragmatic"
	testPlanRunTests           = "Run tests"
	testReasoningXHigh         = "xhigh"
	testSandboxWorkspaceWrite  = "workspace-write"
	testServiceTierFlex        = "flex"
	testThreadOne              = "thr-1"
	testThreadLive             = "thr-live"
)

func TestBuildThreadStartParamsIncludesConfigAndMCPServers(t *testing.T) {
	ephemeral := true
	params := buildThreadStartParams(
		testCWD,
		codexAppConfig{
			ApprovalPolicy:    testApprovalOnRequest,
			ApprovalsReviewer: testApprovalsReviewerGuard,
			BaseInstructions:  "base",
			CompactPrompt:     "compact",
			Config: map[string]any{
				"foo": "bar",
			},
			DeveloperInstructions: "dev",
			Ephemeral:             &ephemeral,
			Model:                 testModelGPT54,
			ModelProvider:         "openai",
			Personality:           testPersonalityPragmatic,
			Profile:               "dev-profile",
			Sandbox:               testSandboxWorkspaceWrite,
			ServiceTier:           testServiceTierFlex,
		},
		"",
		map[string]acp.McpServer{
			"docs": {
				Stdio: &acp.McpServerStdio{
					Name:    "docs",
					Command: "docs-server",
					Args:    []string{"--listen"},
				},
			},
		},
	)

	if got := stringValue(params, "cwd"); got != testCWD {
		t.Fatalf("cwd = %q, want %q", got, testCWD)
	}
	if got := stringValue(params, "model"); got != testModelGPT54 {
		t.Fatalf("model = %q, want %q", got, testModelGPT54)
	}
	if got := stringValue(params, "approvalPolicy"); got != testApprovalOnRequest {
		t.Fatalf("approvalPolicy = %q, want %q", got, testApprovalOnRequest)
	}
	if got := stringValue(params, "approvalsReviewer"); got != testApprovalsReviewerGuard {
		t.Fatalf("approvalsReviewer = %q, want %q", got, testApprovalsReviewerGuard)
	}
	if got := stringValue(params, "modelProvider"); got != "openai" {
		t.Fatalf("modelProvider = %q, want %q", got, "openai")
	}
	if got := stringValue(params, "personality"); got != testPersonalityPragmatic {
		t.Fatalf("personality = %q, want %q", got, testPersonalityPragmatic)
	}
	if got := stringValue(params, "serviceTier"); got != testServiceTierFlex {
		t.Fatalf("serviceTier = %q, want %q", got, testServiceTierFlex)
	}
	if got, ok := boolValue(params, "ephemeral"); !ok || !got {
		t.Fatalf("ephemeral = %t (ok=%t), want true", got, ok)
	}
	config := mapValue(params, "config")
	if got := stringValue(config, "profile"); got != "dev-profile" {
		t.Fatalf("config.profile = %q, want %q", got, "dev-profile")
	}
	if got := stringValue(config, "compact_prompt"); got != "compact" {
		t.Fatalf("config.compact_prompt = %q, want %q", got, "compact")
	}
	if _, ok := config["mcp_servers"]; !ok {
		t.Fatalf("config.mcp_servers missing")
	}
}

func TestNewSessionAppliesCodexMetaOverridesToThreadStart(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.threadStartResp.Thread.SessionID = "backend-session-1"
	l := zerolog.Nop()
	defaultEphemeral := false
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{
		Sandbox:               "read-only",
		ApprovalPolicy:        "never",
		ApprovalsReviewer:     "user",
		BaseInstructions:      "default-base",
		DeveloperInstructions: "default-dev",
		Profile:               "default-profile",
		CompactPrompt:         "default-compact",
		ModelProvider:         "default-provider",
		Personality:           "friendly",
		ServiceTier:           "fast",
		Ephemeral:             &defaultEphemeral,
		Config: map[string]any{
			"shared":         "default",
			"profile":        "default-config-profile",
			"compact_prompt": "default-config-compact",
			"mcp_servers":    "default-config-mcp",
		},
	}, &l)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd: "/tmp/work",
		Meta: map[string]any{
			"codex": map[string]any{
				"sandbox":               testSandboxWorkspaceWrite,
				"approvalPolicy":        testApprovalOnRequest,
				"approvalsReviewer":     testApprovalsReviewerGuard,
				"baseInstructions":      "meta-base",
				"developerInstructions": "meta-dev",
				"profile":               "meta-profile",
				"compactPrompt":         "meta-compact",
				"modelProvider":         "meta-provider",
				"personality":           testPersonalityPragmatic,
				"serviceTier":           testServiceTierFlex,
				"ephemeral":             true,
				"config": map[string]any{
					"shared":                 "meta",
					"x":                      "y",
					"profile":                "from-meta-config-profile",
					"compact_prompt":         "from-meta-config-compact",
					"mcp_servers":            "from-meta-config-mcp",
					"model_reasoning_effort": testReasoningXHigh,
				},
			},
		},
		McpServers: []acp.McpServer{
			{
				Stdio: &acp.McpServerStdio{
					Name:    "docs",
					Command: "docs-server",
					Args:    []string{"--listen"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got, want := string(resp.SessionId), testThreadOne; got != want {
		t.Fatalf("NewSession().SessionId = %q, want %q", got, want)
	}
	meta := resp.Meta
	codexMeta := mapValue(meta, "codex")
	mcpMeta := mapValue(codexMeta, "mcp")
	if got := stringValue(mcpMeta, "contract"); got != mcpContractMerge {
		t.Fatalf("NewSession()._meta.codex.mcp.contract = %q, want %q", got, mcpContractMerge)
	}
	requested := listValue(mcpMeta, "requested")
	if len(requested) != 1 {
		t.Fatalf("NewSession()._meta.codex.mcp.requested len = %d, want 1", len(requested))
	}
	requestedServer, ok := requested[0].(map[string]any)
	if !ok {
		t.Fatalf("NewSession()._meta.codex.mcp.requested[0] type = %T, want map[string]any", requested[0])
	}
	if got := stringValue(requestedServer, "name"); got != "docs" {
		t.Fatalf("NewSession()._meta.codex.mcp.requested[0].name = %q, want %q", got, "docs")
	}
	if got := stringValue(requestedServer, "transport"); got != testMCPTransportStdio {
		t.Fatalf("NewSession()._meta.codex.mcp.requested[0].transport = %q, want %q", got, testMCPTransportStdio)
	}

	threadStartParams := session.threadStartParamsSnapshot()
	if len(threadStartParams) != 1 {
		t.Fatalf("thread/start calls = %d, want 1", len(threadStartParams))
	}
	params := threadStartParams[0]
	if got := stringValue(params, "sandbox"); got != testSandboxWorkspaceWrite {
		t.Fatalf("sandbox = %q, want %q", got, testSandboxWorkspaceWrite)
	}
	if got := stringValue(params, "approvalPolicy"); got != testApprovalOnRequest {
		t.Fatalf("approvalPolicy = %q, want %q", got, testApprovalOnRequest)
	}
	if got := stringValue(params, "approvalsReviewer"); got != testApprovalsReviewerGuard {
		t.Fatalf("approvalsReviewer = %q, want %q", got, testApprovalsReviewerGuard)
	}
	if got := stringValue(params, "baseInstructions"); got != "meta-base" {
		t.Fatalf("baseInstructions = %q, want %q", got, "meta-base")
	}
	if got := stringValue(params, "developerInstructions"); got != "meta-dev" {
		t.Fatalf("developerInstructions = %q, want %q", got, "meta-dev")
	}
	if got := stringValue(params, "modelProvider"); got != "meta-provider" {
		t.Fatalf("modelProvider = %q, want %q", got, "meta-provider")
	}
	if got := stringValue(params, "personality"); got != testPersonalityPragmatic {
		t.Fatalf("personality = %q, want %q", got, testPersonalityPragmatic)
	}
	if got := stringValue(params, "serviceTier"); got != testServiceTierFlex {
		t.Fatalf("serviceTier = %q, want %q", got, testServiceTierFlex)
	}
	if got, ok := boolValue(params, "ephemeral"); !ok || !got {
		t.Fatalf("ephemeral = %t (ok=%t), want true", got, ok)
	}

	config := mapValue(params, "config")
	if got := stringValue(config, "profile"); got != "meta-profile" {
		t.Fatalf("config.profile = %q, want %q", got, "meta-profile")
	}
	if got := stringValue(config, "compact_prompt"); got != "meta-compact" {
		t.Fatalf("config.compact_prompt = %q, want %q", got, "meta-compact")
	}
	if got := stringValue(config, "shared"); got != "meta" {
		t.Fatalf("config.shared = %q, want %q", got, "meta")
	}
	if got := stringValue(config, "x"); got != "y" {
		t.Fatalf("config.x = %q, want %q", got, "y")
	}
	if got := stringValue(config, "model_reasoning_effort"); got != testReasoningXHigh {
		t.Fatalf("config.model_reasoning_effort = %q, want xhigh", got)
	}

	mcpServersCfg := mapValue(config, "mcp_servers")
	docsCfg := mapValue(mcpServersCfg, "docs")
	if got := stringValue(docsCfg, "command"); got != "docs-server" {
		t.Fatalf("config.mcp_servers.docs.command = %q, want %q", got, "docs-server")
	}
}

func TestNewSessionRejectsCustomSessionIDMeta(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1"), nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd: "/tmp/work",
		Meta: map[string]any{
			"sessionId": "session-meta-1",
		},
	})
	if err == nil {
		t.Fatal("NewSession() error = nil, want non-nil")
	}
	reqErr := &acp.RequestError{}
	if !errors.As(err, &reqErr) {
		t.Fatalf("NewSession() error type = %T, want *acp.RequestError", err)
	}
	if reqErr.Code != -32602 {
		t.Fatalf("NewSession() request error code = %d, want -32602", reqErr.Code)
	}
}

func TestNewSessionRejectsUnsupportedCodexMetaKey(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1"), nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd: "/tmp/work",
		Meta: map[string]any{
			"codex": map[string]any{
				"unsupported": true,
			},
		},
	})
	if err == nil {
		t.Fatal("NewSession() error = nil, want non-nil")
	}
	reqErr := &acp.RequestError{}
	if !errors.As(err, &reqErr) {
		t.Fatalf("NewSession() error type = %T, want *acp.RequestError", err)
	}
	if reqErr.Code != -32602 {
		t.Fatalf("NewSession() request error code = %d, want -32602", reqErr.Code)
	}
}

func TestInitializeAdvertisesImagePromptCapability(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, errors.New("not used")
	}, "agent", codexAppConfig{}, &l)

	resp, err := agent.Initialize(context.Background(), acp.InitializeRequest{})
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !resp.AgentCapabilities.PromptCapabilities.Image {
		t.Fatal("prompt image capability = false, want true")
	}
	if resp.AgentCapabilities.PromptCapabilities.Audio {
		t.Fatal("prompt audio capability = true, want false")
	}
	if resp.AgentCapabilities.LoadSession {
		t.Fatal("loadSession = true, want false")
	}
	if resp.AgentCapabilities.SessionCapabilities.Resume == nil {
		t.Fatal("sessionCapabilities.resume = nil, want non-nil")
	}
	if resp.AgentCapabilities.SessionCapabilities.List == nil {
		t.Fatal("sessionCapabilities.list = nil, want non-nil")
	}
	if resp.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Fatal("sessionCapabilities.close = nil, want non-nil")
	}
	if got, want := len(resp.AuthMethods), 1; got != want {
		t.Fatalf("authMethods len = %d, want %d", got, want)
	}
	auth := resp.AuthMethods[0]
	if auth.Terminal == nil {
		t.Fatal("authMethods[0].Terminal = nil, want terminal auth")
	}
	if got, want := auth.Terminal.Id, codexLoginAuthMethodID; got != want {
		t.Fatalf("auth method id = %q, want %q", got, want)
	}
	if got, want := auth.Terminal.Type, "terminal"; got != want {
		t.Fatalf("auth method type = %q, want %q", got, want)
	}
	if got, want := auth.Terminal.Name, "Log in to Codex"; got != want {
		t.Fatalf("auth method name = %q, want %q", got, want)
	}
	if auth.Terminal.Description == nil {
		t.Fatal("auth method description = nil, want non-nil")
	}
	if got, want := auth.Terminal.Args, []string{"login"}; !slices.Equal(got, want) {
		t.Fatalf("auth method args = %#v, want %#v", got, want)
	}
	wire, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("json.Marshal(auth method) error = %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(wire, &fields); err != nil {
		t.Fatalf("json.Unmarshal(auth method) error = %v", err)
	}
	if got, want := fields["type"], "terminal"; got != want {
		t.Fatalf("auth method wire type = %#v, want %#v", got, want)
	}
}

func TestLogoutReturnsEmptyResponse(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, errors.New("not used")
	}, "agent", codexAppConfig{}, &l)

	resp, err := agent.Logout(context.Background(), acp.LogoutRequest{})
	if err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if len(resp.Meta) != 0 {
		t.Fatalf("Logout() response = %#v, want zero value", resp)
	}
}

func TestSessionModeIsStoredButNotForwardedToBackendPayloads(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{Data: []appServerModel{{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true}, {ID: testModelGPT55, DisplayName: "GPT-5.5"}}},
		{Data: []appServerModel{{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true}, {ID: testModelGPT55, DisplayName: "GPT-5.5"}}},
		{Data: []appServerModel{{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true}, {ID: testModelGPT55, DisplayName: "GPT-5.5"}}},
	}
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{Model: testModelGPT54}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	if _, err := agent.SetSessionMode(context.Background(), acp.SetSessionModeRequest{
		SessionId: newResp.SessionId,
		ModeId:    acp.SessionModeId(testSandboxWorkspaceWrite),
	}); err != nil {
		t.Fatalf("SetSessionMode() error = %v", err)
	}

	if _, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId("model"),
			Value:     acp.SessionConfigValueId(testModelGPT55),
		},
	}); err != nil {
		t.Fatalf("SetSessionConfigOption() error = %v", err)
	}
	settingsUpdates := session.settingsUpdateParamsSnapshot()
	if len(settingsUpdates) != 1 {
		t.Fatalf("thread/settings/update calls = %d, want 1", len(settingsUpdates))
	}
	if got := stringValue(settingsUpdates[0], "model"); got != testModelGPT55 {
		t.Fatalf("thread/settings/update model = %q, want %q", got, testModelGPT55)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	threadStartParams := session.threadStartParamsSnapshot()
	if len(threadStartParams) != 1 {
		t.Fatalf("thread/start calls = %d, want 1", len(threadStartParams))
	}
	if got := stringValue(threadStartParams[0], "cwd"); got != testCWD {
		t.Fatalf("thread/start cwd = %q, want %q", got, testCWD)
	}
	if _, ok := threadStartParams[0]["mode"]; ok {
		t.Fatalf("thread/start params unexpectedly include mode: %#v", threadStartParams[0])
	}

	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 1 {
		t.Fatalf("turn/start calls = %d, want 1", len(turnStartParams))
	}
	if got := stringValue(turnStartParams[0], "model"); got != testModelGPT55 {
		t.Fatalf("turn/start model = %q, want %q", got, testModelGPT55)
	}
	if _, ok := turnStartParams[0]["mode"]; ok {
		t.Fatalf("turn/start params unexpectedly include mode: %#v", turnStartParams[0])
	}
}

func TestSetSessionModelRejectsUnadvertisedModelAndKeepsPreviousModel(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{Data: []appServerModel{{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true}}},
		{Data: []appServerModel{{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true}}},
	}
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	_, err = agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId("model"),
			Value:     acp.SessionConfigValueId("gpt-5.3-codex"),
		},
	})
	if err == nil {
		t.Fatal("SetSessionConfigOption() error = nil, want invalid params error")
	}
	reqErr := &acp.RequestError{}
	if !errors.As(err, &reqErr) {
		t.Fatalf("SetSessionConfigOption() error type = %T, want *acp.RequestError", err)
	}
	if reqErr.Code != -32602 {
		t.Fatalf("SetSessionConfigOption() request error code = %d, want -32602", reqErr.Code)
	}
	if got := len(session.settingsUpdateParamsSnapshot()); got != 0 {
		t.Fatalf("thread/settings/update calls = %d, want 0", got)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 1 {
		t.Fatalf("turn/start calls = %d, want 1", len(turnStartParams))
	}
	if got := stringValue(turnStartParams[0], "model"); got != testModelGPT54 {
		t.Fatalf("turn/start model = %q, want %q", got, testModelGPT54)
	}
}

func TestPromptForwardsTextImageAndResourceLinkBlocksToTurnStart(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	imageURI := "https://example.com/image.png"
	mimeType := "application/json"
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt: []acp.ContentBlock{
			acp.TextBlock("hello"),
			{Image: &acp.ContentBlockImage{Uri: &imageURI}},
			{ResourceLink: &acp.ContentBlockResourceLink{
				Name:     "swagger.json",
				Uri:      "file:///state/attachments/swagger.json",
				MimeType: &mimeType,
			}},
		},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 1 {
		t.Fatalf("turn/start calls = %d, want 1", len(turnStartParams))
	}
	inputItems := listValue(turnStartParams[0], "input")
	if len(inputItems) != 3 {
		t.Fatalf("turn/start input items = %d, want 3", len(inputItems))
	}
	first, ok := inputItems[0].(map[string]any)
	if !ok {
		t.Fatalf("turn/start input[0] type = %T, want map[string]any", inputItems[0])
	}
	if got := stringValue(first, "type"); got != testInputTypeText {
		t.Fatalf("turn/start input[0].type = %q, want text", got)
	}
	if got := stringValue(first, "text"); got != "hello" {
		t.Fatalf("turn/start input[0].text = %q, want hello", got)
	}
	second, ok := inputItems[1].(map[string]any)
	if !ok {
		t.Fatalf("turn/start input[1] type = %T, want map[string]any", inputItems[1])
	}
	if got := stringValue(second, "type"); got != "image" {
		t.Fatalf("turn/start input[1].type = %q, want image", got)
	}
	if got := stringValue(second, "url"); got != imageURI {
		t.Fatalf("turn/start input[1].url = %q, want %q", got, imageURI)
	}
	third, ok := inputItems[2].(map[string]any)
	if !ok {
		t.Fatalf("turn/start input[2] type = %T, want map[string]any", inputItems[2])
	}
	if got := stringValue(third, "type"); got != testInputTypeText {
		t.Fatalf("turn/start input[2].type = %q, want text", got)
	}
	wantResource := `Attached resource: name="swagger.json", local_path="/state/attachments/swagger.json", mime_type="application/json"`
	if got := stringValue(third, "text"); got != wantResource {
		t.Fatalf("turn/start input[2].text = %q, want %q", got, wantResource)
	}
}

func TestPromptRejectsAudioContentBlock(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	_, err = agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt: []acp.ContentBlock{
			{
				Audio: &acp.ContentBlockAudio{
					MimeType: "audio/wav",
					Data:     "AAAA",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Prompt() error = nil, want invalid params error")
	}
	if !strings.Contains(err.Error(), "unsupported prompt content block type: audio") {
		t.Fatalf("Prompt() error = %v, want unsupported audio message", err)
	}
	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 0 {
		t.Fatalf("turn/start calls = %d, want 0", len(turnStartParams))
	}
}

func TestNewSessionIncludesModelConfigOptionsFromModelList(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				{ID: "gpt-5.4", DisplayName: "GPT-5.4", IsDefault: true},
				{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 mini"},
			},
		},
	}

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	option := requireModelOption(t, resp.ConfigOptions)
	if got := option.CurrentValue; got != acp.SessionConfigValueId("gpt-5.4") {
		t.Fatalf("model config current value = %q, want %q", got, "gpt-5.4")
	}
	if option.Category == nil || *option.Category != acp.SessionConfigOptionCategoryModel {
		t.Fatalf("model config category = %#v, want model", option.Category)
	}
	if !modelOptionsInclude(option, "gpt-5.4-mini") {
		t.Fatalf("model config options missing gpt-5.4-mini: %#v", option.Options)
	}
	if resp.Modes == nil {
		t.Fatal("NewSession().Modes = nil, want non-nil")
	}
	if got := resp.Modes.CurrentModeId; got != "" {
		t.Fatalf("NewSession().Modes.CurrentModeId = %q, want empty", got)
	}
}

func TestLoadSessionReturnsMethodNotFound(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return newFakeAppServerSession("codex_test/1.0.0", testThreadLive, "turn-1"), nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId:  "sess-load-1",
		Cwd:        testCWD,
		McpServers: []acp.McpServer{},
	})
	if err == nil {
		t.Fatal("LoadSession() error = nil, want non-nil")
	}
	reqErr := &acp.RequestError{}
	if !errors.As(err, &reqErr) {
		t.Fatalf("LoadSession() error type = %T, want *acp.RequestError", err)
	}
	if reqErr.Code != -32601 {
		t.Fatalf("LoadSession() request error code = %d, want -32601", reqErr.Code)
	}
}

func TestListSessionsMapsBackendThreadsAndCursor(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", testThreadLive, "turn-1")
	nextCursor := "cursor-2"
	session.threadListResponses = []appServerThreadListResponse{
		{
			Data: []appServerThread{
				{ID: "thr-2", Cwd: "/tmp/b", Name: "Second", UpdatedAt: 1710000000},
				{ID: "thr-1", Cwd: "/tmp/a", UpdatedAt: 1710000100},
			},
			NextCursor: &nextCursor,
		},
	}

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	cursor := " cursor-1 "
	cwd := " /tmp/filter "
	resp, err := agent.ListSessions(context.Background(), acp.ListSessionsRequest{
		Cursor: &cursor,
		Cwd:    &cwd,
	})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if resp.NextCursor == nil || *resp.NextCursor != nextCursor {
		t.Fatalf("ListSessions().NextCursor = %#v, want %q", resp.NextCursor, nextCursor)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("ListSessions().Sessions len = %d, want 2", len(resp.Sessions))
	}
	if got := string(resp.Sessions[0].SessionId); got != "thr-2" {
		t.Fatalf("sessions[0].sessionId = %q, want %q", got, "thr-2")
	}
	if got := resp.Sessions[0].Cwd; got != "/tmp/b" {
		t.Fatalf("sessions[0].cwd = %q, want %q", got, "/tmp/b")
	}
	if resp.Sessions[0].Title == nil || *resp.Sessions[0].Title != "Second" {
		t.Fatalf("sessions[0].title = %#v, want %q", resp.Sessions[0].Title, "Second")
	}
	if resp.Sessions[0].UpdatedAt == nil || *resp.Sessions[0].UpdatedAt != "2024-03-09T16:00:00Z" {
		t.Fatalf("sessions[0].updatedAt = %#v, want %q", resp.Sessions[0].UpdatedAt, "2024-03-09T16:00:00Z")
	}
	if resp.Sessions[1].Title != nil {
		t.Fatalf("sessions[1].title = %#v, want nil", resp.Sessions[1].Title)
	}

	params := session.threadListParamsSnapshot()
	if len(params) != 1 {
		t.Fatalf("thread/list calls = %d, want 1", len(params))
	}
	if got := stringValue(params[0], "cursor"); got != "cursor-1" {
		t.Fatalf("thread/list cursor = %q, want %q", got, "cursor-1")
	}
	if got := stringValue(params[0], "cwd"); got != "/tmp/filter" {
		t.Fatalf("thread/list cwd = %q, want %q", got, "/tmp/filter")
	}
	if got, ok := boolValue(params[0], "archived"); !ok || got {
		t.Fatalf("thread/list archived = %t (ok=%t), want false", got, ok)
	}
	if got := stringValue(params[0], "sortKey"); got != "updated_at" {
		t.Fatalf("thread/list sortKey = %q, want updated_at", got)
	}
	if got := stringValue(params[0], "sortDirection"); got != "desc" {
		t.Fatalf("thread/list sortDirection = %q, want desc", got)
	}
	sourceKinds, ok := params[0]["sourceKinds"].([]string)
	if !ok {
		t.Fatalf("thread/list sourceKinds type = %T, want []string", params[0]["sourceKinds"])
	}
	if len(sourceKinds) != len(appServerThreadListSourceKinds) {
		t.Fatalf("thread/list sourceKinds len = %d, want %d", len(sourceKinds), len(appServerThreadListSourceKinds))
	}
	if got, want := session.closeCalls, 1; got != want {
		t.Fatalf("backend close calls = %d, want %d", got, want)
	}
	if got, want := session.waitCalls, 1; got != want {
		t.Fatalf("backend wait calls = %d, want %d", got, want)
	}
}

func TestResumeSessionUsesACPIDAsThreadID(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-legacy", "turn-1")
	session.threadResumeResp.Thread.ID = "thr-legacy"
	session.threadResumeResp.Thread.SessionID = "sess-new"

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionId: "thr-legacy",
		Cwd:       "/tmp/work",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	threadResumeParams := session.threadResumeParamsSnapshot()
	if len(threadResumeParams) != 1 {
		t.Fatalf("thread/resume calls = %d, want 1", len(threadResumeParams))
	}
	if got := stringValue(threadResumeParams[0], "threadId"); got != "thr-legacy" {
		t.Fatalf("thread/resume threadId = %q, want %q", got, "thr-legacy")
	}
	if session.threadListCalls != 0 {
		t.Fatalf("thread/list calls = %d, want 0", session.threadListCalls)
	}
}

func TestResumeSessionIncludesLegacyModes(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-mode-1", "turn-1")
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	resp, err := agent.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionId: "thr-mode-1",
		Cwd:       "/tmp/work",
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if resp.Modes == nil {
		t.Fatal("ResumeSession().Modes = nil, want non-nil")
	}
	if got := resp.Modes.CurrentModeId; got != "" {
		t.Fatalf("ResumeSession().Modes.CurrentModeId = %q, want empty", got)
	}
}

func TestCloseSessionRemovesLoadedSessionAndUnsubscribes(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-close-1", "turn-1")
	session.threadUnsubscribeResp = appServerThreadUnsubscribeResponse{Status: "unsubscribed"}

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	cancelCalled := false
	agent.mu.Lock()
	agent.sessions["thr-close-1"] = &codexProxySessionState{
		backend: session,
		cancel: func() {
			cancelCalled = true
		},
		threadID: "thr-close-1",
		turnID:   "turn-1",
	}
	agent.mu.Unlock()

	if _, err := agent.CloseSession(context.Background(), acp.CloseSessionRequest{SessionId: "thr-close-1"}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if !cancelCalled {
		t.Fatal("CloseSession() did not call cancel")
	}
	if got, want := session.turnInterruptCalls, 1; got != want {
		t.Fatalf("turn/interrupt calls = %d, want %d", got, want)
	}
	if got, want := session.lastInterruptedThreadID, "thr-close-1"; got != want {
		t.Fatalf("interrupted threadId = %q, want %q", got, want)
	}
	if ids := session.threadUnsubscribeIDsSnapshot(); len(ids) != 1 || ids[0] != "thr-close-1" {
		t.Fatalf("thread/unsubscribe ids = %#v, want [thr-close-1]", ids)
	}
	if got, want := session.closeCalls, 1; got != want {
		t.Fatalf("backend close calls = %d, want %d", got, want)
	}
	if got, want := session.waitCalls, 1; got != want {
		t.Fatalf("backend wait calls = %d, want %d", got, want)
	}
	agent.mu.Lock()
	_, ok := agent.sessions["thr-close-1"]
	agent.mu.Unlock()
	if ok {
		t.Fatal("session still registered after close")
	}
}

func TestCloseSessionUsesTransientBackendWhenSessionNotLoaded(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-close-2", "turn-1")
	session.threadUnsubscribeResp = appServerThreadUnsubscribeResponse{Status: "notSubscribed"}

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	if _, err := agent.CloseSession(context.Background(), acp.CloseSessionRequest{SessionId: "thr-close-2"}); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if ids := session.threadUnsubscribeIDsSnapshot(); len(ids) != 1 || ids[0] != "thr-close-2" {
		t.Fatalf("thread/unsubscribe ids = %#v, want [thr-close-2]", ids)
	}
	if got, want := session.closeCalls, 1; got != want {
		t.Fatalf("backend close calls = %d, want %d", got, want)
	}
	if got, want := session.waitCalls, 1; got != want {
		t.Fatalf("backend wait calls = %d, want %d", got, want)
	}
}

func TestCloseSessionTreatsIdempotentUnsubscribeStatusesAsSuccess(t *testing.T) {
	statuses := []string{"unsubscribed", "notSubscribed", "notLoaded"}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			session := newFakeAppServerSession("codex_test/1.0.0", "thr-close-3", "turn-1")
			session.threadUnsubscribeResp = appServerThreadUnsubscribeResponse{Status: status}

			l := zerolog.Nop()
			agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
				return session, nil
			}, "agent", codexAppConfig{}, &l)

			if _, err := agent.CloseSession(context.Background(), acp.CloseSessionRequest{SessionId: "thr-close-3"}); err != nil {
				t.Fatalf("CloseSession() error = %v", err)
			}
		})
	}
}

func TestResumeSessionAcceptsPriorBridgeMCPMeta(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-live", "turn-1")
	session.threadResumeResp.Thread.ID = "thr-live"
	session.threadResumeResp.Thread.SessionID = "sess-tree-1"

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.ResumeSession(context.Background(), acp.ResumeSessionRequest{
		SessionId: "thr-live",
		Cwd:       "/tmp/work",
		Meta: map[string]any{
			"codex": map[string]any{
				"mcp": map[string]any{
					"contract": "merge",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
}

func TestLoadSessionIgnoresRequestShapeAndReturnsMethodNotFound(t *testing.T) {
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1"), nil
	}, "agent", codexAppConfig{}, &l)

	_, err := agent.LoadSession(context.Background(), acp.LoadSessionRequest{
		SessionId:             "sess-load-1",
		Cwd:                   "/tmp/work",
		McpServers:            []acp.McpServer{},
		AdditionalDirectories: []string{"/tmp/extra"},
	})
	if err == nil {
		t.Fatal("LoadSession() error = nil, want non-nil")
	}
	reqErr := &acp.RequestError{}
	if !errors.As(err, &reqErr) {
		t.Fatalf("LoadSession() error type = %T, want *acp.RequestError", err)
	}
	if reqErr.Code != -32601 {
		t.Fatalf("LoadSession() request error code = %d, want -32601", reqErr.Code)
	}
}

func TestNewSessionIncludesReasoningEffortConfigOptionsFromModelList(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, testReasoningXHigh, "minimal", "low", "medium", "high", testReasoningXHigh),
			},
		},
	}

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	option := requireReasoningEffortOption(t, resp.ConfigOptions)
	if got := option.CurrentValue; got != acp.SessionConfigValueId(testReasoningXHigh) {
		t.Fatalf("reasoning current value = %q, want xhigh", got)
	}
	if option.Category == nil || *option.Category != acp.SessionConfigOptionCategoryThoughtLevel {
		t.Fatalf("reasoning category = %#v, want %q", option.Category, sessionConfigCategoryThoughtLevel)
	}
	if !reasoningEffortOptionsInclude(option, testReasoningXHigh) {
		t.Fatalf("reasoning options missing xhigh: %#v", option.Options)
	}
}

func TestSetSessionConfigOptionReasoningEffortAppliesToNextTurn(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high", testReasoningXHigh),
			},
		},
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high", testReasoningXHigh),
			},
		},
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high", testReasoningXHigh),
			},
		},
	}
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	setResp, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId(sessionConfigIDReasoningEffort),
			Value:     acp.SessionConfigValueId(testReasoningXHigh),
		},
	})
	if err != nil {
		t.Fatalf("SetSessionConfigOption() error = %v", err)
	}
	option := requireReasoningEffortOption(t, setResp.ConfigOptions)
	if got := option.CurrentValue; got != acp.SessionConfigValueId(testReasoningXHigh) {
		t.Fatalf("reasoning current value = %q, want xhigh", got)
	}
	settingsUpdates := session.settingsUpdateParamsSnapshot()
	if len(settingsUpdates) != 1 {
		t.Fatalf("thread/settings/update calls = %d, want 1", len(settingsUpdates))
	}
	if got := stringValue(settingsUpdates[0], "effort"); got != testReasoningXHigh {
		t.Fatalf("thread/settings/update effort = %q, want %q", got, testReasoningXHigh)
	}
	if got := stringValue(settingsUpdates[0], "summary"); got != reasoningSummaryAuto {
		t.Fatalf("thread/settings/update summary = %q, want %q", got, reasoningSummaryAuto)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 1 {
		t.Fatalf("turn/start calls = %d, want 1", len(turnStartParams))
	}
	if got := stringValue(turnStartParams[0], "effort"); got != testReasoningXHigh {
		t.Fatalf("turn/start effort = %q, want xhigh", got)
	}
	if got := stringValue(turnStartParams[0], "summary"); got != reasoningSummaryAuto {
		t.Fatalf("turn/start summary = %q, want %q", got, reasoningSummaryAuto)
	}
}

func TestSetSessionConfigOptionModelAppliesToNextTurn(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high"),
				appServerModelWithReasoning(testModelGPT55, false, "high", "high", testReasoningXHigh),
			},
		},
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high"),
				appServerModelWithReasoning(testModelGPT55, false, "high", "high", testReasoningXHigh),
			},
		},
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high"),
				appServerModelWithReasoning(testModelGPT55, false, "high", "high", testReasoningXHigh),
			},
		},
	}
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	setResp, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId(sessionConfigIDModel),
			Value:     acp.SessionConfigValueId(testModelGPT55),
		},
	})
	if err != nil {
		t.Fatalf("SetSessionConfigOption() error = %v", err)
	}
	modelOption := requireModelOption(t, setResp.ConfigOptions)
	if got := modelOption.CurrentValue; got != acp.SessionConfigValueId(testModelGPT55) {
		t.Fatalf("model config current value = %q, want %q", got, testModelGPT55)
	}
	reasoningOption := requireReasoningEffortOption(t, setResp.ConfigOptions)
	if got := reasoningOption.CurrentValue; got != acp.SessionConfigValueId("high") {
		t.Fatalf("reasoning current value = %q, want high", got)
	}

	settingsUpdates := session.settingsUpdateParamsSnapshot()
	if len(settingsUpdates) != 1 {
		t.Fatalf("thread/settings/update calls = %d, want 1", len(settingsUpdates))
	}
	if got := stringValue(settingsUpdates[0], "model"); got != testModelGPT55 {
		t.Fatalf("thread/settings/update model = %q, want %q", got, testModelGPT55)
	}
	if got := stringValue(settingsUpdates[0], "summary"); got != reasoningSummaryAuto {
		t.Fatalf("thread/settings/update summary = %q, want %q", got, reasoningSummaryAuto)
	}

	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	turnStartParams := session.turnStartParamsSnapshot()
	if len(turnStartParams) != 1 {
		t.Fatalf("turn/start calls = %d, want 1", len(turnStartParams))
	}
	if got := stringValue(turnStartParams[0], "model"); got != testModelGPT55 {
		t.Fatalf("turn/start model = %q, want %q", got, testModelGPT55)
	}
	if got := stringValue(turnStartParams[0], "summary"); got != reasoningSummaryAuto {
		t.Fatalf("turn/start summary = %q, want %q", got, reasoningSummaryAuto)
	}
}

func TestPreloadedTurnCompletedNotificationFlushesOnFirstTurnStart(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("Prompt().StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}
	if got := len(session.turnStartParamsSnapshot()); got != 1 {
		t.Fatalf("turn/start calls = %d, want 1", got)
	}
}

func TestSetSessionConfigOptionRejectsUnsupportedReasoningEffort(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high"),
			},
		},
		{
			Data: []appServerModel{
				appServerModelWithReasoning("gpt-5.4", true, "medium", "low", "medium", "high"),
			},
		},
	}

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	_, err = agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId(sessionConfigIDReasoningEffort),
			Value:     acp.SessionConfigValueId(testReasoningXHigh),
		},
	})
	if err == nil {
		t.Fatal("SetSessionConfigOption() error = nil, want unsupported value error")
	}
	if !strings.Contains(err.Error(), sessionConfigOptionValueUnsupported) {
		t.Fatalf("SetSessionConfigOption() error = %v, want unsupported value", err)
	}
}

func TestSetSessionConfigOptionRejectsUnknownConfigID(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	_, err = agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId("unknown"),
			Value:     acp.SessionConfigValueId(testReasoningXHigh),
		},
	})
	if err == nil {
		t.Fatal("SetSessionConfigOption() error = nil, want unsupported config error")
	}
	if !strings.Contains(err.Error(), sessionConfigOptionIDUnsupported) {
		t.Fatalf("SetSessionConfigOption() error = %v, want unsupported config", err)
	}
}

func TestNewSessionModelListPaginationAndThreadModelPrecedence(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.threadStartResp.Model = "gpt-5.4-mini"
	cursor := "page-2"
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				{ID: "gpt-5.4", DisplayName: "GPT-5.4", IsDefault: true},
			},
			NextCursor: &cursor,
		},
		{
			Data: []appServerModel{
				{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 mini"},
			},
		},
	}

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	option := requireModelOption(t, resp.ConfigOptions)
	if got := option.CurrentValue; got != acp.SessionConfigValueId("gpt-5.4-mini") {
		t.Fatalf("model config current value = %q, want %q", got, "gpt-5.4-mini")
	}
	if got := len(session.modelListParamsSnapshot()); got != 2 {
		t.Fatalf("model/list calls = %d, want 2", got)
	}
}

func TestNewSessionNormalizesUnsupportedThreadModelToAdvertisedDefault(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.threadStartResp.Model = "gpt-5.3-codex"
	session.modelListResponses = []appServerModelListResponse{
		{
			Data: []appServerModel{
				{ID: testModelGPT54, DisplayName: "GPT-5.4", IsDefault: true},
				{ID: "gpt-5.4-mini", DisplayName: "GPT-5.4 mini"},
			},
		},
	}

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	option := requireModelOption(t, resp.ConfigOptions)
	if got := option.CurrentValue; got != acp.SessionConfigValueId(testModelGPT54) {
		t.Fatalf("model config current value = %q, want %q", got, testModelGPT54)
	}
}

func TestNewSessionContinuesWhenModelListFails(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	session.modelListErr = errors.New("boom")

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	resp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if got := strings.TrimSpace(string(resp.SessionId)); got == "" {
		t.Fatal("session id = empty, want non-empty")
	}
	if len(resp.ConfigOptions) != 0 {
		t.Fatalf("NewSession().ConfigOptions = %#v, want empty on model/list error", resp.ConfigOptions)
	}
}

func TestHandleNotificationCommandExecOutputDeltaDoesNotEmitThought(t *testing.T) {
	sessionID := acp.SessionId("s1")
	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, errors.New("not used")
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)
	agent.sessions[sessionID] = &codexProxySessionState{
		threadID: "thr-1",
		turnID:   "turn-1",
	}

	raw, err := json.Marshal(map[string]any{
		"threadId":    "thr-1",
		"turnId":      "turn-1",
		"processId":   "proc-1",
		"stream":      "stdout",
		"deltaBase64": "QUJDRA==",
		"capReached":  false,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	done, stopReason, usage, errorMeta, err := agent.handleNotification(context.Background(), sessionID, "thr-1", "turn-1", true, &appServerNotification{
		Method: "command/exec/outputDelta",
		Params: raw,
	})
	if err != nil {
		t.Fatalf("handleNotification() error = %v", err)
	}
	if done {
		t.Fatalf("handleNotification() done = %v, want false", done)
	}
	if stopReason != "" {
		t.Fatalf("handleNotification() stopReason = %q, want empty", stopReason)
	}
	if usage != nil {
		t.Fatalf("handleNotification() usage = %#v, want nil", usage)
	}
	if errorMeta != nil {
		t.Fatalf("handleNotification() errorMeta = %#v, want nil", errorMeta)
	}

	updates := conn.sessionUpdates(sessionID)
	if containsThoughtSubstring(updates, "QUJDRA==") {
		t.Fatalf("unexpected raw delta payload in thought update: %#v", updates)
	}
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected thought updates for command/exec output delta: %#v", updates)
	}
}

func TestUsageFromTokenNotificationUsesLastFieldsOnly(t *testing.T) {
	usage := usageFromTokenNotification(map[string]any{
		"tokenUsage": map[string]any{
			"last": map[string]any{
				"inputTokens":           10,
				"outputTokens":          2,
				"totalTokens":           12,
				"cachedInputTokens":     4,
				"reasoningOutputTokens": 3,
			},
			"total": map[string]any{
				"inputTokens": 999,
			},
			"modelContextWindow": 200000,
		},
	})
	if usage == nil {
		t.Fatal("usage = nil, want non-nil")
	}
	if got := usage["inputTokens"]; got != 10 {
		t.Fatalf("usage.inputTokens = %#v, want %d", got, 10)
	}
	if got := usage["outputTokens"]; got != 2 {
		t.Fatalf("usage.outputTokens = %#v, want %d", got, 2)
	}
	if got := usage["totalTokens"]; got != 12 {
		t.Fatalf("usage.totalTokens = %#v, want %d", got, 12)
	}
	if got := usage["cachedReadTokens"]; got != 4 {
		t.Fatalf("usage.cachedReadTokens = %#v, want %d", got, 4)
	}
	if got := usage["thoughtTokens"]; got != 3 {
		t.Fatalf("usage.thoughtTokens = %#v, want %d", got, 3)
	}
	if _, ok := usage["modelContextWindow"]; ok {
		t.Fatalf("usage unexpectedly includes modelContextWindow: %#v", usage)
	}
	acpUsage := acpUsageFromMap(usage)
	if acpUsage == nil {
		t.Fatal("acpUsageFromMap() = nil, want non-nil")
	}
	if acpUsage.InputTokens != 10 {
		t.Fatalf("acpUsage.InputTokens = %d, want 10", acpUsage.InputTokens)
	}
	if acpUsage.OutputTokens != 2 {
		t.Fatalf("acpUsage.OutputTokens = %d, want 2", acpUsage.OutputTokens)
	}
	if acpUsage.TotalTokens != 12 {
		t.Fatalf("acpUsage.TotalTokens = %d, want 12", acpUsage.TotalTokens)
	}
	if acpUsage.CachedReadTokens == nil || *acpUsage.CachedReadTokens != 4 {
		t.Fatalf("acpUsage.CachedReadTokens = %#v, want 4", acpUsage.CachedReadTokens)
	}
	if acpUsage.ThoughtTokens == nil || *acpUsage.ThoughtTokens != 3 {
		t.Fatalf("acpUsage.ThoughtTokens = %#v, want 3", acpUsage.ThoughtTokens)
	}
}

func TestResolveAgentIdentityFromUserAgent(t *testing.T) {
	name, version := resolveAgentIdentity("", parseAppServerIdentity("codex_vscode/0.1.0 (darwin)"))
	if name != "codex_vscode" {
		t.Fatalf("name = %q, want %q", name, "codex_vscode")
	}
	if version != "0.1.0" {
		t.Fatalf("version = %q, want %q", version, "0.1.0")
	}
}

func TestPromptStreamsAppServerNotificationsToACPUpdates(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "commandExecution",
			"id":      "item-cmd-1",
			"status":  "inProgress",
			"command": "go test ./...",
		},
	})
	queueNotification(session, "item/commandExecution/outputDelta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-cmd-1",
		"delta":    "  ok   ./...",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":   "commandExecution",
			"id":     "item-cmd-1",
			"status": "completed",
		},
	})
	queueNotification(session, "turn/plan/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"plan": []any{
			map[string]any{"step": "Run tests", "status": "completed"},
		},
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "Hi",
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    " ",
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "done",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "Hi done",
			"phase": "final_answer",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if len(updates) == 0 {
		t.Fatal("expected ACP session updates")
	}
	if !containsAgentMessageText(updates, "Hi done") {
		t.Fatalf("missing completed agent message in ACP updates: %#v", updates)
	}
	if !containsToolCallText(updates, "  ok   ./...") {
		t.Fatalf("missing tool call output delta with leading spaces in ACP updates: %#v", updates)
	}
	if !containsPlanEntry(updates, testPlanRunTests) {
		t.Fatalf("missing plan update in ACP updates: %#v", updates)
	}
	planEntries := latestPlanEntries(updates)
	if len(planEntries) != 1 || planEntries[0].Content != testPlanRunTests || planEntries[0].Status != acp.PlanEntryStatusCompleted {
		t.Fatalf("latest plan entries = %#v, want one completed snapshot entry", planEntries)
	}
	if !containsToolCall(updates, "codex-item-item-cmd-1") {
		t.Fatalf("missing tool call start/update in ACP updates: %#v", updates)
	}
}

func TestPromptDoesNotProjectNonToolItemLifecycleAsToolCalls(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/reasoning/textDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"contentIndex": 0,
		"delta":        "thinking",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{},
			"content": []any{"thinking"},
		},
	})
	for _, itemType := range []string{"plan", "agentMessage"} {
		itemID := "item-" + itemType + "-1"
		queueNotification(session, "item/started", map[string]any{
			"threadId": "thr-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type":   itemType,
				"id":     itemID,
				"status": "inProgress",
			},
		})
		queueNotification(session, "item/completed", map[string]any{
			"threadId": "thr-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type":   itemType,
				"id":     itemID,
				"status": "completed",
			},
		})
	}
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if countToolCallEvents(updates) != 0 {
		t.Fatalf("unexpected tool call lifecycle updates for non-tool items: %#v", updates)
	}
	if countThoughtText(updates, "thinking") != 1 {
		t.Fatalf("missing reasoning text delta thought update: %#v", updates)
	}
}

func TestPromptEmitsCompletedReasoningThoughtsWhenReasoningStreamingDisabled(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"First sentence.", "Second sentence. <!-- -->"},
			"content": []any{"raw one"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	opts := Options{}
	opts.SetReasoningStreaming(false)
	agent.setBridgeOptions(opts)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 1 {
		t.Fatalf("thought chunk count = %d, want 1: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "First sentence. Second sentence." {
		t.Fatalf("thought text = %q, want normalized aggregate", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindSummary {
		t.Fatalf("thought reasoning kind = %q, want %q", got, reasoningKindSummary)
	}
	if got, ok := thoughtChunkMetaBool(chunks[0], metaCompletedKey); !ok || !got {
		t.Fatalf("thought completed meta = (%t,%t), want (true,true)", got, ok)
	}
	if countThoughtText(conn.sessionUpdates(newResp.SessionId), "raw one") != 0 {
		t.Fatalf("unexpected raw reasoning content in default summary mode: %#v", chunks)
	}
}

func TestPromptEmitsCompletedReasoningBothLanesWhenReasoningStreamingDisabled(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"summary"},
			"content": []any{"raw"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	opts := Options{ReasoningThoughts: "both"}
	opts.SetReasoningStreaming(false)
	agent.setBridgeOptions(opts)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 2 {
		t.Fatalf("thought chunk count = %d, want 2: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "summary" {
		t.Fatalf("first thought text = %q, want summary", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindSummary {
		t.Fatalf("first thought reasoning kind = %q, want %q", got, reasoningKindSummary)
	}
	if got := thoughtChunkText(chunks[1]); got != "raw" {
		t.Fatalf("second thought text = %q, want raw", got)
	}
	if got := thoughtChunkMetaString(chunks[1], metaReasoningKindKey); got != reasoningKindContent {
		t.Fatalf("second thought reasoning kind = %q, want %q", got, reasoningKindContent)
	}
	if !isUUIDString(thoughtChunkMessageID(chunks[0])) {
		t.Fatalf("first thought messageId = %q, want UUID", thoughtChunkMessageID(chunks[0]))
	}
	if !isUUIDString(thoughtChunkMessageID(chunks[1])) {
		t.Fatalf("second thought messageId = %q, want UUID", thoughtChunkMessageID(chunks[1]))
	}
	if thoughtChunkMessageID(chunks[0]) == thoughtChunkMessageID(chunks[1]) {
		t.Fatalf("summary and content thought message IDs must differ: %q", thoughtChunkMessageID(chunks[0]))
	}
	for i, chunk := range chunks {
		if got, ok := thoughtChunkMetaBool(chunk, metaCompletedKey); !ok || !got {
			t.Fatalf("thought %d completed meta = (%t,%t), want (true,true)", i, got, ok)
		}
	}
}

func TestPromptEmitsCompletedReasoningContentWhenReasoningStreamingDisabled(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"summary"},
			"content": []any{"raw one", "raw two"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	opts := Options{ReasoningThoughts: "content"}
	opts.SetReasoningStreaming(false)
	agent.setBridgeOptions(opts)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 1 {
		t.Fatalf("thought chunk count = %d, want 1: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "raw one raw two" {
		t.Fatalf("thought text = %q, want aggregated content", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindContent {
		t.Fatalf("thought reasoning kind = %q, want %q", got, reasoningKindContent)
	}
	if countThoughtText(conn.sessionUpdates(newResp.SessionId), "summary") != 0 {
		t.Fatalf("unexpected summary reasoning text in content mode: %#v", chunks)
	}
}

func TestPromptSuppressesReasoningThoughtsWhenConfiguredOff(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 0,
		"delta":        "summary",
	})
	queueNotification(session, "item/reasoning/textDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"contentIndex": 0,
		"delta":        "raw",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"completed summary"},
			"content": []any{"completed raw"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "off"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	if chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId)); len(chunks) != 0 {
		t.Fatalf("thought chunk count = %d, want 0: %#v", len(chunks), chunks)
	}
}

func TestReasoningThoughtChunkUpdateCarriesStableMessageAndItemIDs(t *testing.T) {
	first := reasoningThoughtChunkUpdate("first", "item-reasoning-1", reasoningKindSummary, 0, false).AgentThoughtChunk
	again := reasoningThoughtChunkUpdate("again", "item-reasoning-1", reasoningKindSummary, 0, true).AgentThoughtChunk
	content := reasoningThoughtChunkUpdate("content", "item-reasoning-1", reasoningKindContent, 0, false).AgentThoughtChunk
	nextSummary := reasoningThoughtChunkUpdate("next", "item-reasoning-1", reasoningKindSummary, 1, false).AgentThoughtChunk
	otherItem := reasoningThoughtChunkUpdate("other", "item-reasoning-2", reasoningKindSummary, 0, false).AgentThoughtChunk

	if got := thoughtChunkMetaString(first, metaItemIDKey); got != "item-reasoning-1" {
		t.Fatalf("thought itemId meta = %q, want item-reasoning-1", got)
	}
	if got := thoughtChunkMetaString(first, metaReasoningKindKey); got != reasoningKindSummary {
		t.Fatalf("thought reasoning kind meta = %q, want %q", got, reasoningKindSummary)
	}
	if got := thoughtChunkMetaInt64(first, metaSummaryIndexKey); got != 0 {
		t.Fatalf("thought summary index meta = %d, want 0", got)
	}
	if got, ok := thoughtChunkMetaBool(first, metaCompletedKey); !ok || got {
		t.Fatalf("thought completed meta = (%t,%t), want (false,true)", got, ok)
	}
	if got, ok := thoughtChunkMetaBool(again, metaCompletedKey); !ok || !got {
		t.Fatalf("completed thought meta = (%t,%t), want (true,true)", got, ok)
	}
	if !isUUIDString(thoughtChunkMessageID(first)) {
		t.Fatalf("thought messageId = %q, want UUID", thoughtChunkMessageID(first))
	}
	if thoughtChunkMessageID(first) != thoughtChunkMessageID(again) {
		t.Fatalf("same item/kind/index messageIds differ: %q vs %q", thoughtChunkMessageID(first), thoughtChunkMessageID(again))
	}
	for name, chunk := range map[string]*acp.SessionUpdateAgentThoughtChunk{
		"content":      content,
		"next summary": nextSummary,
		"other item":   otherItem,
	} {
		if thoughtChunkMessageID(first) == thoughtChunkMessageID(chunk) {
			t.Fatalf("%s messageId = %q, must differ from summary item/index messageId", name, thoughtChunkMessageID(chunk))
		}
	}
}

func TestPromptStreamsReasoningSummaryThoughtsByDefault(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/reasoning/textDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"contentIndex": 0,
		"delta":        "raw",
	})
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 0,
		"delta":        "summary",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"summary"},
			"content": []any{"raw"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 2 {
		t.Fatalf("thought chunk count = %d, want 2: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "summary" {
		t.Fatalf("first thought text = %q, want summary", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindSummary {
		t.Fatalf("first thought reasoning kind = %q, want %q", got, reasoningKindSummary)
	}
	if got, ok := thoughtChunkMetaBool(chunks[0], metaCompletedKey); !ok || got {
		t.Fatalf("first thought completed meta = (%t,%t), want (false,true)", got, ok)
	}
	if got := thoughtChunkText(chunks[1]); got != "" {
		t.Fatalf("second thought text = %q, want empty closing chunk", got)
	}
	if got, ok := thoughtChunkMetaBool(chunks[1], metaCompletedKey); !ok || !got {
		t.Fatalf("second thought completed meta = (%t,%t), want (true,true)", got, ok)
	}
	if thoughtChunkMessageID(chunks[0]) == "" || thoughtChunkMessageID(chunks[0]) != thoughtChunkMessageID(chunks[1]) {
		t.Fatalf("streamed summary message IDs = (%q, %q), want matching non-empty IDs", thoughtChunkMessageID(chunks[0]), thoughtChunkMessageID(chunks[1]))
	}
	if countThoughtText(conn.sessionUpdates(newResp.SessionId), "raw") != 0 {
		t.Fatalf("unexpected raw reasoning text in default summary mode: %#v", chunks)
	}
}

func TestPromptPublishesCompletedSummaryPartsWithoutTokenStreaming(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 0,
		"delta":        "first summary",
	})
	queueNotification(session, "item/reasoning/summaryPartAdded", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 1,
	})
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 1,
		"delta":        "second summary",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"first summary", "second summary"},
			"content": []any{"raw ignored"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	opts := Options{}
	opts.SetReasoningStreaming(false)
	agent.setBridgeOptions(opts)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 2 {
		t.Fatalf("thought chunk count = %d, want 2: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "first summary" {
		t.Fatalf("first thought text = %q, want first summary", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindSummary {
		t.Fatalf("first thought reasoning kind = %q, want %q", got, reasoningKindSummary)
	}
	if got := thoughtChunkMetaInt64(chunks[0], metaSummaryIndexKey); got != 0 {
		t.Fatalf("first thought summary index = %d, want 0", got)
	}
	if got, ok := thoughtChunkMetaBool(chunks[0], metaCompletedKey); !ok || !got {
		t.Fatalf("first thought completed meta = (%t,%t), want (true,true)", got, ok)
	}
	if got := thoughtChunkText(chunks[1]); got != "second summary" {
		t.Fatalf("second thought text = %q, want second summary", got)
	}
	if got := thoughtChunkMetaInt64(chunks[1], metaSummaryIndexKey); got != 1 {
		t.Fatalf("second thought summary index = %d, want 1", got)
	}
	if got, ok := thoughtChunkMetaBool(chunks[1], metaCompletedKey); !ok || !got {
		t.Fatalf("second thought completed meta = (%t,%t), want (true,true)", got, ok)
	}
	if countThoughtText(conn.sessionUpdates(newResp.SessionId), "raw ignored") != 0 {
		t.Fatalf("unexpected raw reasoning text in summary mode: %#v", chunks)
	}
}

func TestPromptFallsBackToCompletedReasoningContentInSummaryMode(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{},
			"content": []any{"raw fallback"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 1 {
		t.Fatalf("thought chunk count = %d, want 1: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkText(chunks[0]); got != "raw fallback" {
		t.Fatalf("thought text = %q, want raw fallback", got)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindContent {
		t.Fatalf("thought reasoning kind = %q, want %q", got, reasoningKindContent)
	}
	if got, ok := thoughtChunkMetaBool(chunks[0], metaCompletedKey); !ok || !got {
		t.Fatalf("thought completed meta = (%t,%t), want (true,true)", got, ok)
	}
}

func TestPromptStreamsReasoningContentThoughtsWhenConfigured(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/reasoning/textDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"contentIndex": 0,
		"delta":        "raw",
	})
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 0,
		"delta":        "summary",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"summary"},
			"content": []any{"raw"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "content"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 2 {
		t.Fatalf("thought chunk count = %d, want 2: %#v", len(chunks), chunks)
	}
	if got := thoughtChunkMetaString(chunks[0], metaReasoningKindKey); got != reasoningKindContent {
		t.Fatalf("first thought reasoning kind = %q, want %q", got, reasoningKindContent)
	}
	if got := thoughtChunkText(chunks[0]); got != "raw" {
		t.Fatalf("first thought text = %q, want raw", got)
	}
	if countThoughtText(conn.sessionUpdates(newResp.SessionId), "summary") != 0 {
		t.Fatalf("unexpected summary reasoning text in content mode: %#v", chunks)
	}
}

func TestPromptStreamsReasoningBothLanesWhenConfigured(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "reasoning",
			"id":   "item-reasoning-1",
		},
	})
	queueNotification(session, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"summaryIndex": 0,
		"delta":        "summary",
	})
	queueNotification(session, "item/reasoning/textDelta", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reasoning-1",
		"contentIndex": 0,
		"delta":        "raw",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "reasoning",
			"id":      "item-reasoning-1",
			"summary": []any{"summary"},
			"content": []any{"raw"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{ReasoningThoughts: "both"})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := thoughtChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 4 {
		t.Fatalf("thought chunk count = %d, want 4: %#v", len(chunks), chunks)
	}
	if !containsThoughtSubstring(conn.sessionUpdates(newResp.SessionId), "summary") || !containsThoughtSubstring(conn.sessionUpdates(newResp.SessionId), "raw") {
		t.Fatalf("missing expected thought texts in both mode: %#v", chunks)
	}
}

func TestPromptSuppressesCommentaryAgentMessage(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "working",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "working",
			"phase": "commentary",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if countAgentMessageChunks(updates) != 0 {
		t.Fatalf("unexpected agent message chunks for commentary: %#v", updates)
	}
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected thought chunks for commentary: %#v", updates)
	}
}

func TestPromptStreamsAgentMessageDeltasWhenMessageStreamingEnabled(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "",
			"phase": "commentary",
		},
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "work",
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "ing",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "working",
			"phase": "commentary",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{MessageStreaming: true})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := agentMessageChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 3 {
		t.Fatalf("agent message chunks = %d, want 3", len(chunks))
	}
	if got := chunkText(chunks[0]); got != "work" {
		t.Fatalf("first chunk text = %q, want %q", got, "work")
	}
	if got := chunkText(chunks[1]); got != "ing" {
		t.Fatalf("second chunk text = %q, want %q", got, "ing")
	}
	for i := 0; i < 2; i++ {
		if got := chunkMetaString(chunks[i], metaItemIDKey); got != "item-msg-1" {
			t.Fatalf("chunk %d itemId = %q, want item-msg-1", i, got)
		}
		if got, ok := chunkMetaBool(chunks[i], metaCompletedKey); !ok || got {
			t.Fatalf("chunk %d completed = %v,%v, want false,true", i, got, ok)
		}
		if got := chunkMetaString(chunks[i], metaPhaseKey); got != "commentary" {
			t.Fatalf("chunk %d phase = %q, want commentary", i, got)
		}
	}
	if got := chunkText(chunks[2]); got != "" {
		t.Fatalf("closing chunk text = %q, want empty", got)
	}
	if got, ok := chunkMetaBool(chunks[2], metaCompletedKey); !ok || !got {
		t.Fatalf("closing chunk completed = %v,%v, want true,true", got, ok)
	}
}

func TestPromptForwardsCompletedAgentMessageForVisiblePhases(t *testing.T) {
	tests := []struct {
		name     string
		hasPhase bool
		phase    any
	}{
		{name: "final answer", hasPhase: true, phase: "final_answer"},
		{name: "null phase", hasPhase: true, phase: nil},
		{name: "empty phase", hasPhase: true, phase: ""},
		{name: "missing phase"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
			item := map[string]any{
				"type": "agentMessage",
				"id":   "item-msg-1",
				"text": "visible answer",
			}
			if tt.hasPhase {
				item["phase"] = tt.phase
			}
			queueNotification(session, "item/completed", map[string]any{
				"threadId": "thr-1",
				"turnId":   "turn-1",
				"item":     item,
			})
			queueNotification(session, "turn/completed", map[string]any{
				"threadId": "thr-1",
				"turn": map[string]any{
					"id":     "turn-1",
					"status": "completed",
				},
			})

			conn := &fakeACPAppConnection{}
			l := zerolog.Nop()
			agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
				return session, nil
			}, "agent", codexAppConfig{}, &l)
			agent.setConnection(conn)

			newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
			if err != nil {
				t.Fatalf("NewSession() error = %v", err)
			}
			if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
				SessionId: newResp.SessionId,
				Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
			}); err != nil {
				t.Fatalf("Prompt() error = %v", err)
			}

			updates := conn.sessionUpdates(newResp.SessionId)
			if !containsAgentMessageText(updates, "visible answer") {
				t.Fatalf("missing visible agent message: %#v", updates)
			}
			if countThoughtChunks(updates) != 0 {
				t.Fatalf("unexpected thought chunks for visible agent message: %#v", updates)
			}
		})
	}
}

func TestPromptIgnoresAgentMessageDeltasWhenMessageStreamingDisabled(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-msg-1",
		"delta":    "draft",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "final",
			"phase": "final_answer",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if !containsAgentMessageText(updates, "final") {
		t.Fatalf("missing completed agent message text: %#v", updates)
	}
	if containsAgentMessageText(updates, "draft") {
		t.Fatalf("unexpected buffered draft agent message text: %#v", updates)
	}
}

func TestPromptEmitsCompletedAgentMessageTextWhenStreamingEnabledWithoutDeltas(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "",
			"phase": "final_answer",
		},
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "final",
			"phase": "final_answer",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setBridgeOptions(Options{MessageStreaming: true})
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	chunks := agentMessageChunks(conn.sessionUpdates(newResp.SessionId))
	if len(chunks) != 1 {
		t.Fatalf("agent message chunks = %d, want 1", len(chunks))
	}
	if got := chunkText(chunks[0]); got != "final" {
		t.Fatalf("chunk text = %q, want final", got)
	}
	if got := chunkMetaString(chunks[0], metaItemIDKey); got != "item-msg-1" {
		t.Fatalf("chunk itemId = %q, want item-msg-1", got)
	}
	if got, ok := chunkMetaBool(chunks[0], metaCompletedKey); !ok || !got {
		t.Fatalf("chunk completed = %v,%v, want true,true", got, ok)
	}
	if got := chunkMetaString(chunks[0], metaPhaseKey); got != "final_answer" {
		t.Fatalf("chunk phase = %q, want final_answer", got)
	}
}

func TestCompletedFinalAnswerDoesNotCompletePrompt(t *testing.T) {
	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)
	sessionID := acp.SessionId("session-phase")
	agent.mu.Lock()
	agent.sessions[sessionID] = &codexProxySessionState{}
	agent.mu.Unlock()

	raw, err := json.Marshal(map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "done",
			"phase": "final_answer",
		},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	done, stopReason, usage, errorMeta, err := agent.handleNotification(context.Background(), sessionID, "thr-1", "turn-1", true, &appServerNotification{
		Method: "item/completed",
		Params: raw,
	})
	if err != nil {
		t.Fatalf("handleNotification() error = %v", err)
	}
	if done {
		t.Fatalf("handleNotification() done = %v, want false", done)
	}
	if stopReason != "" {
		t.Fatalf("handleNotification() stopReason = %q, want empty", stopReason)
	}
	if usage != nil {
		t.Fatalf("handleNotification() usage = %#v, want nil", usage)
	}
	if errorMeta != nil {
		t.Fatalf("handleNotification() errorMeta = %#v, want nil", errorMeta)
	}
	if !containsAgentMessageText(conn.sessionUpdates(sessionID), "done") {
		t.Fatalf("missing final answer agent message update: %#v", conn.sessionUpdates(sessionID))
	}
}

func TestPromptBridgesCommandApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/commandExecution/requestApproval", json.RawMessage("1"), map[string]any{
		"threadId":           "thr-1",
		"turnId":             "turn-1",
		"itemId":             "item-cmd-1",
		"command":            "curl example.com",
		"cwd":                "/tmp/work",
		"reason":             "The command needs network access.",
		"availableDecisions": []any{decisionDecline, decisionAcceptForSession},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-2"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	if got := len(conn.permissionRequests); got != 1 {
		t.Fatalf("permission requests = %d, want 1", got)
	}
	request := conn.permissionRequests[0]
	if len(request.ToolCall.Content) != 1 || request.ToolCall.Content[0].Content == nil || request.ToolCall.Content[0].Content.Content.Text == nil {
		t.Fatalf("permission content = %#v, want one text block", request.ToolCall.Content)
	}
	wantContent := "The command needs network access.\n\nCommand:\n```sh\ncurl example.com\n```\n\nWorking directory: `/tmp/work`"
	if got := request.ToolCall.Content[0].Content.Content.Text.Text; got != wantContent {
		t.Fatalf("permission content = %q, want %q", got, wantContent)
	}
	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	decision := stringValue(responses[0], "decision")
	if decision != decisionAcceptForSession {
		t.Fatalf("approval decision = %q, want %q", decision, decisionAcceptForSession)
	}
}

func TestPromptFallbackRespondsUnsupportedServerRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "custom/unknown", json.RawMessage("2"), map[string]any{"foo": "bar"})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	errResponses := session.errorResponsesSnapshot()
	if len(errResponses) != 1 {
		t.Fatalf("error responses = %d, want 1", len(errResponses))
	}
	if got := errResponses[0]["message"]; got != "unsupported server request" {
		t.Fatalf("fallback error message = %v, want %q", got, "unsupported server request")
	}
}

func TestPromptMapsExtendedNotifications(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "thread/started", map[string]any{
		"thread": map[string]any{
			"id": "thr-1",
		},
	})
	queueNotification(session, "turn/started", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "inProgress",
		},
	})
	queueNotification(session, "thread/status/changed", map[string]any{
		"threadId": "thr-1",
		"status": map[string]any{
			"type":        "active",
			"activeFlags": []any{"waitingOnApproval"},
		},
	})
	// Duplicate status should be deduplicated.
	queueNotification(session, "thread/status/changed", map[string]any{
		"threadId": "thr-1",
		"status": map[string]any{
			"type":        "active",
			"activeFlags": []any{"waitingOnApproval"},
		},
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "Run ",
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "tests",
	})
	queueNotification(session, "item/reasoning/summaryPartAdded", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"itemId":       "item-reason-1",
		"summaryIndex": 2,
	})
	queueNotification(session, "item/started", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type":    "commandExecution",
			"id":      "item-cmd-1",
			"status":  "inProgress",
			"command": "echo hi",
		},
	})
	queueNotification(session, "item/commandExecution/terminalInteraction", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"itemId":    "item-cmd-1",
		"processId": "p-1",
		"stdin":     "y\n",
	})
	queueNotification(session, "item/fileChange/patchUpdated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-file-1",
		"changes": []any{
			map[string]any{
				"path": "README.md",
				"kind": map[string]any{
					"type": "update",
				},
				"diff": "@@ -1 +1 @@\n-old\n+new",
			},
		},
	})
	queueNotification(session, "item/autoApprovalReview/started", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"targetItemId": "item-cmd-1",
		"review": map[string]any{
			"status":    "inProgress",
			"riskLevel": "highRiskCyberActivity",
			"rationale": "command touches network",
		},
	})
	queueNotification(session, "item/autoApprovalReview/completed", map[string]any{
		"threadId":     "thr-1",
		"turnId":       "turn-1",
		"targetItemId": "item-cmd-1",
		"review": map[string]any{
			"status":    "approved",
			"riskLevel": "highRiskCyberActivity",
		},
	})
	queueNotification(session, "hook/started", map[string]any{
		"threadId": "thr-1",
		"run": map[string]any{
			"id":        "hk-1",
			"eventName": "preCommand",
			"status":    "running",
		},
	})
	queueNotification(session, "hook/completed", map[string]any{
		"threadId": "thr-1",
		"run": map[string]any{
			"id":            "hk-1",
			"eventName":     "preCommand",
			"status":        "completed",
			"statusMessage": "ok",
		},
	})
	queueNotification(session, "mcpServer/startupStatus/updated", map[string]any{
		"name":   "docs",
		"status": statusFailed,
		"error":  "spawn failed",
	})
	queueNotification(session, "mcpServer/oauthLogin/completed", map[string]any{
		"name":    "docs",
		"success": false,
		"error":   "device code expired",
	})
	queueNotification(session, "model/rerouted", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"fromModel": "gpt-5.4",
		"toModel":   "gpt-5.4-mini",
		"reason":    "highRiskCyberActivity",
	})
	queueNotification(session, "model/verification", map[string]any{
		"threadId":      "thr-1",
		"turnId":        "turn-1",
		"verifications": []any{"trustedAccessForCyber"},
	})
	queueNotification(session, "configWarning", map[string]any{
		"summary": "Invalid config value",
		"path":    "/tmp/config.toml",
		"details": "using default",
	})
	queueNotification(session, "deprecationNotice", map[string]any{
		"summary": "old flag is deprecated",
		"details": "use --new-flag",
	})
	queueNotification(session, "account/login/completed", map[string]any{
		"accountId": "acc-1",
	})
	queueNotification(session, "account/updated", map[string]any{})
	queueNotification(session, "app/list/updated", map[string]any{})
	queueNotification(session, "skills/changed", map[string]any{})
	queueNotification(session, "externalAgentConfig/import/completed", map[string]any{})
	queueNotification(session, "guardianWarning", map[string]any{
		"threadId": "thr-1",
		"message":  "guardian warning",
	})
	queueNotification(session, "warning", map[string]any{
		"threadId": "thr-1",
		"message":  "warning",
	})
	queueNotification(session, "thread/compacted", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/archived", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/unarchived", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/closed", map[string]any{
		"threadId": "thr-1",
	})
	queueNotification(session, "thread/name/updated", map[string]any{
		"threadId":   "thr-1",
		"threadName": "ACP Mapping Thread",
	})
	queueNotification(session, "windows/worldWritableWarning", map[string]any{
		"extraCount": 2,
		"failedScan": false,
		"samplePaths": []any{
			"C:/tmp/a",
			"C:/tmp/b",
		},
	})
	queueNotification(session, "windowsSandbox/setupCompleted", map[string]any{
		"mode":    "sandbox",
		"success": true,
		"error":   nil,
	})
	queueNotification(session, "thread/realtime/started", map[string]any{
		"threadId":  "thr-1",
		"version":   "v1",
		"sessionId": "rt-1",
	})
	queueNotification(session, "thread/realtime/itemAdded", map[string]any{
		"threadId": "thr-1",
		"item": map[string]any{
			"type": "message",
			"id":   "rt-item-1",
		},
	})
	queueNotification(session, "thread/realtime/outputAudio/delta", map[string]any{
		"threadId": "thr-1",
		"audio": map[string]any{
			"itemId":      "rt-item-1",
			"sampleRate":  24000,
			"numChannels": 1,
			"data":        "AQID",
		},
	})
	queueNotification(session, "thread/realtime/transcriptUpdated", map[string]any{
		"threadId": "thr-1",
		"role":     "assistant",
		"text":     "hello realtime",
	})
	queueNotification(session, "thread/realtime/transcript/delta", map[string]any{
		"threadId": "thr-1",
		"role":     "assistant",
		"delta":    "hello",
	})
	queueNotification(session, "thread/realtime/transcript/done", map[string]any{
		"threadId": "thr-1",
		"role":     "assistant",
		"text":     "hello",
	})
	queueNotification(session, "thread/realtime/sdp", map[string]any{
		"threadId": "thr-1",
		"sdp":      "v=0",
	})
	queueNotification(session, "thread/realtime/error", map[string]any{
		"threadId": "thr-1",
		"message":  "transport issue",
	})
	queueNotification(session, "thread/realtime/closed", map[string]any{
		"threadId": "thr-1",
		"reason":   "done",
	})
	queueNotification(session, "fs/changed", map[string]any{
		"watchId": "watch-1",
		"changedPaths": []any{
			"/tmp/a.go",
			"/tmp/b.go",
		},
	})
	queueNotification(session, "fuzzyFileSearch/sessionUpdated", map[string]any{
		"sessionId": "fuzzy-1",
		"query":     "agent",
		"files": []any{
			map[string]any{"path": "internal/apps/codexacpbridge/agent.go", "score": 0.9},
		},
	})
	queueNotification(session, "fuzzyFileSearch/sessionCompleted", map[string]any{
		"sessionId": "fuzzy-1",
	})
	queueNotification(session, "command/exec/outputDelta", map[string]any{
		"processId":   "proc-1",
		"stream":      "stdout",
		"deltaBase64": "AQID",
		"capReached":  false,
	})
	queueNotification(session, "turn/diff/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"diff":     "@@ -1 +1 @@\n-foo\n+bar\n  tail",
	})
	queueNotification(session, "account/rateLimits/updated", map[string]any{
		"rateLimits": map[string]any{
			"planType": "plus",
			"primary": map[string]any{
				"usedPercent": 12,
			},
		},
	})
	queueNotification(session, "thread/tokenUsage/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"tokenUsage": map[string]any{
			"total": map[string]any{
				"inputTokens":           30,
				"outputTokens":          6,
				"totalTokens":           36,
				"cachedInputTokens":     8,
				"reasoningOutputTokens": 5,
			},
			"last": map[string]any{
				"inputTokens":           10,
				"outputTokens":          2,
				"totalTokens":           12,
				"cachedInputTokens":     4,
				"reasoningOutputTokens": 3,
			},
			"modelContextWindow": 258400,
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if !containsPlanEntry(updates, "Run tests") {
		t.Fatalf("missing aggregated plan delta update: %#v", updates)
	}
	planEntries := latestPlanEntries(updates)
	if len(planEntries) != 1 || planEntries[0].Content != "Run tests" || planEntries[0].Status != acp.PlanEntryStatusInProgress {
		t.Fatalf("latest plan entries = %#v, want one in-progress preview entry", planEntries)
	}
	if !containsToolCallText(updates, "y\n") {
		t.Fatalf("missing terminal interaction content: %#v", updates)
	}
	if !containsToolCallText(updates, "@@ -1 +1 @@\n-old\n+new") {
		t.Fatalf("missing patchUpdated tool call content: %#v", updates)
	}
	if !containsToolCall(updates, "codex-guardian-item-cmd-1") {
		t.Fatalf("missing guardian synthetic tool call updates: %#v", updates)
	}
	if !containsToolCall(updates, "codex-hook-hk-1") {
		t.Fatalf("missing hook synthetic tool call updates: %#v", updates)
	}
	if countThoughtText(updates, "Reasoning summary part added (#2).") != 0 {
		t.Fatalf("unexpected reasoning summary part thought update: %#v", updates)
	}
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected non-reasoning thought updates: %#v", updates)
	}
	meta := promptResp.Meta
	if _, ok := meta["usage"]; !ok {
		t.Fatalf("PromptResponse.Meta.usage missing: %#v", promptResp.Meta)
	}
	if promptResp.Usage == nil {
		t.Fatalf("PromptResponse.Usage missing")
	}
	if promptResp.Usage.InputTokens != 10 {
		t.Fatalf("PromptResponse.Usage.InputTokens = %d, want 10", promptResp.Usage.InputTokens)
	}
	if promptResp.Usage.OutputTokens != 2 {
		t.Fatalf("PromptResponse.Usage.OutputTokens = %d, want 2", promptResp.Usage.OutputTokens)
	}
	if promptResp.Usage.TotalTokens != 12 {
		t.Fatalf("PromptResponse.Usage.TotalTokens = %d, want 12", promptResp.Usage.TotalTokens)
	}
	if promptResp.Usage.CachedReadTokens == nil || *promptResp.Usage.CachedReadTokens != 4 {
		t.Fatalf("PromptResponse.Usage.CachedReadTokens = %#v, want 4", promptResp.Usage.CachedReadTokens)
	}
	if promptResp.Usage.ThoughtTokens == nil || *promptResp.Usage.ThoughtTokens != 3 {
		t.Fatalf("PromptResponse.Usage.ThoughtTokens = %#v, want 3", promptResp.Usage.ThoughtTokens)
	}
	if _, ok := meta["rateLimits"]; !ok {
		t.Fatalf("PromptResponse.Meta.rateLimits missing: %#v", promptResp.Meta)
	}
	usageUpdate := latestUsageUpdate(updates)
	if usageUpdate == nil {
		t.Fatalf("missing ACP session usage_update: %#v", updates)
	}
	if usageUpdate.Size != 258400 {
		t.Fatalf("usage_update.Size = %d, want 258400", usageUpdate.Size)
	}
	if usageUpdate.Used != 36 {
		t.Fatalf("usage_update.Used = %d, want 36", usageUpdate.Used)
	}
}

func TestPromptStopsOnErrorNotificationWithoutRetry(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "error", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"willRetry": false,
		"error": map[string]any{
			"message":           "fatal boom",
			"additionalDetails": "stacktrace",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}
	errMeta := mapValue(promptResp.Meta, "error")
	if got := stringValue(errMeta, "message"); got != "fatal boom" {
		t.Fatalf("PromptResponse.Meta.error.message = %q, want %q", got, "fatal boom")
	}
	if got := stringValue(errMeta, "additionalDetails"); got != "stacktrace" {
		t.Fatalf("PromptResponse.Meta.error.additionalDetails = %q, want %q", got, "stacktrace")
	}
	updates := conn.sessionUpdates(newResp.SessionId)
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected thought updates after error notification: %#v", updates)
	}
}

func TestPromptWarnLogsTerminalErrorNotificationWithoutRetry(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "error", map[string]any{
		"threadId":  "thr-1",
		"turnId":    "turn-1",
		"willRetry": false,
		"error": map[string]any{
			"message": "fatal boom",
			"code":    "quota_exceeded",
		},
	})

	conn := &fakeACPAppConnection{}
	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &logger)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	got := logs.String()
	for _, want := range []string{
		`"level":"warn"`,
		`"error_message":"fatal boom"`,
		`"error_code":"quota_exceeded"`,
		`"source":"error"`,
		`prompt completed with terminal error`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q: %s", want, got)
		}
	}
}

func TestPromptMetaIncludesTurnFailureErrorDetails(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "failed",
			"error": map[string]any{
				"message":           "model not supported",
				"codexErrorInfo":    "other",
				"additionalDetails": "chatgpt account mismatch",
			},
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}
	errMeta := mapValue(promptResp.Meta, "error")
	if got := stringValue(errMeta, "message"); got != "model not supported" {
		t.Fatalf("PromptResponse.Meta.error.message = %q, want %q", got, "model not supported")
	}
	if got := stringValue(errMeta, "codexErrorInfo"); got != "other" {
		t.Fatalf("PromptResponse.Meta.error.codexErrorInfo = %q, want %q", got, "other")
	}
}

func TestPromptWarnLogsTurnFailureErrorDetails(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "failed",
			"error": map[string]any{
				"message":        "model not supported",
				"codexErrorInfo": "other",
			},
		},
	})

	conn := &fakeACPAppConnection{}
	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &logger)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	got := logs.String()
	for _, want := range []string{
		`"level":"warn"`,
		`"error_message":"model not supported"`,
		`"error_code":"other"`,
		`"source":"turn/completed"`,
		`prompt completed with terminal error`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q: %s", want, got)
		}
	}
}

func TestPromptMetaIncludesRequestedMCPStartupStatusOnly(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "mcpServer/startupStatus/updated", map[string]any{
		"name":   "docs",
		"status": statusFailed,
		"error":  "spawn failed",
	})
	queueNotification(session, "mcpServer/startupStatus/updated", map[string]any{
		"name":   "global-only",
		"status": "completed",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{
		Cwd: "/tmp/work",
		McpServers: []acp.McpServer{
			{
				Stdio: &acp.McpServerStdio{
					Name:    "docs",
					Command: "docs-server",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	meta := promptResp.Meta
	codexMeta := mapValue(meta, "codex")
	mcpMeta := mapValue(codexMeta, "mcp")
	if got := stringValue(mcpMeta, "contract"); got != mcpContractMerge {
		t.Fatalf("PromptResponse.Meta.codex.mcp.contract = %q, want %q", got, mcpContractMerge)
	}
	requested := listValue(mcpMeta, "requested")
	if len(requested) != 1 {
		t.Fatalf("PromptResponse.Meta.codex.mcp.requested len = %d, want 1", len(requested))
	}
	requestedServer, ok := requested[0].(map[string]any)
	if !ok {
		t.Fatalf("PromptResponse.Meta.codex.mcp.requested[0] type = %T, want map[string]any", requested[0])
	}
	if got := stringValue(requestedServer, "name"); got != "docs" {
		t.Fatalf("PromptResponse.Meta.codex.mcp.requested[0].name = %q, want %q", got, "docs")
	}
	if got := stringValue(requestedServer, "transport"); got != testMCPTransportStdio {
		t.Fatalf("PromptResponse.Meta.codex.mcp.requested[0].transport = %q, want %q", got, testMCPTransportStdio)
	}
	startupStatus := mapValue(mcpMeta, "startupStatus")
	docsStatus := mapValue(startupStatus, "docs")
	if got := stringValue(docsStatus, "status"); got != statusFailed {
		t.Fatalf("PromptResponse.Meta.codex.mcp.startupStatus.docs.status = %q, want %q", got, statusFailed)
	}
	if got := stringValue(docsStatus, "error"); got != "spawn failed" {
		t.Fatalf("PromptResponse.Meta.codex.mcp.startupStatus.docs.error = %q, want %q", got, "spawn failed")
	}
	if _, ok := startupStatus["global-only"]; ok {
		t.Fatalf("PromptResponse.Meta.codex.mcp.startupStatus unexpectedly contains non-requested server: %#v", startupStatus)
	}
}

func TestPromptForwardsSessionScopedUpdatesAfterCompletion(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	queueNotification(session, "account/updated", map[string]any{})
	time.Sleep(50 * time.Millisecond)
	updates := conn.sessionUpdates(newResp.SessionId)
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected thought updates after completion for session-scoped event: %#v", updates)
	}
}

func TestPromptRecreatesBackendAfterSessionBackendStopsBeforeFirstPrompt(t *testing.T) {
	firstSession := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	secondSession := newFakeAppServerSession("codex_test/1.0.0", "thr-2", "turn-2")
	queueNotification(secondSession, "turn/completed", map[string]any{
		"threadId": "thr-2",
		"turnId":   "turn-2",
		"turn": map[string]any{
			"id":     "turn-2",
			"status": "completed",
		},
	})

	l := zerolog.Nop()
	var (
		factoryMu sync.Mutex
		calls     int
	)
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		factoryMu.Lock()
		defer factoryMu.Unlock()
		calls++
		if calls == 1 {
			return firstSession, nil
		}
		return secondSession, nil
	}, "agent", codexAppConfig{}, &l)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	close(firstSession.events)
	waitForCondition(t, 500*time.Millisecond, func() bool {
		agent.mu.Lock()
		defer agent.mu.Unlock()
		state := agent.sessions[newResp.SessionId]
		return state != nil && state.backend == nil && state.threadID == ""
	}, "session backend was not cleared after backend event stream closed")

	promptResp, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	})
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("Prompt().StopReason = %q, want %q", promptResp.StopReason, acp.StopReasonEndTurn)
	}
	if got := len(firstSession.turnStartParamsSnapshot()); got != 0 {
		t.Fatalf("first session turn/start calls = %d, want 0", got)
	}
	if got := len(secondSession.threadStartParamsSnapshot()); got != 1 {
		t.Fatalf("replacement session thread/start calls = %d, want 1", got)
	}
	if got := len(secondSession.turnStartParamsSnapshot()); got != 1 {
		t.Fatalf("replacement session turn/start calls = %d, want 1", got)
	}
}

func TestShuttingDownRejectsNewACPRequests(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	agent.beginShutdown()

	if _, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/other"}); err == nil {
		t.Fatal("NewSession() error = nil, want shutdown error")
	} else {
		assertShutdownError(t, err, errBridgeShuttingDown)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err == nil {
		t.Fatal("Prompt() error = nil, want shutdown error")
	} else {
		assertShutdownError(t, err, errBridgeShuttingDown)
	}
	if _, err := agent.SetSessionMode(context.Background(), acp.SetSessionModeRequest{
		SessionId: newResp.SessionId,
		ModeId:    acp.SessionModeId("chat"),
	}); err == nil {
		t.Fatal("SetSessionMode() error = nil, want shutdown error")
	} else {
		assertShutdownError(t, err, errBridgeShuttingDown)
	}
	if _, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId("model"),
			Value:     acp.SessionConfigValueId("gpt-5.4"),
		},
	}); err == nil {
		t.Fatal("SetSessionConfigOption() error = nil, want shutdown error")
	} else {
		assertShutdownError(t, err, errBridgeShuttingDown)
	}
	if _, err := agent.SetSessionConfigOption(context.Background(), acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: newResp.SessionId,
			ConfigId:  acp.SessionConfigId(sessionConfigIDReasoningEffort),
			Value:     acp.SessionConfigValueId(testReasoningXHigh),
		},
	}); err == nil {
		t.Fatal("SetSessionConfigOption() error = nil, want shutdown error")
	} else {
		assertShutdownError(t, err, errBridgeShuttingDown)
	}
}

func TestPromptRebindsTurnIDFromTurnStartedNotification(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	type promptResult struct {
		resp acp.PromptResponse
		err  error
	}
	resultCh := make(chan promptResult, 1)
	go func() {
		resp, promptErr := agent.Prompt(context.Background(), acp.PromptRequest{
			SessionId: newResp.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
		})
		resultCh <- promptResult{resp: resp, err: promptErr}
	}()

	waitForCondition(t, time.Second, func() bool {
		return len(session.turnStartParamsSnapshot()) == 1
	}, "turn/start not observed before queuing turn events")
	queueNotification(session, "turn/started", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-2",
			"status": "inProgress",
		},
	})
	queueNotification(session, "item/agentMessage/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-2",
		"itemId":   "item-msg-1",
		"delta":    "rebound",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-2",
		"item": map[string]any{
			"type":  "agentMessage",
			"id":    "item-msg-1",
			"text":  "rebound",
			"phase": "final_answer",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-2",
		"turn": map[string]any{
			"id":     "turn-2",
			"status": "completed",
		},
	})

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("Prompt() error = %v", res.err)
		}
		if res.resp.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("Prompt().StopReason = %q, want %q", res.resp.StopReason, acp.StopReasonEndTurn)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt completion after turn-id rebind")
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if !containsAgentMessageText(updates, "rebound") {
		t.Fatalf("missing rebound delta after turn-id rebind: %#v", updates)
	}
}

func TestPromptRebindsThreadIDFromThreadStartedNotification(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	type promptResult struct {
		resp acp.PromptResponse
		err  error
	}
	resultCh := make(chan promptResult, 1)
	go func() {
		resp, promptErr := agent.Prompt(context.Background(), acp.PromptRequest{
			SessionId: newResp.SessionId,
			Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
		})
		resultCh <- promptResult{resp: resp, err: promptErr}
	}()

	waitForCondition(t, time.Second, func() bool {
		return len(session.turnStartParamsSnapshot()) == 1
	}, "turn/start not observed before queuing thread-rebind events")
	queueNotification(session, "thread/started", map[string]any{
		"thread": map[string]any{
			"id": "thr-2",
		},
	})
	queueNotification(session, "thread/status/changed", map[string]any{
		"threadId": "thr-2",
		"status": map[string]any{
			"type": "active",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-2",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("Prompt() error = %v", res.err)
		}
		if res.resp.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("Prompt().StopReason = %q, want %q", res.resp.StopReason, acp.StopReasonEndTurn)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for prompt completion after thread-id rebind")
	}

	updates := conn.sessionUpdates(newResp.SessionId)
	if countThoughtChunks(updates) != 0 {
		t.Fatalf("unexpected thought updates after thread-id rebind: %#v", updates)
	}
}

func TestPromptPlanDeltaEmitsFullReplacementEntries(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "Run ",
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "tests",
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-2",
		"delta":    "Lint",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	entries := latestPlanEntries(conn.sessionUpdates(newResp.SessionId))
	if len(entries) != 2 {
		t.Fatalf("latest plan entries len = %d, want 2: %#v", len(entries), entries)
	}
	if entries[0].Content != "Run tests" || entries[1].Content != "Lint" {
		t.Fatalf("latest plan entries = %#v, want ordered full replacement", entries)
	}
	for _, entry := range entries {
		if entry.Status != acp.PlanEntryStatusInProgress {
			t.Fatalf("preview plan entry status = %q, want in_progress", entry.Status)
		}
	}
}

func TestPromptCompletedPlanItemOverridesDraftText(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "Draft",
	})
	queueNotification(session, "item/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"item": map[string]any{
			"type": "plan",
			"id":   "item-plan-1",
			"text": "Authoritative plan",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	entries := latestPlanEntries(conn.sessionUpdates(newResp.SessionId))
	if len(entries) != 1 || entries[0].Content != "Authoritative plan" {
		t.Fatalf("latest plan entries = %#v, want authoritative completed text", entries)
	}
}

func TestPromptPlanSnapshotClearsPreviewState(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-1",
		"delta":    "Draft one",
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-2",
		"delta":    "Draft two",
	})
	queueNotification(session, "turn/plan/updated", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"plan": []any{
			map[string]any{"step": "Snapshot step", "status": "completed"},
		},
	})
	queueNotification(session, "item/plan/delta", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-plan-3",
		"delta":    "Later preview",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	entries := latestPlanEntries(conn.sessionUpdates(newResp.SessionId))
	if len(entries) != 1 || entries[0].Content != "Later preview" {
		t.Fatalf("latest plan entries = %#v, want preview rebuilt after snapshot reset", entries)
	}
}

func TestPromptBridgesToolCallRequestAsPartialForward(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/tool/call", json.RawMessage("3"), map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"callId":   "call-1",
		"tool":     "relay.agents.start",
		"arguments": map[string]any{
			"agent_name": "planner",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got, ok := responses[0]["success"].(bool); !ok || got {
		t.Fatalf("response.success = %v, want false", responses[0]["success"])
	}
	contentItems, ok := responses[0]["contentItems"].([]any)
	if !ok || len(contentItems) == 0 {
		t.Fatalf("response.contentItems missing: %#v", responses[0])
	}
	first, ok := contentItems[0].(map[string]any)
	if !ok {
		t.Fatalf("response.contentItems[0] type = %T, want map[string]any", contentItems[0])
	}
	if got := stringValue(first, "type"); got != "inputText" {
		t.Fatalf("response.contentItems[0].type = %q, want %q", got, "inputText")
	}
	if got := stringValue(first, "text"); !strings.Contains(got, "not executed by ACP bridge") {
		t.Fatalf("response.contentItems[0].text = %q, want contains %q", got, "not executed by ACP bridge")
	}
}

func TestPromptBridgesToolRequestUserInputRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "item/tool/requestUserInput", json.RawMessage("4"), map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"itemId":   "item-ui-1",
		"questions": []any{
			map[string]any{
				"header":   "Scope",
				"id":       "scope",
				"question": "Which scope?",
				"options": []any{
					map[string]any{"label": "Tier1", "description": "must-have only"},
					map[string]any{"label": "Tier1+2", "description": "must + should"},
				},
			},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	answers, ok := responses[0]["answers"].(map[string]any)
	if !ok {
		t.Fatalf("response.answers type = %T, want map[string]any", responses[0]["answers"])
	}
	scopeAnswer, ok := answers["scope"].(map[string]any)
	if !ok {
		t.Fatalf("response.answers.scope type = %T, want map[string]any", answers["scope"])
	}
	answerList, ok := scopeAnswer["answers"].([]string)
	if !ok {
		rawList, okAny := scopeAnswer["answers"].([]any)
		if !okAny {
			t.Fatalf("response.answers.scope.answers type = %T, want []string/[]any", scopeAnswer["answers"])
		}
		answerList = make([]string, 0, len(rawList))
		for _, raw := range rawList {
			s, _ := raw.(string)
			answerList = append(answerList, s)
		}
	}
	if len(answerList) != 1 || answerList[0] != "Tier1" {
		t.Fatalf("response.answers.scope.answers = %#v, want [Tier1]", answerList)
	}
}

func TestPromptBridgesMcpElicitationRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "mcpServer/elicitation/request", json.RawMessage("5"), map[string]any{
		"serverName": "docs",
		"threadId":   "thr-1",
		"mode":       "form",
		"message":    "Need user input",
		"requestedSchema": map[string]any{
			"type": "object",
		},
		"_meta": map[string]any{
			"trace": "abc",
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
		elicitationResponse: acp.UnstableCreateElicitationResponse{
			Accept: &acp.UnstableCreateElicitationAccept{Action: decisionAccept, Content: map[string]any{}},
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)
	_, _ = agent.Initialize(context.Background(), acp.InitializeRequest{ClientCapabilities: acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}}}})

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "action"); got != "accept" {
		t.Fatalf("response.action = %q, want %q", got, "accept")
	}
	if _, ok := responses[0]["content"]; !ok {
		t.Fatalf("response.content missing for accept action: %#v", responses[0])
	}
	if _, ok := responses[0]["_meta"]; !ok {
		t.Fatalf("response._meta missing passthrough: %#v", responses[0])
	}
}

func TestMCPToolApprovalPolicies(t *testing.T) {
	tests := []struct {
		name             string
		policy           MCPApprovalPolicy
		permissionResult acp.RequestPermissionResponse
		wantAction       string
		wantPersist      string
		wantRequests     int
	}{
		{
			name:             "ask allows for session when advertised",
			policy:           MCPApprovalPolicyAsk,
			permissionResult: acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(mcpApprovalOptionAllowSession)},
			wantAction:       decisionAccept,
			wantPersist:      "session",
			wantRequests:     1,
		},
		{
			name:         "allow automatically accepts",
			policy:       MCPApprovalPolicyAllow,
			wantAction:   decisionAccept,
			wantRequests: 0,
		},
		{
			name:         "deny automatically declines",
			policy:       MCPApprovalPolicyDeny,
			wantAction:   decisionDecline,
			wantRequests: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
			conn := &fakeACPAppConnection{permissionResponse: test.permissionResult}
			logger := zerolog.Nop()
			agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
			agent.mcpApprovalPolicy = test.policy
			agent.setConnection(conn)
			params := map[string]any{
				"serverName": "knowl",
				"threadId":   "thr-1",
				"mode":       "form",
				"message":    "Allow knowl to run tool?",
				"_meta": map[string]any{
					"codex_approval_kind": "mcp_tool_call",
					"persist":             []any{"session", "always"},
				},
			}
			if err := agent.handleMCPToolApproval(context.Background(), "session-1", session, &appServerRequest{}, params); err != nil {
				t.Fatalf("handleMCPToolApproval() error = %v", err)
			}

			responses := session.responsesSnapshot()
			if got, want := len(responses), 1; got != want {
				t.Fatalf("responses = %d, want %d", got, want)
			}
			if got := stringValue(responses[0], "action"); got != test.wantAction {
				t.Fatalf("response.action = %q, want %q", got, test.wantAction)
			}
			meta := mapValue(responses[0], "_meta")
			if got := stringValue(meta, "persist"); got != test.wantPersist {
				t.Fatalf("response._meta.persist = %q, want %q", got, test.wantPersist)
			}
			if got := len(conn.permissionRequests); got != test.wantRequests {
				t.Fatalf("permission requests = %d, want %d", got, test.wantRequests)
			}
			if test.wantRequests == 1 {
				gotOptions := conn.permissionRequests[0].Options
				if got, want := len(gotOptions), 4; got != want {
					t.Fatalf("permission options = %d, want %d", got, want)
				}
				if got, want := gotOptions[0].OptionId, mcpApprovalOptionAllowOnce; got != want {
					t.Fatalf("first option = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestMCPToolApprovalFailsClosed(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	conn := &fakeACPAppConnection{permissionResponse: acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("unexpected")}}
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.setConnection(conn)
	params := map[string]any{
		"mode": "form",
		"_meta": map[string]any{
			"codex_approval_kind": "mcp_tool_call",
		},
	}
	if err := agent.handleMCPToolApproval(context.Background(), "session-1", session, &appServerRequest{}, params); err != nil {
		t.Fatalf("handleMCPToolApproval() error = %v", err)
	}
	responses := session.responsesSnapshot()
	if got := stringValue(responses[0], "action"); got != decisionCancel {
		t.Fatalf("response.action = %q, want %q", got, decisionCancel)
	}
}

func TestIsMCPToolApproval(t *testing.T) {
	if !isMCPToolApproval(map[string]any{"mode": "form", "_meta": map[string]any{"codex_approval_kind": "mcp_tool_call"}}) {
		t.Fatal("isMCPToolApproval() = false, want true")
	}
	if isMCPToolApproval(map[string]any{"mode": "url", "_meta": map[string]any{"codex_approval_kind": "mcp_tool_call"}}) {
		t.Fatal("isMCPToolApproval(url) = true, want false")
	}
	if !isMCPToolApproval(map[string]any{"mode": "openai/form", "_meta": map[string]any{"codex_approval_kind": "mcp_tool_call"}}) {
		t.Fatal("isMCPToolApproval(openai/form) = false, want true")
	}
}

func TestForwardMCPElicitationRequiresCapabilityAndForwardsAcceptedForm(t *testing.T) {
	logger := zerolog.Nop()
	conn := &fakeACPAppConnection{elicitationResponse: acp.UnstableCreateElicitationResponse{
		Accept: &acp.UnstableCreateElicitationAccept{Action: decisionAccept, Content: map[string]any{"project": "balda"}},
	}}
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.setConnection(conn)
	params := map[string]any{"mode": "form", "message": "Project?", "requestedSchema": map[string]any{"type": "object"}}
	if got := stringValue(agent.forwardMCPElicitation(context.Background(), nil, params), "action"); got != decisionCancel {
		t.Fatalf("without capability action = %q, want %q", got, decisionCancel)
	}
	_, _ = agent.Initialize(context.Background(), acp.InitializeRequest{ClientCapabilities: acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}}}})
	response := agent.forwardMCPElicitation(context.Background(), nil, params)
	if got := stringValue(response, "action"); got != decisionAccept {
		t.Fatalf("with capability action = %q, want %q", got, decisionAccept)
	}
	if got, want := len(conn.elicitationRequests), 1; got != want {
		t.Fatalf("elicitation requests = %d, want %d", got, want)
	}
}

func TestForwardMCPElicitationForwardsURLAndCancelMetadata(t *testing.T) {
	logger := zerolog.Nop()
	conn := &fakeACPAppConnection{elicitationResponse: acp.UnstableCreateElicitationResponse{
		Cancel: &acp.UnstableCreateElicitationCancel{Action: decisionCancel, Meta: map[string]any{"reason": "closed"}},
	}}
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.setConnection(conn)
	_, _ = agent.Initialize(context.Background(), acp.InitializeRequest{ClientCapabilities: acp.ClientCapabilities{Elicitation: &acp.ElicitationCapabilities{Url: &acp.ElicitationUrlCapabilities{}}}})
	response := agent.forwardMCPElicitation(context.Background(), &appServerRequest{ID: json.RawMessage("17")}, map[string]any{"mode": "url", "message": "Log in", "url": "https://example.test/login"})
	if got := stringValue(response, "action"); got != decisionCancel {
		t.Fatalf("response.action = %q, want %q", got, decisionCancel)
	}
	if got := stringValue(mapValue(response, "_meta"), "reason"); got != "closed" {
		t.Fatalf("response._meta.reason = %q, want %q", got, "closed")
	}
	if got, want := conn.elicitationRequests[0].Url.ElicitationId, acp.UnstableElicitationId("17"); got != want {
		t.Fatalf("URL elicitation ID = %q, want %q", got, want)
	}
}

func TestOpenAIFormElicitationCancelsWithoutPermissionRequest(t *testing.T) {
	logger := zerolog.Nop()
	conn := &fakeACPAppConnection{}
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.mcpApprovalPolicy = MCPApprovalPolicyAllow
	agent.setConnection(conn)
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	agent.sessions["session-1"] = &codexProxySessionState{backend: session}
	params, err := json.Marshal(map[string]any{"mode": "openai/form", "_meta": map[string]any{"codex_approval_kind": "mcp_tool_call"}})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if err := agent.handleServerRequest(context.Background(), "session-1", &appServerRequest{Method: "mcpServer/elicitation/request", Params: params}); err != nil {
		t.Fatalf("handleServerRequest() error = %v", err)
	}
	responses := session.responsesSnapshot()
	if got := stringValue(responses[0], "action"); got != decisionCancel {
		t.Fatalf("response.action = %q, want %q", got, decisionCancel)
	}
	if got := len(conn.permissionRequests); got != 0 {
		t.Fatalf("permission requests = %d, want 0", got)
	}
}

func TestAutomaticMCPApprovalLogsToolWithoutArguments(t *testing.T) {
	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.mcpApprovalPolicy = MCPApprovalPolicyAllow
	agent.sessions["session-1"] = &codexProxySessionState{pendingMCPToolCalls: map[string]pendingMCPToolCall{
		"knowl": {itemID: "call-42", tool: "knowl_ingest"},
	}}
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	params := map[string]any{
		"serverName": "knowl",
		"threadId":   "thr-1",
		"mode":       "form",
		"arguments":  map[string]any{"token": "never-log-this"},
		"_meta":      map[string]any{"codex_approval_kind": "mcp_tool_call"},
	}
	if err := agent.handleMCPToolApproval(context.Background(), "session-1", session, &appServerRequest{}, params); err != nil {
		t.Fatalf("handleMCPToolApproval() error = %v", err)
	}
	got := logs.String()
	if !strings.Contains(got, `"tool":"knowl_ingest"`) {
		t.Fatalf("log does not contain tool: %s", got)
	}
	if strings.Contains(got, "never-log-this") || strings.Contains(got, `"arguments"`) {
		t.Fatalf("log contains MCP arguments: %s", got)
	}
}

func TestMCPApprovalUsesPendingToolCallCorrelation(t *testing.T) {
	logger := zerolog.Nop()
	agent := newCodexACPProxyAgent(nil, "agent", codexAppConfig{}, &logger)
	agent.sessions["session-1"] = &codexProxySessionState{pendingMCPToolCalls: map[string]pendingMCPToolCall{"knowl": {itemID: "call-42", tool: "knowl_ingest"}}}
	got := agent.mcpApprovalToolCallID("session-1", &appServerRequest{ID: json.RawMessage("7")}, map[string]any{"serverName": "knowl"})
	if want := toolCallID("call-42"); got != want {
		t.Fatalf("mcpApprovalToolCallID() = %q, want %q", got, want)
	}
	if got := agent.mcpApprovalToolCallID("session-1", &appServerRequest{ID: json.RawMessage("7")}, map[string]any{"serverName": "knowl"}); got == toolCallID("call-42") {
		t.Fatalf("consumed correlation was reused: %q", got)
	}
}

func TestPromptBridgesApplyPatchApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "applyPatchApproval", json.RawMessage("6"), map[string]any{
		"callId":         "call-apply-1",
		"conversationId": "thr-1",
		"fileChanges": map[string]any{
			"README.md": map[string]any{"kind": "modify"},
		},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-2"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "decision"); got != "approved_for_session" {
		t.Fatalf("response.decision = %q, want %q", got, "approved_for_session")
	}
}

func TestPromptBridgesExecCommandApprovalRequest(t *testing.T) {
	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "execCommandApproval", json.RawMessage("7"), map[string]any{
		"callId":         "call-exec-1",
		"conversationId": "thr-1",
		"command":        []any{"curl", "example.com"},
		"cwd":            "/tmp/work",
		"parsedCmd":      []any{},
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-3"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "decision"); got != "denied" {
		t.Fatalf("response.decision = %q, want %q", got, "denied")
	}
}

func TestPromptBridgesChatgptAuthTokensRefreshFromEnv(t *testing.T) {
	t.Setenv("CODEX_CHATGPT_ACCESS_TOKEN", "token-1")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "acct-1")
	t.Setenv("CODEX_CHATGPT_PLAN_TYPE", "plus")

	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "account/chatgptAuthTokens/refresh", json.RawMessage("8"), map[string]any{
		"reason": "expired",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	responses := session.responsesSnapshot()
	if len(responses) != 1 {
		t.Fatalf("responses = %d, want 1", len(responses))
	}
	if got := stringValue(responses[0], "accessToken"); got != "token-1" {
		t.Fatalf("response.accessToken = %q, want %q", got, "token-1")
	}
	if got := stringValue(responses[0], "chatgptAccountId"); got != "acct-1" {
		t.Fatalf("response.chatgptAccountId = %q, want %q", got, "acct-1")
	}
	if got := stringValue(responses[0], "chatgptPlanType"); got != "plus" {
		t.Fatalf("response.chatgptPlanType = %q, want %q", got, "plus")
	}
}

func TestPromptBridgesChatgptAuthTokensRefreshUnavailable(t *testing.T) {
	t.Setenv("CODEX_CHATGPT_ACCESS_TOKEN", "")
	t.Setenv("CODEX_CHATGPT_ACCOUNT_ID", "")

	session := newFakeAppServerSession("codex_test/1.0.0", "thr-1", "turn-1")
	queueRequest(session, "account/chatgptAuthTokens/refresh", json.RawMessage("9"), map[string]any{
		"reason": "expired",
	})
	queueNotification(session, "turn/completed", map[string]any{
		"threadId": "thr-1",
		"turnId":   "turn-1",
		"turn": map[string]any{
			"id":     "turn-1",
			"status": "completed",
		},
	})

	conn := &fakeACPAppConnection{
		permissionResponse: acp.RequestPermissionResponse{
			Outcome: acp.NewRequestPermissionOutcomeSelected("opt-1"),
		},
	}
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return session, nil
	}, "agent", codexAppConfig{}, &l)
	agent.setConnection(conn)

	newResp, err := agent.NewSession(context.Background(), acp.NewSessionRequest{Cwd: "/tmp/work"})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if _, err := agent.Prompt(context.Background(), acp.PromptRequest{
		SessionId: newResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello")},
	}); err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}

	errResponses := session.errorResponsesSnapshot()
	if len(errResponses) != 1 {
		t.Fatalf("error responses = %d, want 1", len(errResponses))
	}
	if got := stringValue(errResponses[0], "message"); !strings.Contains(got, "chatgpt token refresh unavailable") {
		t.Fatalf("error message = %v, want contains %q", got, "chatgpt token refresh unavailable")
	}
}

func TestServerRequestResolvedClearsPendingRequest(t *testing.T) {
	sessionID := acp.SessionId("s1")
	l := zerolog.Nop()
	agent := newCodexACPProxyAgent(func(context.Context, string) (appServerSession, error) {
		return nil, errors.New("not used")
	}, "agent", codexAppConfig{}, &l)
	agent.sessions[sessionID] = &codexProxySessionState{
		threadID:            "thr-1",
		turnID:              "turn-1",
		pendingRequests:     map[string]string{"1": "item/tool/call"},
		pendingMCPToolCalls: map[string]pendingMCPToolCall{"knowl": {itemID: "call-42", tool: "knowl_ingest"}},
		planItems:           map[string]planItemState{},
		latestRateLimits:    map[string]any{},
	}

	raw, err := json.Marshal(map[string]any{
		"threadId":  "thr-1",
		"requestId": 1,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	done, stopReason, usage, errorMeta, err := agent.handleNotification(context.Background(), sessionID, "thr-1", "turn-1", true, &appServerNotification{
		Method: "serverRequest/resolved",
		Params: raw,
	})
	if err != nil {
		t.Fatalf("handleNotification() error = %v", err)
	}
	if done {
		t.Fatalf("handleNotification() done = %v, want %v", done, false)
	}
	if stopReason != "" {
		t.Fatalf("handleNotification() stopReason = %q, want empty", stopReason)
	}
	if usage != nil {
		t.Fatalf("handleNotification() usage = %#v, want nil", usage)
	}
	if errorMeta != nil {
		t.Fatalf("handleNotification() errorMeta = %#v, want nil", errorMeta)
	}
	if got := len(agent.sessions[sessionID].pendingRequests); got != 0 {
		t.Fatalf("pending requests = %d, want 0", got)
	}
	if got := len(agent.sessions[sessionID].pendingMCPToolCalls); got != 0 {
		t.Fatalf("pending MCP tool calls = %d, want 0", got)
	}
}

type fakeACPAppConnection struct {
	mu                  sync.Mutex
	permissionResponse  acp.RequestPermissionResponse
	permissionError     error
	permissionRequests  []acp.RequestPermissionRequest
	updates             []acp.SessionNotification
	elicitationResponse acp.UnstableCreateElicitationResponse
	elicitationError    error
	elicitationRequests []acp.UnstableCreateElicitationRequest
}

func (f *fakeACPAppConnection) SessionUpdate(_ context.Context, params acp.SessionNotification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, params)
	return nil
}

func (f *fakeACPAppConnection) RequestPermission(_ context.Context, params acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionRequests = append(f.permissionRequests, params)
	if f.permissionError != nil {
		return acp.RequestPermissionResponse{}, f.permissionError
	}
	if f.permissionResponse.Outcome.Cancelled == nil && f.permissionResponse.Outcome.Selected == nil {
		if len(params.Options) > 0 {
			return acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(params.Options[0].OptionId),
			}, nil
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return f.permissionResponse, nil
}

func (f *fakeACPAppConnection) UnstableCreateElicitation(_ context.Context, params acp.UnstableCreateElicitationRequest) (acp.UnstableCreateElicitationResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.elicitationRequests = append(f.elicitationRequests, params)
	if f.elicitationError != nil {
		return acp.UnstableCreateElicitationResponse{}, f.elicitationError
	}
	return f.elicitationResponse, nil
}

func (f *fakeACPAppConnection) sessionUpdates(sessionID acp.SessionId) []acp.SessionNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	filtered := make([]acp.SessionNotification, 0, len(f.updates))
	for _, update := range f.updates {
		if update.SessionId == sessionID {
			filtered = append(filtered, update)
		}
	}
	return filtered
}

type fakeAppServerSession struct {
	mu sync.Mutex

	initializeResp appServerInitializeResponse
	events         chan appServerEvent
	pendingEvents  []appServerEvent
	turnStarted    bool

	threadStartResp      appServerThreadStartResponse
	threadResumeResp     appServerThreadResumeResponse
	turnStartResp        appServerTurnStartResponse
	threadListErr        error
	threadUnsubscribeErr error
	modelListErr         error

	threadListParams        []map[string]any
	threadStartParams       []map[string]any
	threadResumeParams      []map[string]any
	threadUnsubscribeIDs    []string
	settingsUpdateParams    []map[string]any
	turnStartParams         []map[string]any
	modelListParams         []map[string]any
	threadListCalls         int
	threadListResponses     []appServerThreadListResponse
	threadUnsubscribeResp   appServerThreadUnsubscribeResponse
	modelListCalls          int
	modelListResponses      []appServerModelListResponse
	responses               []map[string]any
	errorResponses          []map[string]any
	closeCalls              int
	waitCalls               int
	turnInterruptCalls      int
	lastInterruptedThreadID string
	lastInterruptedTurnID   string
}

func newFakeAppServerSession(userAgent string, threadID string, turnID string) *fakeAppServerSession {
	threadResp := appServerThreadStartResponse{}
	threadResp.Thread.ID = threadID
	threadResp.Thread.SessionID = threadID
	threadResumeResp := appServerThreadResumeResponse{}
	threadResumeResp.Thread.ID = threadID
	threadResumeResp.Thread.SessionID = threadID
	turnResp := appServerTurnStartResponse{}
	turnResp.Turn.ID = turnID
	return &fakeAppServerSession{
		initializeResp:   appServerInitializeResponse{UserAgent: userAgent},
		events:           make(chan appServerEvent, 64),
		threadStartResp:  threadResp,
		threadResumeResp: threadResumeResp,
		turnStartResp:    turnResp,
	}
}

func appServerModelWithReasoning(
	id string,
	isDefault bool,
	defaultReasoningEffort string,
	supportedReasoningEfforts ...string,
) appServerModel {
	model := appServerModel{
		ID:                     id,
		DisplayName:            id,
		IsDefault:              isDefault,
		DefaultReasoningEffort: defaultReasoningEffort,
	}
	for _, effort := range supportedReasoningEfforts {
		model.SupportedReasoningEfforts = append(model.SupportedReasoningEfforts, appServerReasoningEffortOption{
			Description:     effort + " effort",
			ReasoningEffort: effort,
		})
	}
	return model
}

func (f *fakeAppServerSession) InitializeResponse() appServerInitializeResponse {
	return f.initializeResp
}

func (f *fakeAppServerSession) Events() <-chan appServerEvent {
	return f.events
}

func (f *fakeAppServerSession) ThreadList(_ context.Context, params map[string]any) (appServerThreadListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadListParams = append(f.threadListParams, cloneAnyMap(params))
	if f.threadListErr != nil {
		return appServerThreadListResponse{}, f.threadListErr
	}
	if f.threadListCalls >= len(f.threadListResponses) {
		f.threadListCalls++
		return appServerThreadListResponse{}, nil
	}
	resp := f.threadListResponses[f.threadListCalls]
	f.threadListCalls++
	return resp, nil
}

func (f *fakeAppServerSession) ThreadStart(_ context.Context, params map[string]any) (appServerThreadStartResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadStartParams = append(f.threadStartParams, cloneAnyMap(params))
	return f.threadStartResp, nil
}

func (f *fakeAppServerSession) ThreadUnsubscribe(_ context.Context, threadID string) (appServerThreadUnsubscribeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadUnsubscribeIDs = append(f.threadUnsubscribeIDs, strings.TrimSpace(threadID))
	if f.threadUnsubscribeErr != nil {
		return appServerThreadUnsubscribeResponse{}, f.threadUnsubscribeErr
	}
	return f.threadUnsubscribeResp, nil
}

func (f *fakeAppServerSession) ThreadResume(_ context.Context, params map[string]any) (appServerThreadResumeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.threadResumeParams = append(f.threadResumeParams, cloneAnyMap(params))
	return f.threadResumeResp, nil
}

func (f *fakeAppServerSession) ThreadSettingsUpdate(_ context.Context, params map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settingsUpdateParams = append(f.settingsUpdateParams, cloneAnyMap(params))
	return nil
}

func (f *fakeAppServerSession) TurnStart(_ context.Context, params map[string]any) (appServerTurnStartResponse, error) {
	f.mu.Lock()
	f.turnStartParams = append(f.turnStartParams, cloneAnyMap(params))
	f.turnStarted = true
	pending := append([]appServerEvent(nil), f.pendingEvents...)
	f.pendingEvents = nil
	resp := f.turnStartResp
	f.mu.Unlock()

	for _, event := range pending {
		f.events <- event
	}
	return resp, nil
}

func (f *fakeAppServerSession) ModelList(_ context.Context, params map[string]any) (appServerModelListResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelListParams = append(f.modelListParams, cloneAnyMap(params))
	if f.modelListErr != nil {
		return appServerModelListResponse{}, f.modelListErr
	}
	if f.modelListCalls >= len(f.modelListResponses) {
		f.modelListCalls++
		return appServerModelListResponse{}, nil
	}
	resp := f.modelListResponses[f.modelListCalls]
	f.modelListCalls++
	return resp, nil
}

func (f *fakeAppServerSession) TurnInterrupt(_ context.Context, threadID string, turnID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.turnInterruptCalls++
	f.lastInterruptedThreadID = strings.TrimSpace(threadID)
	f.lastInterruptedTurnID = strings.TrimSpace(turnID)
	return nil
}

func (f *fakeAppServerSession) RespondRequest(_ context.Context, _ *appServerRequest, result any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := result.(map[string]any)
	if !ok {
		return errors.New("result must be map")
	}
	f.responses = append(f.responses, m)
	return nil
}

func (f *fakeAppServerSession) RespondRequestError(_ context.Context, _ *appServerRequest, code int, message string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errorResponses = append(f.errorResponses, map[string]any{
		"code":    code,
		"message": message,
		"data":    data,
	})
	return nil
}

func (f *fakeAppServerSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return nil
}

func (f *fakeAppServerSession) Wait() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls++
	return nil
}

func (f *fakeAppServerSession) responsesSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.responses))
	copy(out, f.responses)
	return out
}

func (f *fakeAppServerSession) threadStartParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.threadStartParams))
	copy(out, f.threadStartParams)
	return out
}

func (f *fakeAppServerSession) turnStartParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.turnStartParams))
	copy(out, f.turnStartParams)
	return out
}

func (f *fakeAppServerSession) errorResponsesSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.errorResponses))
	copy(out, f.errorResponses)
	return out
}

func (f *fakeAppServerSession) modelListParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.modelListParams))
	copy(out, f.modelListParams)
	return out
}

func (f *fakeAppServerSession) threadListParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.threadListParams))
	copy(out, f.threadListParams)
	return out
}

func (f *fakeAppServerSession) threadResumeParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.threadResumeParams))
	copy(out, f.threadResumeParams)
	return out
}

func (f *fakeAppServerSession) settingsUpdateParamsSnapshot() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.settingsUpdateParams))
	copy(out, f.settingsUpdateParams)
	return out
}

func (f *fakeAppServerSession) threadUnsubscribeIDsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.threadUnsubscribeIDs))
	copy(out, f.threadUnsubscribeIDs)
	return out
}

func queueNotification(session *fakeAppServerSession, method string, params map[string]any) {
	raw, _ := json.Marshal(params)
	session.enqueueEvent(appServerEvent{
		Notification: &appServerNotification{
			Method: method,
			Params: raw,
		},
	})
}

func queueRequest(session *fakeAppServerSession, method string, id json.RawMessage, params map[string]any) {
	raw, _ := json.Marshal(params)
	session.enqueueEvent(appServerEvent{
		Request: &appServerRequest{
			ID:     id,
			Method: method,
			Params: raw,
		},
	})
}

func (f *fakeAppServerSession) enqueueEvent(event appServerEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.turnStarted {
		f.pendingEvents = append(f.pendingEvents, event)
		return
	}
	f.events <- event
}

func assertShutdownError(t *testing.T, err error, wantMessage string) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), wantMessage) {
		t.Fatalf("error = %q, want contains %q", err.Error(), wantMessage)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, failureMessage string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal(failureMessage)
}

func containsAgentMessageText(updates []acp.SessionNotification, text string) bool {
	for _, update := range updates {
		if chunk := update.Update.AgentMessageChunk; chunk != nil && chunk.Content.Text != nil {
			if chunk.Content.Text.Text == text {
				return true
			}
		}
	}
	return false
}

func agentMessageChunks(updates []acp.SessionNotification) []*acp.SessionUpdateAgentMessageChunk {
	chunks := make([]*acp.SessionUpdateAgentMessageChunk, 0, len(updates))
	for _, update := range updates {
		if update.Update.AgentMessageChunk != nil {
			chunks = append(chunks, update.Update.AgentMessageChunk)
		}
	}
	return chunks
}

func chunkText(chunk *acp.SessionUpdateAgentMessageChunk) string {
	if chunk == nil || chunk.Content.Text == nil {
		return ""
	}
	return chunk.Content.Text.Text
}

func chunkMetaString(chunk *acp.SessionUpdateAgentMessageChunk, key string) string {
	if chunk == nil || chunk.Meta == nil {
		return ""
	}
	v, ok := chunk.Meta[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func chunkMetaBool(chunk *acp.SessionUpdateAgentMessageChunk, key string) (bool, bool) {
	if chunk == nil || chunk.Meta == nil {
		return false, false
	}
	v, ok := chunk.Meta[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func thoughtChunks(updates []acp.SessionNotification) []*acp.SessionUpdateAgentThoughtChunk {
	chunks := make([]*acp.SessionUpdateAgentThoughtChunk, 0, len(updates))
	for _, update := range updates {
		if chunk := update.Update.AgentThoughtChunk; chunk != nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

func thoughtChunkText(chunk *acp.SessionUpdateAgentThoughtChunk) string {
	if chunk == nil || chunk.Content.Text == nil {
		return ""
	}
	return chunk.Content.Text.Text
}

func thoughtChunkMessageID(chunk *acp.SessionUpdateAgentThoughtChunk) string {
	if chunk == nil || chunk.MessageId == nil {
		return ""
	}
	return *chunk.MessageId
}

func thoughtChunkMetaString(chunk *acp.SessionUpdateAgentThoughtChunk, key string) string {
	if chunk == nil || chunk.Meta == nil {
		return ""
	}
	v, ok := chunk.Meta[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func thoughtChunkMetaBool(chunk *acp.SessionUpdateAgentThoughtChunk, key string) (bool, bool) {
	if chunk == nil || chunk.Meta == nil {
		return false, false
	}
	v, ok := chunk.Meta[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func thoughtChunkMetaInt64(chunk *acp.SessionUpdateAgentThoughtChunk, key string) int64 {
	if chunk == nil || chunk.Meta == nil {
		return 0
	}
	v, ok := chunk.Meta[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func isUUIDString(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
				return false
			}
		}
	}
	return true
}

func latestUsageUpdate(updates []acp.SessionNotification) *acp.SessionUsageUpdate {
	for i := len(updates) - 1; i >= 0; i-- {
		if updates[i].Update.UsageUpdate != nil {
			return updates[i].Update.UsageUpdate
		}
	}
	return nil
}

func requireReasoningEffortOption(t *testing.T, options []acp.SessionConfigOption) *acp.SessionConfigOptionSelect {
	t.Helper()
	for _, option := range options {
		if option.Select == nil {
			continue
		}
		if option.Select.Id == acp.SessionConfigId(sessionConfigIDReasoningEffort) {
			return option.Select
		}
	}
	t.Fatalf("reasoning effort option missing: %#v", options)
	return nil
}

func requireModelOption(t *testing.T, options []acp.SessionConfigOption) *acp.SessionConfigOptionSelect {
	t.Helper()
	for _, option := range options {
		if option.Select == nil {
			continue
		}
		if option.Select.Id == acp.SessionConfigId(sessionConfigIDModel) {
			return option.Select
		}
	}
	t.Fatalf("model option missing: %#v", options)
	return nil
}

func modelOptionsInclude(option *acp.SessionConfigOptionSelect, modelID string) bool {
	if option == nil || option.Options.Ungrouped == nil {
		return false
	}
	for _, candidate := range *option.Options.Ungrouped {
		if candidate.Value == acp.SessionConfigValueId(modelID) {
			return true
		}
	}
	return false
}

func reasoningEffortOptionsInclude(option *acp.SessionConfigOptionSelect, effort string) bool {
	if option == nil || option.Options.Ungrouped == nil {
		return false
	}
	for _, candidate := range *option.Options.Ungrouped {
		if candidate.Value == acp.SessionConfigValueId(effort) {
			return true
		}
	}
	return false
}

func countAgentMessageChunks(updates []acp.SessionNotification) int {
	count := 0
	for _, update := range updates {
		if update.Update.AgentMessageChunk != nil {
			count++
		}
	}
	return count
}

func containsPlanEntry(updates []acp.SessionNotification, step string) bool {
	for _, update := range updates {
		if plan := update.Update.Plan; plan != nil {
			for _, entry := range plan.Entries {
				if entry.Content == step {
					return true
				}
			}
		}
	}
	return false
}

func latestPlanEntries(updates []acp.SessionNotification) []acp.PlanEntry {
	for i := len(updates) - 1; i >= 0; i-- {
		if plan := updates[i].Update.Plan; plan != nil {
			return append([]acp.PlanEntry(nil), plan.Entries...)
		}
	}
	return nil
}

func containsToolCall(updates []acp.SessionNotification, toolCallID string) bool {
	for _, update := range updates {
		if call := update.Update.ToolCall; call != nil && string(call.ToolCallId) == toolCallID {
			return true
		}
		if callUpdate := update.Update.ToolCallUpdate; callUpdate != nil && string(callUpdate.ToolCallId) == toolCallID {
			return true
		}
	}
	return false
}

func countToolCallEvents(updates []acp.SessionNotification) int {
	count := 0
	for _, update := range updates {
		if update.Update.ToolCall != nil {
			count++
		}
		if update.Update.ToolCallUpdate != nil {
			count++
		}
	}
	return count
}

func containsToolCallText(updates []acp.SessionNotification, text string) bool {
	for _, update := range updates {
		callUpdate := update.Update.ToolCallUpdate
		if callUpdate == nil {
			continue
		}
		for _, content := range callUpdate.Content {
			if content.Content == nil || content.Content.Content.Text == nil {
				continue
			}
			if content.Content.Content.Text.Text == text {
				return true
			}
		}
	}
	return false
}

func containsThoughtSubstring(updates []acp.SessionNotification, substring string) bool {
	for _, update := range updates {
		if chunk := update.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
			if strings.Contains(chunk.Content.Text.Text, substring) {
				return true
			}
		}
	}
	return false
}

func countThoughtText(updates []acp.SessionNotification, text string) int {
	count := 0
	for _, update := range updates {
		if chunk := update.Update.AgentThoughtChunk; chunk != nil && chunk.Content.Text != nil {
			if chunk.Content.Text.Text == text {
				count++
			}
		}
	}
	return count
}

func countThoughtChunks(updates []acp.SessionNotification) int {
	count := 0
	for _, update := range updates {
		if update.Update.AgentThoughtChunk != nil {
			count++
		}
	}
	return count
}

func TestStopReasonFromTurnStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   acp.StopReason
	}{
		{"completed", "completed", acp.StopReasonEndTurn},
		{"interrupted", "interrupted", acp.StopReasonCancelled},
		{"failed", "failed", acp.StopReasonEndTurn},
		{"empty", "", acp.StopReasonEndTurn},
		{"unknown status", "unknown_status", acp.StopReasonEndTurn},
		{"inProgress", "inProgress", acp.StopReasonEndTurn},
		{"cancelled", "cancelled", acp.StopReasonEndTurn},
		{"max_iterations", "max_iterations", acp.StopReasonEndTurn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopReasonFromTurnStatus(tt.status)
			if got != tt.want {
				t.Errorf("stopReasonFromTurnStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestToACPToolCallStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   acp.ToolCallStatus
	}{
		{"inProgress", "inProgress", acp.ToolCallStatusInProgress},
		{"completed", "completed", acp.ToolCallStatusCompleted},
		{"failed", "failed", acp.ToolCallStatusFailed},
		{"declined", "declined", acp.ToolCallStatusFailed},
		{"empty", "", acp.ToolCallStatusInProgress},
		{"unknown", "unknown", acp.ToolCallStatusInProgress},
		{"pending", "pending", acp.ToolCallStatusInProgress},
		{"in_progress", "in_progress", acp.ToolCallStatusInProgress},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toACPToolCallStatus(tt.status)
			if got != tt.want {
				t.Errorf("toACPToolCallStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestToACPPlanStatus(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   acp.PlanEntryStatus
	}{
		{"pending", "pending", acp.PlanEntryStatusPending},
		{"inProgress", "inProgress", acp.PlanEntryStatusInProgress},
		{"completed", "completed", acp.PlanEntryStatusCompleted},
		{"empty", "", acp.PlanEntryStatusPending},
		{"unknown", "unknown", acp.PlanEntryStatusPending},
		{"done", "done", acp.PlanEntryStatusPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toACPPlanStatus(tt.status)
			if got != tt.want {
				t.Errorf("toACPPlanStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestPermissionToolCallID(t *testing.T) {
	tests := []struct {
		name     string
		rawInput map[string]any
		want     acp.ToolCallId
	}{
		{
			name:     "with itemId",
			rawInput: map[string]any{"itemId": "item-123"},
			want:     "codex-item-item-123",
		},
		{
			name:     "with tool name",
			rawInput: map[string]any{"tool": "myTool"},
			want:     "codex-tool-myTool",
		},
		{
			name:     "with patchId",
			rawInput: map[string]any{"patchId": "patch-456"},
			want:     "codex-patch-patch-456",
		},
		{
			name:     "with command",
			rawInput: map[string]any{"command": "ls -la"},
			want:     "codex-cmd-ls -la",
		},
		{
			name:     "with serverName",
			rawInput: map[string]any{"serverName": "myMcp"},
			want:     "codex-mcp-myMcp",
		},
		{
			name:     "empty input",
			rawInput: map[string]any{},
			want:     "codex-permission-unknown",
		},
		{
			name:     "nil input",
			rawInput: nil,
			want:     "codex-permission-unknown",
		},
		{
			name:     "priority: itemId over tool",
			rawInput: map[string]any{"itemId": "item-1", "tool": "myTool"},
			want:     "codex-item-item-1",
		},
		{
			name:     "priority: tool over patchId",
			rawInput: map[string]any{"tool": "myTool", "patchId": "patch-1"},
			want:     "codex-tool-myTool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := permissionToolCallID(tt.rawInput)
			if got != tt.want {
				t.Errorf("permissionToolCallID() = %v, want %v", got, tt.want)
			}
		})
	}
}
