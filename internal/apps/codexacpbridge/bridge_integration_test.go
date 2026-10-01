//go:build integration && codex

package codexacp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const (
	integrationTestTimeout = 60 * time.Second
	tinyPNGBase64          = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO6Lr8kAAAAASUVORK5CYII="
)

func TestCodexACPIntegrationInitializeCapabilities(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	initResp := mustInitialize(t, client, stderr)
	if initResp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		t.Fatalf("initialize protocol version = %d, want %d", initResp.ProtocolVersion, acp.ProtocolVersionNumber)
	}

	caps := initResp.AgentCapabilities
	if caps.LoadSession {
		t.Fatalf("initialize loadSession = %t, want false", caps.LoadSession)
	}
	if !caps.McpCapabilities.Http {
		t.Fatal("initialize mcpCapabilities.http = false, want true")
	}
	if caps.McpCapabilities.Sse {
		t.Fatal("initialize mcpCapabilities.sse = true, want false")
	}
	if !caps.PromptCapabilities.Image {
		t.Fatal("initialize promptCapabilities.image = false, want true")
	}
	if caps.PromptCapabilities.Audio {
		t.Fatal("initialize promptCapabilities.audio = true, want false")
	}
	if caps.PromptCapabilities.EmbeddedContext {
		t.Fatal("initialize promptCapabilities.embeddedContext = true, want false")
	}
	if caps.SessionCapabilities.Resume == nil {
		t.Fatal("initialize sessionCapabilities.resume = nil, want non-nil")
	}
	if caps.SessionCapabilities.List == nil {
		t.Fatal("initialize sessionCapabilities.list = nil, want non-nil")
	}
	if caps.SessionCapabilities.Close == nil {
		t.Fatal("initialize sessionCapabilities.close = nil, want non-nil")
	}
}

func TestCodexACPIntegrationNewSessionExposesModels(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)
	_ = requireSessionModels(t, sessionResp, stderr.String())
}

func TestCodexACPIntegrationNewSessionRejectsUnsupportedCodexMetaKey(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	_, err := client.NewSessionWithMeta(ctx, workingDir, nil, map[string]any{
		"codex": map[string]any{
			"unsupported": true,
		},
	})
	if err == nil {
		failWithDetails(t, "session/new unexpectedly succeeded with unsupported _meta.codex key", nil, stderr.String())
	}
	assertInvalidParamsError(t, err)
}

func TestCodexACPIntegrationNewSessionRejectsCustomSessionID(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	_, err := client.NewSessionWithMeta(ctx, workingDir, nil, map[string]any{
		"sessionId": "custom-session-id",
	})
	if err == nil {
		failWithDetails(t, "session/new unexpectedly succeeded with custom _meta.sessionId", nil, stderr.String())
	}
	assertInvalidParamsError(t, err)
}

func TestCodexACPIntegrationSetModelRoundTrip(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)
	modelID := requireCurrentModelConfigValue(t, sessionResp, stderr.String())

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	if err := client.SetSessionModel(ctx, string(sessionResp.SessionId), modelID); err != nil {
		failWithDetails(t, "session/set_model failed", err, stderr.String())
	}
}

func TestCodexACPIntegrationSetModelUnknownModelPolicy(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)

	invalidModel := fmt.Sprintf("norma-invalid-model-%d", time.Now().UnixNano())
	if invalidModel == requireCurrentModelConfigValue(t, sessionResp, stderr.String()) {
		invalidModel += "-x"
	}

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	err := client.SetSessionModel(ctx, string(sessionResp.SessionId), invalidModel)
	if err == nil {
		failWithDetails(t, "session/set_model unexpectedly accepted unknown model id", nil, stderr.String())
	}
	assertInvalidParamsError(t, err)
}

func TestCodexACPIntegrationPromptTextFlow(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	updates, resultCh, err := client.Prompt(ctx, string(sessionResp.SessionId), "Say only: ok")
	if err != nil {
		failWithDetails(t, "session/prompt failed to start", err, stderr.String())
	}

	promptResult := awaitPromptResult(ctx, updates, resultCh)
	requirePromptSuccessOrSkipAuth(t, promptResult, stderr.String())
}

func TestCodexACPIntegrationPromptImageFlow(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	updates, resultCh, err := client.PromptWithContent(ctx, string(sessionResp.SessionId), []acp.ContentBlock{
		acp.TextBlock("Describe this image in one short word."),
		acp.ImageBlock(tinyPNGBase64, "image/png"),
	})
	if err != nil {
		failWithDetails(t, "session/prompt(image) failed to start", err, stderr.String())
	}

	promptResult := awaitPromptResult(ctx, updates, resultCh)
	requirePromptSuccessOrSkipAuth(t, promptResult, stderr.String())
}

func TestCodexACPIntegrationPromptAudioRejectedAsInvalidParams(t *testing.T) {
	workingDir := requireCodexEnvironment(t)
	bin := buildCodexACPBinary(t, workingDir)

	client, stderr := newCodexACPClient(t, workingDir, bin)
	_ = mustInitialize(t, client, stderr)
	sessionResp := mustNewSession(t, client, stderr, workingDir)

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	updates, resultCh, err := client.PromptWithContent(ctx, string(sessionResp.SessionId), []acp.ContentBlock{
		acp.AudioBlock("Zm9v", "audio/wav"),
	})
	if err != nil {
		failWithDetails(t, "session/prompt(audio) failed to start", err, stderr.String())
	}

	promptResult := awaitPromptResult(ctx, updates, resultCh)
	if promptResult.Err == nil {
		failWithDetails(t, "session/prompt(audio) unexpectedly succeeded", nil, stderr.String())
	}
	assertInvalidParamsError(t, promptResult.Err)
}

func awaitPromptResult(
	ctx context.Context,
	updates <-chan integrationExtendedSessionNotification,
	resultCh <-chan integrationPromptResult,
) integrationPromptResult {
	var result integrationPromptResult
	for updates != nil || resultCh != nil {
		select {
		case <-ctx.Done():
			return integrationPromptResult{Err: ctx.Err()}
		case _, ok := <-updates:
			if !ok {
				updates = nil
			}
		case r, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			result = r
			resultCh = nil
		}
	}
	return result
}

func requirePromptSuccessOrSkipAuth(t *testing.T, promptResult integrationPromptResult, stderr string) {
	t.Helper()

	if promptResult.Err != nil {
		if isLikelyAuthEnvError(promptResult.Err) {
			t.Skipf("skipping prompt integration without codex auth/session context: %v", promptResult.Err)
		}
		failWithDetails(t, "session/prompt failed", promptResult.Err, stderr)
	}
	if promptResult.Response.StopReason == "" {
		t.Fatal("prompt response stop reason is empty")
	}
}

func maybeSkipCodexIntegration(t *testing.T, err error, stderr string) {
	t.Helper()
	if err == nil {
		return
	}

	combined := strings.ToLower(strings.TrimSpace(err.Error()) + "\n" + strings.TrimSpace(stderr))
	skipMarkers := []string{
		"read-only file system",
		"failed to initialize session",
		"could not update path",
	}
	for _, marker := range skipMarkers {
		if strings.Contains(combined, marker) {
			t.Skipf("codex ACP unavailable in this environment (%s)", marker)
		}
	}
}

func requireCurrentModelConfigValue(t *testing.T, resp acp.NewSessionResponse, stderr string) string {
	t.Helper()

	if strings.TrimSpace(string(resp.SessionId)) == "" {
		failWithDetails(t, "session/new returned empty session id", nil, stderr)
	}
	for _, option := range resp.ConfigOptions {
		if option.Select == nil {
			continue
		}
		if strings.TrimSpace(string(option.Select.Id)) != "model" {
			continue
		}
		current := strings.TrimSpace(string(option.Select.CurrentValue))
		if current == "" {
			failWithDetails(t, "session/new returned empty model config currentValue", nil, stderr)
		}
		foundCurrent := false
		for i, model := range option.Select.Options {
			if strings.TrimSpace(string(model.Value)) == "" {
				t.Fatalf("session/new model option[%d].value is empty", i)
			}
			if strings.TrimSpace(model.Name) == "" {
				t.Fatalf("session/new model option[%d].name is empty", i)
			}
			if string(model.Value) == current {
				foundCurrent = true
			}
		}
		if !foundCurrent {
			t.Fatalf("session/new model currentValue %q not present in options", current)
		}
		return current
	}
	failWithDetails(t, "session/new returned no model config option", nil, stderr)
	return ""
}

func assertInvalidParamsError(t *testing.T, err error) {
	t.Helper()

	var reqErr *acp.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("error = %T %v, want *acp.RequestError", err, err)
	}
	if reqErr.Code != -32602 {
		t.Fatalf("request error code = %d, want -32602 (invalid_params)", reqErr.Code)
	}
}

func isLikelyAuthEnvError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	hints := []string{
		"unauthorized",
		"auth",
		"login",
		"api key",
		"account",
		"forbidden",
		"401",
		"403",
	}
	for _, hint := range hints {
		if strings.Contains(msg, hint) {
			return true
		}
	}
	return false
}

func requireCodexEnvironment(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("codex"); err != nil {
		t.Fatalf("codex binary not found in PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	helpCmd := exec.CommandContext(ctx, "codex", "app-server", "--help")
	var helpOut bytes.Buffer
	helpCmd.Stdout = &helpOut
	helpCmd.Stderr = &helpOut
	if err := helpCmd.Run(); err != nil {
		t.Fatalf("codex app-server --help failed: %v | output=%s", err, strings.TrimSpace(helpOut.String()))
	}

	return findWorkingDir(t)
}

func findWorkingDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat go.mod failed in %q: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate working dir containing go.mod (started from %q)", dir)
		}
		dir = parent
	}
}

func buildCodexACPBinary(t *testing.T, workingDir string) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "codex-acp")
	goCacheDir := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(goCacheDir, 0o755); err != nil {
		t.Fatalf("create GOCACHE dir %q: %v", goCacheDir, err)
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/codex-acp")
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), "GOCACHE="+goCacheDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build codex-acp binary failed: %v | output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func newCodexACPClient(t *testing.T, workingDir, binPath string, args ...string) (*integrationACPClient, *bytes.Buffer) {
	t.Helper()

	command := []string{binPath}
	command = append(command, args...)

	var stderr bytes.Buffer
	client, err := newIntegrationACPClient(context.Background(), integrationACPClientConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     &stderr,
	})
	if err != nil {
		failWithDetails(t, "start codex-acp client failed", err, stderr.String())
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client, &stderr
}

func mustInitialize(t *testing.T, client *integrationACPClient, stderr *bytes.Buffer) acp.InitializeResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	resp, err := client.Initialize(ctx)
	if err != nil {
		maybeSkipCodexIntegration(t, err, stderr.String())
		failWithDetails(t, "initialize failed", err, stderr.String())
	}
	return resp
}

func mustNewSession(t *testing.T, client *integrationACPClient, stderr *bytes.Buffer, cwd string) acp.NewSessionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), integrationTestTimeout)
	defer cancel()

	resp, err := client.NewSession(ctx, cwd, nil)
	if err != nil {
		maybeSkipCodexIntegration(t, err, stderr.String())
		failWithDetails(t, "session/new failed", err, stderr.String())
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		failWithDetails(t, "session/new returned empty session id", nil, stderr.String())
	}
	return resp
}

func failWithDetails(t *testing.T, heading string, err error, stderr string) {
	t.Helper()

	errText := ""
	if err != nil {
		errText = strings.TrimSpace(err.Error())
	}
	stderrText := strings.TrimSpace(stderr)

	message := heading
	if errText != "" {
		message += ": " + errText
	}
	if stderrText != "" && (errText == "" || !strings.Contains(stderrText, errText)) {
		message += " | stderr: " + stderrText
	}
	t.Fatal(message)
}
