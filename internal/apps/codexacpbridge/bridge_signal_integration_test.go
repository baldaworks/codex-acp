//go:build integration

package codexacp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

const helperIntegrationTestTimeout = 20 * time.Second

type fakeCodexHelperHarness struct {
	stateDir   string
	eventsPath string
}

type fakeCodexHelperEvent struct {
	Type     string `json:"type"`
	Instance int    `json:"instance"`
	PID      int    `json:"pid"`
	Method   string `json:"method,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Signal   string `json:"signal,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type fakeCodexHelperEnvelope struct {
	Method string          `json:"method,omitempty"`
	ID     json.RawMessage `json:"id,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func TestBridgeIntegrationDeferredBackendInitializesWithoutCodex(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	isolatedDir := t.TempDir()

	var stderr lockedBuffer
	client, err := newIntegrationACPClient(context.Background(), integrationACPClientConfig{
		Command:    []string{binPath, "--defer-backend"},
		WorkingDir: workingDir,
		Stderr:     &stderr,
		Env: []string{
			"HOME=" + isolatedDir,
			"PATH=" + isolatedDir,
		},
	})
	if err != nil {
		t.Fatalf("start bridge client error = %v | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	initResponse := helperMustInitialize(t, client, &stderr)
	if got, want := len(initResponse.AuthMethods), 1; got != want {
		t.Fatalf("Initialize().AuthMethods length = %d, want %d", got, want)
	}
	if auth := initResponse.AuthMethods[0].Terminal; auth == nil || auth.Type != "terminal" || auth.Id != "codex-login" {
		t.Fatalf("Initialize().AuthMethods[0] = %#v, want codex-login terminal auth", initResponse.AuthMethods[0])
	}

	ctx, cancel := context.WithTimeout(context.Background(), helperIntegrationTestTimeout)
	defer cancel()
	_, err = client.NewSession(ctx, workingDir, nil)
	if err == nil {
		t.Fatal("NewSession() error = nil, want missing Codex error")
	}
	if !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("NewSession() error = %q, want missing Codex context | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}

	if err := client.CloseStdin(); err != nil {
		t.Fatalf("CloseStdin() error = %v", err)
	}
	if err := waitForIntegrationClientExit(t, client); err != nil {
		t.Fatalf("bridge exit error = %v | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
}

func TestBridgeIntegrationSIGINTShutsDownLiveSessionBackend(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	harness := newFakeCodexHelperHarness(t)

	client, stderr := newHelperBackedACPClient(t, workingDir, binPath, harness, "steady")
	helperMustInitialize(t, client, stderr)
	helperMustNewSession(t, client, stderr, workingDir)
	startIndex := len(harness.events(t))

	if err := client.Signal(os.Interrupt); err != nil {
		t.Fatalf("Signal(SIGINT) error = %v", err)
	}
	waitErr := waitForIntegrationClientExit(t, client)
	eventsAfter := harness.eventsSince(t, startIndex)

	if !containsSignalEvent(eventsAfter, os.Interrupt.String()) {
		t.Fatalf("helper events after SIGINT = %#v | all events = %#v | waitErr = %v | stderr=%s", eventsAfter, harness.events(t), waitErr, strings.TrimSpace(stderr.String()))
	}
	assertNoClosedPipeNoise(t, nil, stderr.String())
}

func TestBridgeIntegrationSIGTERMShutsDownLiveSessionBackend(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	harness := newFakeCodexHelperHarness(t)

	client, stderr := newHelperBackedACPClient(t, workingDir, binPath, harness, "steady")
	helperMustInitialize(t, client, stderr)
	helperMustNewSession(t, client, stderr, workingDir)
	startIndex := len(harness.events(t))

	if err := client.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal(SIGTERM) error = %v", err)
	}
	waitErr := waitForIntegrationClientExit(t, client)
	eventsAfter := harness.eventsSince(t, startIndex)

	if !containsSignalEvent(eventsAfter, os.Interrupt.String()) {
		t.Fatalf("helper events after SIGTERM = %#v | all events = %#v | waitErr = %v | stderr=%s", eventsAfter, harness.events(t), waitErr, strings.TrimSpace(stderr.String()))
	}
	assertNoClosedPipeNoise(t, nil, stderr.String())
}

func TestBridgeIntegrationACPStdioDisconnectTriggersBackendShutdown(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	harness := newFakeCodexHelperHarness(t)

	client, stderr := newHelperBackedACPClient(t, workingDir, binPath, harness, "steady")
	helperMustInitialize(t, client, stderr)
	helperMustNewSession(t, client, stderr, workingDir)
	startIndex := len(harness.events(t))

	if err := client.CloseStdin(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatalf("CloseStdin() error = %v", err)
	}
	waitErr := waitForIntegrationClientExit(t, client)
	events := harness.eventsSince(t, startIndex)
	if !containsSignalEvent(events, os.Interrupt.String()) && !containsReasonEvent(events, "stdin_eof") {
		t.Fatalf("helper events after ACP stdio disconnect = %#v | all events = %#v | waitErr = %v | stderr=%s", events, harness.events(t), waitErr, strings.TrimSpace(stderr.String()))
	}
	assertNoClosedPipeNoise(t, nil, stderr.String())
}

func TestBridgeIntegrationSharesOneBackendAcrossSessions(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	harness := newFakeCodexHelperHarness(t)

	client, stderr := newHelperBackedACPClient(t, workingDir, binPath, harness, "steady")
	helperMustInitialize(t, client, stderr)
	first := helperMustNewSession(t, client, stderr, workingDir)
	second := helperMustNewSession(t, client, stderr, workingDir)
	if first.SessionId == second.SessionId {
		t.Fatalf("session ids = %q and %q, want distinct Codex threads", first.SessionId, second.SessionId)
	}

	startCount := 0
	for _, event := range harness.events(t) {
		if event.Type == "start" {
			startCount++
		}
	}
	if startCount != 1 {
		t.Fatalf("helper start event count = %d, want one shared app-server", startCount)
	}
}

func TestBridgeIntegrationPromptRecreatesBackendAfterPrePromptBackendExit(t *testing.T) {
	workingDir := integrationWorkingDir(t)
	binPath := buildIntegrationBridgeBinary(t, workingDir)
	harness := newFakeCodexHelperHarness(t)

	client, stderr := newHelperBackedACPClient(t, workingDir, binPath, harness, "exit_after_thread_start")
	helperMustInitialize(t, client, stderr)
	sessionResp := helperMustNewSession(t, client, stderr, workingDir)

	waitForHelperEvents(t, harness, func(events []fakeCodexHelperEvent) bool {
		return containsReasonEvent(events, "exit_after_thread_start")
	}, "session backend did not exit after thread/start")

	ctx, cancel := context.WithTimeout(context.Background(), helperIntegrationTestTimeout)
	defer cancel()

	updates, resultCh, err := client.Prompt(ctx, string(sessionResp.SessionId), "Say only: ok")
	if err != nil {
		assertNoClosedPipeNoise(t, err, stderr.String())
		t.Fatalf("Prompt() start error = %v", err)
	}

	promptResult := awaitIntegrationPromptResult(ctx, updates, resultCh)
	assertNoClosedPipeNoise(t, promptResult.Err, stderr.String())
	if promptResult.Err != nil {
		t.Fatalf("Prompt() error = %v | helper events = %#v | stderr=%s", promptResult.Err, harness.events(t), strings.TrimSpace(stderr.String()))
	}
	if promptResult.Response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("Prompt().StopReason = %q, want %q", promptResult.Response.StopReason, acp.StopReasonEndTurn)
	}

	startCount := 0
	for _, event := range harness.events(t) {
		if event.Type == "start" {
			startCount++
		}
	}
	if startCount != 2 {
		t.Fatalf("helper start event count = %d, want 2 (failed shared backend + recreated backend)", startCount)
	}
}

func newFakeCodexHelperHarness(t *testing.T) *fakeCodexHelperHarness {
	t.Helper()

	stateDir := t.TempDir()
	return &fakeCodexHelperHarness{
		stateDir:   stateDir,
		eventsPath: filepath.Join(stateDir, "events.jsonl"),
	}
}

func (h *fakeCodexHelperHarness) events(t *testing.T) []fakeCodexHelperEvent {
	t.Helper()

	raw, err := os.ReadFile(h.eventsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", h.eventsPath, err)
	}

	var events []fakeCodexHelperEvent
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event fakeCodexHelperEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("unmarshal helper event %q error = %v", line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan helper events error = %v", err)
	}
	return events
}

func (h *fakeCodexHelperHarness) eventsSince(t *testing.T, start int) []fakeCodexHelperEvent {
	t.Helper()

	events := h.events(t)
	if start >= len(events) {
		return nil
	}
	return append([]fakeCodexHelperEvent(nil), events[start:]...)
}

func newHelperBackedACPClient(
	t *testing.T,
	workingDir string,
	binPath string,
	harness *fakeCodexHelperHarness,
	mode string,
) (*integrationACPClient, *lockedBuffer) {
	t.Helper()

	helperDir := installFakeCodexScript(t)
	env := append(os.Environ(),
		"PATH="+helperDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GO_WANT_FAKE_CODEX_APP_SERVER=1",
		"FAKE_CODEX_STATE_DIR="+harness.stateDir,
		"FAKE_CODEX_MODE="+mode,
	)

	var stderr lockedBuffer
	client, err := newIntegrationACPClient(context.Background(), integrationACPClientConfig{
		Command:    []string{binPath},
		WorkingDir: workingDir,
		Stderr:     &stderr,
		Env:        env,
	})
	if err != nil {
		t.Fatalf("start bridge client error = %v | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client, &stderr
}

func installFakeCodexScript(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "codex")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=TestIntegrationFakeCodexHelperProcess -- \"$@\"\n", os.Args[0])
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}
	return dir
}

func helperMustInitialize(t *testing.T, client *integrationACPClient, stderr *lockedBuffer) acp.InitializeResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), helperIntegrationTestTimeout)
	defer cancel()

	resp, err := client.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize() error = %v | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
	if resp.ProtocolVersion != acp.ProtocolVersion(acp.ProtocolVersionNumber) {
		t.Fatalf("Initialize().ProtocolVersion = %d, want %d", resp.ProtocolVersion, acp.ProtocolVersionNumber)
	}
	return resp
}

func helperMustNewSession(
	t *testing.T,
	client *integrationACPClient,
	stderr *lockedBuffer,
	cwd string,
) acp.NewSessionResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), helperIntegrationTestTimeout)
	defer cancel()

	resp, err := client.NewSession(ctx, cwd, nil)
	if err != nil {
		t.Fatalf("NewSession() error = %v | stderr=%s", err, strings.TrimSpace(stderr.String()))
	}
	if strings.TrimSpace(string(resp.SessionId)) == "" {
		t.Fatalf("NewSession().SessionId is empty | stderr=%s", strings.TrimSpace(stderr.String()))
	}
	return resp
}

func waitForIntegrationClientExit(t *testing.T, client *integrationACPClient) error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.Wait()
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(helperIntegrationTestTimeout):
		t.Fatalf("bridge process %d did not exit before timeout", client.PID())
		return nil
	}
}

func awaitIntegrationPromptResult(
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
		case promptResult, ok := <-resultCh:
			if !ok {
				resultCh = nil
				continue
			}
			result = promptResult
			resultCh = nil
		}
	}
	return result
}

func waitForHelperEvents(
	t *testing.T,
	harness *fakeCodexHelperHarness,
	cond func([]fakeCodexHelperEvent) bool,
	failureMessage string,
) {
	t.Helper()

	deadline := time.Now().Add(helperIntegrationTestTimeout)
	for time.Now().Before(deadline) {
		events := harness.events(t)
		if cond(events) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s: %#v", failureMessage, harness.events(t))
}

func containsSignalEvent(events []fakeCodexHelperEvent, want string) bool {
	for _, event := range events {
		if event.Type == "signal" && event.Signal == want {
			return true
		}
	}
	return false
}

func containsReasonEvent(events []fakeCodexHelperEvent, want string) bool {
	for _, event := range events {
		if event.Reason == want {
			return true
		}
	}
	return false
}

func assertNoClosedPipeNoise(t *testing.T, err error, stderr string) {
	t.Helper()

	combined := stderr
	if err != nil {
		combined += "\n" + err.Error()
	}
	lower := strings.ToLower(combined)
	for _, marker := range []string{"file already closed", "write |1:", "broken pipe"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("unexpected raw pipe error marker %q in %q", marker, combined)
		}
	}
}

func integrationWorkingDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(go.mod) in %q error = %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod starting from %q", dir)
		}
		dir = parent
	}
}

func buildIntegrationBridgeBinary(t *testing.T, workingDir string) string {
	t.Helper()

	binPath := filepath.Join(t.TempDir(), "codex-acp")
	goCacheDir := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(goCacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", goCacheDir, err)
	}

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/codex-acp")
	cmd.Dir = workingDir
	cmd.Env = append(os.Environ(), "GOCACHE="+goCacheDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build error = %v | output=%s", err, strings.TrimSpace(string(out)))
	}
	return binPath
}

func TestIntegrationFakeCodexHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FAKE_CODEX_APP_SERVER") != "1" {
		return
	}

	args := os.Args
	if len(args) < 2 || args[len(args)-1] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: codex app-server")
		os.Exit(0)
	}

	stateDir := os.Getenv("FAKE_CODEX_STATE_DIR")
	mode := strings.TrimSpace(os.Getenv("FAKE_CODEX_MODE"))
	if strings.TrimSpace(stateDir) == "" {
		os.Exit(2)
	}

	instance, err := nextFakeCodexHelperInstance(stateDir)
	if err != nil {
		os.Exit(3)
	}
	writeFakeCodexHelperEvent(stateDir, fakeCodexHelperEvent{
		Type:     "start",
		Instance: instance,
		PID:      os.Getpid(),
		Mode:     mode,
	})

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		writeFakeCodexHelperEvent(stateDir, fakeCodexHelperEvent{
			Type:     "signal",
			Instance: instance,
			PID:      os.Getpid(),
			Signal:   sig.String(),
			Reason:   "signal",
		})
		os.Exit(0)
	}()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	encoder := json.NewEncoder(os.Stdout)
	threadCount := 0
	turnCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		var env fakeCodexHelperEnvelope
		if err := json.Unmarshal(line, &env); err != nil {
			continue
		}
		if strings.TrimSpace(env.Method) == "" {
			continue
		}

		writeFakeCodexHelperEvent(stateDir, fakeCodexHelperEvent{
			Type:     "method",
			Instance: instance,
			PID:      os.Getpid(),
			Method:   env.Method,
		})

		switch env.Method {
		case "initialize":
			_ = encoder.Encode(map[string]any{
				"id": env.ID,
				"result": map[string]any{
					"userAgent": "codex_test/1.0.0",
				},
			})
		case "initialized":
		case "model/list":
			_ = encoder.Encode(map[string]any{
				"id": env.ID,
				"result": map[string]any{
					"data": []map[string]any{
						{
							"id":          "gpt-5.4",
							"displayName": "GPT-5.4",
							"isDefault":   true,
						},
					},
				},
			})
		case "thread/start":
			threadCount++
			threadID := fmt.Sprintf("thr-%d-%d", instance, threadCount)
			_ = encoder.Encode(map[string]any{
				"id": env.ID,
				"result": map[string]any{
					"thread": map[string]any{
						"id":        threadID,
						"sessionId": threadID,
					},
					"model": "gpt-5.4",
				},
			})
			if mode == "exit_after_thread_start" && instance == 1 {
				writeFakeCodexHelperEvent(stateDir, fakeCodexHelperEvent{
					Type:     "exit",
					Instance: instance,
					PID:      os.Getpid(),
					Reason:   "exit_after_thread_start",
				})
				os.Exit(0)
			}
		case "turn/start":
			turnCount++
			var params map[string]any
			_ = json.Unmarshal(env.Params, &params)
			threadID := stringValueForFakeHelper(params, "threadId")
			turnID := fmt.Sprintf("turn-%d-%d", instance, turnCount)
			_ = encoder.Encode(map[string]any{
				"id": env.ID,
				"result": map[string]any{
					"turn": map[string]any{
						"id": turnID,
					},
				},
			})
			_ = encoder.Encode(map[string]any{
				"method": "turn/completed",
				"params": map[string]any{
					"threadId": threadID,
					"turnId":   turnID,
					"turn": map[string]any{
						"id":     turnID,
						"status": "completed",
					},
				},
			})
		case "turn/interrupt":
			_ = encoder.Encode(map[string]any{
				"id":     env.ID,
				"result": map[string]any{},
			})
		default:
			_ = encoder.Encode(map[string]any{
				"id":     env.ID,
				"result": map[string]any{},
			})
		}
	}

	reason := "stdin_eof"
	if err := scanner.Err(); err != nil {
		reason = "stdin_error"
	}
	writeFakeCodexHelperEvent(stateDir, fakeCodexHelperEvent{
		Type:     "exit",
		Instance: instance,
		PID:      os.Getpid(),
		Reason:   reason,
	})
	os.Exit(0)
}

func stringValueForFakeHelper(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func nextFakeCodexHelperInstance(stateDir string) (int, error) {
	counterPath := filepath.Join(stateDir, "counter.txt")
	raw, err := os.ReadFile(counterPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}

	next := 1
	if len(raw) > 0 {
		_, _ = fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &next)
		next++
	}
	if err := os.WriteFile(counterPath, []byte(fmt.Sprintf("%d\n", next)), 0o644); err != nil {
		return 0, err
	}
	return next, nil
}

func writeFakeCodexHelperEvent(stateDir string, event fakeCodexHelperEvent) {
	path := filepath.Join(stateDir, "events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		os.Exit(4)
	}
	defer f.Close()

	raw, err := json.Marshal(event)
	if err != nil {
		os.Exit(5)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		os.Exit(6)
	}
}
