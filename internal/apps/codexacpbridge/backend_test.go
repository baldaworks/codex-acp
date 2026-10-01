package codexacp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

const (
	errRequestIDRequired = "request id is required"
	errBackendStopped    = "bridge backend stopped"
)

type captureWriteCloser struct {
	mu       sync.Mutex
	writes   [][]byte
	writeErr error
	closeErr error
}

func (c *captureWriteCloser) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	cp := append([]byte(nil), p...)
	c.writes = append(c.writes, cp)
	return len(p), nil
}

func (c *captureWriteCloser) Close() error {
	return c.closeErr
}

func (c *captureWriteCloser) writesSnapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	for i := range c.writes {
		out[i] = append([]byte(nil), c.writes[i]...)
	}
	return out
}

func newTestBackend(writer io.WriteCloser) *appServerBackend {
	logger := testNopLogger()
	return &appServerBackend{
		stdin:               writer,
		logger:              logger,
		pending:             make(map[string]chan appServerRPCResponse),
		events:              make(chan appServerEvent, 16),
		closing:             make(chan struct{}),
		done:                make(chan struct{}),
		readDone:            make(chan struct{}),
		shutdownGracePeriod: defaultBackendShutdownGracePeriod,
	}
}

func testNopLogger() *zerolog.Logger {
	nop := zerolog.Nop()
	return &nop
}

func parseFirstWriteAsJSONMap(t *testing.T, writer *captureWriteCloser) map[string]any {
	t.Helper()
	writes := writer.writesSnapshot()
	if len(writes) == 0 {
		t.Fatal("writer captured no payload")
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(writes[0]), &payload); err != nil {
		t.Fatalf("unmarshal payload error = %v", err)
	}
	return payload
}

func waitForPendingRequest(t *testing.T, b *appServerBackend, key string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		b.pendingMu.Lock()
		_, ok := b.pending[key]
		b.pendingMu.Unlock()
		if ok {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("pending request %q not found before timeout", key)
}

func TestAppServerRPCErrorError(t *testing.T) {
	var nilErr *appServerRPCError
	if got := nilErr.Error(); got != "" {
		t.Fatalf("(*appServerRPCError)(nil).Error() = %q, want empty", got)
	}

	errValue := &appServerRPCError{Code: -32000, Message: "boom"}
	if got, want := errValue.Error(), "bridge backend rpc error (-32000): boom"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestCanonicalRequestID(t *testing.T) {
	if got, want := canonicalRequestID(json.RawMessage("  123 ")), "123"; got != want {
		t.Fatalf("canonicalRequestID() = %q, want %q", got, want)
	}
}

func TestSendJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		ctx       context.Context
		closeDone bool
		payload   any
		writeErr  error
		wantErr   string
	}{
		{
			name:    "marshal error",
			ctx:     context.Background(),
			payload: map[string]any{"bad": make(chan int)},
			wantErr: "marshal bridge backend payload",
		},
		{
			name:    "context canceled",
			ctx:     canceledContext(),
			payload: map[string]any{"ok": true},
			wantErr: context.Canceled.Error(),
		},
		{
			name:      "backend stopped",
			ctx:       context.Background(),
			closeDone: true,
			payload:   map[string]any{"ok": true},
			wantErr:   errBackendStopped,
		},
		{
			name:     "write error",
			ctx:      context.Background(),
			payload:  map[string]any{"ok": true},
			writeErr: errors.New("sink failed"),
			wantErr:  "write bridge backend payload",
		},
		{
			name:     "closed pipe write maps to backend stopped",
			ctx:      context.Background(),
			payload:  map[string]any{"ok": true},
			writeErr: io.ErrClosedPipe,
			wantErr:  errBackendStopped,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer := &captureWriteCloser{writeErr: tc.writeErr}
			backend := newTestBackend(writer)
			if tc.closeDone {
				close(backend.done)
			}

			err := backend.sendJSON(tc.ctx, tc.payload)
			if err == nil {
				t.Fatalf("sendJSON() error = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("sendJSON() error = %q, want contains %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestSendRequestResponseErrorAndNotification(t *testing.T) {
	writer := &captureWriteCloser{}
	backend := newTestBackend(writer)

	if err := backend.sendRequest(context.Background(), json.RawMessage("1"), "model/list", map[string]any{"a": "b"}); err != nil {
		t.Fatalf("sendRequest() error = %v", err)
	}
	payload := parseFirstWriteAsJSONMap(t, writer)
	if got, want := payload["method"], "model/list"; got != want {
		t.Fatalf("sendRequest method = %#v, want %q", got, want)
	}
	if _, ok := payload["params"]; !ok {
		t.Fatal("sendRequest params missing")
	}

	writer = &captureWriteCloser{}
	backend = newTestBackend(writer)
	if err := backend.sendResponse(context.Background(), json.RawMessage("2"), map[string]any{"ok": true}); err != nil {
		t.Fatalf("sendResponse() error = %v", err)
	}
	payload = parseFirstWriteAsJSONMap(t, writer)
	if _, ok := payload["result"]; !ok {
		t.Fatal("sendResponse result missing")
	}

	writer = &captureWriteCloser{}
	backend = newTestBackend(writer)
	if err := backend.sendError(context.Background(), json.RawMessage("3"), -32601, "bad", map[string]any{"m": "x"}); err != nil {
		t.Fatalf("sendError() error = %v", err)
	}
	payload = parseFirstWriteAsJSONMap(t, writer)
	errObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("sendError payload error type = %T, want map[string]any", payload["error"])
	}
	if got, want := errObj["message"], "bad"; got != want {
		t.Fatalf("sendError message = %#v, want %q", got, want)
	}

	writer = &captureWriteCloser{}
	backend = newTestBackend(writer)
	if err := backend.sendNotification(context.Background(), "initialized", nil); err != nil {
		t.Fatalf("sendNotification() error = %v", err)
	}
	payload = parseFirstWriteAsJSONMap(t, writer)
	if got, want := payload["method"], "initialized"; got != want {
		t.Fatalf("sendNotification method = %#v, want %q", got, want)
	}
}

func TestAppServerInitializeParamsOptOutNotificationMethods(t *testing.T) {
	t.Run("defaults opt out message deltas and raw reasoning deltas", func(t *testing.T) {
		params := appServerInitializeParams("bridge", Options{})
		caps, ok := params["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("capabilities type = %T, want map[string]any", params["capabilities"])
		}
		methods, ok := caps["optOutNotificationMethods"].([]string)
		if !ok {
			t.Fatalf("optOutNotificationMethods type = %T, want []string", caps["optOutNotificationMethods"])
		}
		want := []string{
			methodAgentMessageDelta,
			methodReasoningTextDelta,
		}
		if !slices.Equal(methods, want) {
			t.Fatalf("optOutNotificationMethods = %#v, want %#v", methods, want)
		}
	})

	t.Run("message streaming and non-streaming reasoning opt out raw reasoning deltas only", func(t *testing.T) {
		opts := Options{MessageStreaming: true}
		opts.SetReasoningStreaming(false)
		params := appServerInitializeParams("bridge", opts)
		caps, ok := params["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("capabilities type = %T, want map[string]any", params["capabilities"])
		}
		methods, ok := caps["optOutNotificationMethods"].([]string)
		if !ok {
			t.Fatalf("optOutNotificationMethods type = %T, want []string", caps["optOutNotificationMethods"])
		}
		want := []string{methodReasoningTextDelta}
		if !slices.Equal(methods, want) {
			t.Fatalf("optOutNotificationMethods = %#v, want %#v", methods, want)
		}
	})

	t.Run("summary-only streaming opts out raw reasoning deltas", func(t *testing.T) {
		opts := Options{MessageStreaming: true, ReasoningThoughts: "summary"}
		params := appServerInitializeParams("bridge", opts)
		caps := params["capabilities"].(map[string]any)
		methods := caps["optOutNotificationMethods"].([]string)
		want := []string{methodReasoningTextDelta}
		if !slices.Equal(methods, want) {
			t.Fatalf("optOutNotificationMethods = %#v, want %#v", methods, want)
		}
	})

	t.Run("content-only streaming opts out summary reasoning notifications", func(t *testing.T) {
		opts := Options{MessageStreaming: true, ReasoningThoughts: "content"}
		params := appServerInitializeParams("bridge", opts)
		caps := params["capabilities"].(map[string]any)
		methods := caps["optOutNotificationMethods"].([]string)
		want := []string{
			methodReasoningSummaryTextDelta,
			methodReasoningSummaryPartAdded,
		}
		if !slices.Equal(methods, want) {
			t.Fatalf("optOutNotificationMethods = %#v, want %#v", methods, want)
		}
	})

	t.Run("reasoning thoughts off opts out all reasoning notifications", func(t *testing.T) {
		opts := Options{MessageStreaming: true, ReasoningThoughts: "off"}
		params := appServerInitializeParams("bridge", opts)
		caps := params["capabilities"].(map[string]any)
		methods := caps["optOutNotificationMethods"].([]string)
		want := []string{
			methodReasoningTextDelta,
			methodReasoningSummaryTextDelta,
			methodReasoningSummaryPartAdded,
		}
		if !slices.Equal(methods, want) {
			t.Fatalf("optOutNotificationMethods = %#v, want %#v", methods, want)
		}
	})
}

func TestCallSuccessAndDecodePaths(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)

		type response struct {
			Value string `json:"value"`
		}
		var out response
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.call(context.Background(), "x/method", map[string]any{"p": "v"}, &out)
		}()

		waitForPendingRequest(t, backend, "1")
		if err := backend.handleIncomingLine([]byte(`{"id":1,"result":{"value":"ok"}}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("call() error = %v", err)
		}
		if got, want := out.Value, "ok"; got != want {
			t.Fatalf("decoded value = %q, want %q", got, want)
		}
	})

	t.Run("rpc error", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.call(context.Background(), "x/method", nil, nil)
		}()

		waitForPendingRequest(t, backend, "1")
		if err := backend.handleIncomingLine([]byte(`{"id":1,"error":{"code":-32000,"message":"nope"}}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		err := <-errCh
		if err == nil {
			t.Fatal("call() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "bridge backend rpc error") {
			t.Fatalf("call() error = %q, want rpc error", err.Error())
		}
	})

	t.Run("decode error", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		var out struct {
			Value string `json:"value"`
		}
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.call(context.Background(), "x/method", nil, &out)
		}()

		waitForPendingRequest(t, backend, "1")
		if err := backend.handleIncomingLine([]byte(`{"id":1,"result":["bad"]}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		err := <-errCh
		if err == nil {
			t.Fatal("call() error = nil, want decode error")
		}
		if !strings.Contains(err.Error(), "decode x/method response") {
			t.Fatalf("call() error = %q, want decode context", err.Error())
		}
	})
}

func TestCallContextAndDonePaths(t *testing.T) {
	t.Run("context canceled after send", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.call(ctx, "x/method", map[string]any{"p": "v"}, nil)
		}()
		waitForPendingRequest(t, backend, "1")
		cancel()
		err := <-errCh
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("call() error = %v, want context.Canceled", err)
		}
	})

	t.Run("backend stopped", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.call(context.Background(), "x/method", nil, nil)
		}()
		waitForPendingRequest(t, backend, "1")
		close(backend.done)
		err := <-errCh
		if err == nil || err.Error() != errBackendStopped {
			t.Fatalf("call() error = %v, want %q", err, errBackendStopped)
		}
	})
}

func TestHandleIncomingLine(t *testing.T) {
	t.Run("blank line", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		if err := backend.handleIncomingLine([]byte(" \n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		if err := backend.handleIncomingLine([]byte("{oops\n")); err == nil {
			t.Fatal("handleIncomingLine() error = nil, want non-nil")
		}
	})

	t.Run("request event", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		err := backend.handleIncomingLine([]byte(`{"id":1,"method":"ask","params":{"k":"v"}}` + "\n"))
		if err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		ev := <-backend.events
		if ev.Request == nil || ev.Request.Method != "ask" {
			t.Fatalf("request event = %#v, want method ask", ev.Request)
		}
	})

	t.Run("notification event", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		err := backend.handleIncomingLine([]byte(`{"method":"note","params":{"x":1}}` + "\n"))
		if err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		ev := <-backend.events
		if ev.Notification == nil || ev.Notification.Method != "note" {
			t.Fatalf("notification event = %#v, want method note", ev.Notification)
		}
	})

	t.Run("response without pending request", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		if err := backend.handleIncomingLine([]byte(`{"id":1,"result":{"ok":true}}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
	})

	t.Run("drops request event after backend stop", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		close(backend.done)
		if err := backend.handleIncomingLine([]byte(`{"id":1,"method":"ask","params":{"k":"v"}}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		select {
		case ev := <-backend.events:
			t.Fatalf("unexpected event after backend stop: %#v", ev)
		default:
		}
	})
}

func TestEmitEventDropsWhenShutdownStarts(t *testing.T) {
	backend := newTestBackend(&captureWriteCloser{})
	backend.events = make(chan appServerEvent, 1)
	backend.events <- appServerEvent{
		Notification: &appServerNotification{Method: "busy"},
	}

	done := make(chan struct{})
	go func() {
		backend.emitEvent(appServerEvent{
			Notification: &appServerNotification{Method: "late"},
		})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("emitEvent returned before shutdown while channel was full")
	case <-time.After(20 * time.Millisecond):
	}

	close(backend.closing)

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("emitEvent blocked after shutdown started")
	}
}

func TestTurnInterrupt(t *testing.T) {
	t.Run("empty ids is no-op", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		if err := backend.TurnInterrupt(context.Background(), " ", "\t"); err != nil {
			t.Fatalf("TurnInterrupt() error = %v", err)
		}
		if got := len(writer.writesSnapshot()); got != 0 {
			t.Fatalf("writes = %d, want 0", got)
		}
	})

	t.Run("sends request when ids present", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		errCh := make(chan error, 1)
		go func() {
			errCh <- backend.TurnInterrupt(context.Background(), "thr-1", "turn-1")
		}()
		waitForPendingRequest(t, backend, "1")
		if err := backend.handleIncomingLine([]byte(`{"id":1,"result":null}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("TurnInterrupt() error = %v", err)
		}
		payload := parseFirstWriteAsJSONMap(t, writer)
		if got, want := payload["method"], "turn/interrupt"; got != want {
			t.Fatalf("method = %#v, want %q", got, want)
		}
	})
}

func TestThreadUnsubscribe(t *testing.T) {
	t.Run("empty thread id is no-op", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		resp, err := backend.ThreadUnsubscribe(context.Background(), " ")
		if err != nil {
			t.Fatalf("ThreadUnsubscribe() error = %v", err)
		}
		if resp.Status != "" {
			t.Fatalf("ThreadUnsubscribe() status = %q, want empty", resp.Status)
		}
		if got := len(writer.writesSnapshot()); got != 0 {
			t.Fatalf("writes = %d, want 0", got)
		}
	})

	t.Run("sends request when thread id present", func(t *testing.T) {
		writer := &captureWriteCloser{}
		backend := newTestBackend(writer)
		errCh := make(chan error, 1)
		var resp appServerThreadUnsubscribeResponse
		go func() {
			var err error
			resp, err = backend.ThreadUnsubscribe(context.Background(), "thr-1")
			errCh <- err
		}()
		waitForPendingRequest(t, backend, "1")
		if err := backend.handleIncomingLine([]byte(`{"id":1,"result":{"status":"unsubscribed"}}` + "\n")); err != nil {
			t.Fatalf("handleIncomingLine() error = %v", err)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("ThreadUnsubscribe() error = %v", err)
		}
		if got, want := resp.Status, "unsubscribed"; got != want {
			t.Fatalf("ThreadUnsubscribe() status = %q, want %q", got, want)
		}
		payload := parseFirstWriteAsJSONMap(t, writer)
		if got, want := payload["method"], "thread/unsubscribe"; got != want {
			t.Fatalf("method = %#v, want %q", got, want)
		}
		params, ok := payload["params"].(map[string]any)
		if !ok {
			t.Fatalf("params type = %T, want map[string]any", payload["params"])
		}
		if got, want := params["threadId"], "thr-1"; got != want {
			t.Fatalf("threadId = %#v, want %q", got, want)
		}
	})
}

func TestRespondRequestAndErrorValidation(t *testing.T) {
	backend := newTestBackend(&captureWriteCloser{})
	if err := backend.RespondRequest(context.Background(), nil, nil); err == nil || err.Error() != errRequestIDRequired {
		t.Fatalf("RespondRequest(nil) error = %v, want %q", err, errRequestIDRequired)
	}
	if err := backend.RespondRequestError(context.Background(), nil, -1, "bad", nil); err == nil || err.Error() != errRequestIDRequired {
		t.Fatalf("RespondRequestError(nil) error = %v, want %q", err, errRequestIDRequired)
	}
}

func TestFailPending(t *testing.T) {
	t.Run("generic stop error", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		ch := make(chan appServerRPCResponse, 1)
		backend.pending["1"] = ch
		backend.failPending(nil)
		resp := <-ch
		if resp.err == nil || resp.err.Error() != errBackendStopped {
			t.Fatalf("failPending(nil) err = %v, want %q", resp.err, errBackendStopped)
		}
	})

	t.Run("exit error wraps wait error", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		ch := make(chan appServerRPCResponse, 1)
		backend.pending["1"] = ch
		backend.failPending(errors.New("boom"))
		resp := <-ch
		if resp.err == nil || !strings.Contains(resp.err.Error(), "bridge backend exited: boom") {
			t.Fatalf("failPending(err) = %v, want wrapped error", resp.err)
		}
	})

	t.Run("process done uses generic stop error", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		ch := make(chan appServerRPCResponse, 1)
		backend.pending["1"] = ch
		backend.failPending(os.ErrProcessDone)
		resp := <-ch
		if resp.err == nil || resp.err.Error() != errBackendStopped {
			t.Fatalf("failPending(os.ErrProcessDone) err = %v, want %q", resp.err, errBackendStopped)
		}
	})
}

func TestWait(t *testing.T) {
	t.Run("nil wait err", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		close(backend.done)
		if err := backend.Wait(); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	})

	t.Run("process done wait err", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		backend.waitErr = os.ErrProcessDone
		close(backend.done)
		if err := backend.Wait(); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	})

	t.Run("returns wait err", func(t *testing.T) {
		backend := newTestBackend(&captureWriteCloser{})
		wantErr := errors.New("wait failed")
		backend.waitErr = wantErr
		close(backend.done)
		if err := backend.Wait(); !errors.Is(err, wantErr) {
			t.Fatalf("Wait() error = %v, want %v", err, wantErr)
		}
	})
}

func TestClose(t *testing.T) {
	t.Run("close error returned", func(t *testing.T) {
		writer := &captureWriteCloser{closeErr: errors.New("close failed")}
		backend := newTestBackend(writer)
		backend.cmd = &exec.Cmd{}
		if err := backend.Close(); err == nil || err.Error() != "close failed" {
			t.Fatalf("Close() error = %v, want %q", err, "close failed")
		}
	})

	t.Run("os.ErrClosed ignored", func(t *testing.T) {
		writer := &captureWriteCloser{closeErr: os.ErrClosed}
		backend := newTestBackend(writer)
		backend.cmd = &exec.Cmd{}
		if err := backend.Close(); err != nil {
			t.Fatalf("Close() error = %v, want nil", err)
		}
	})

	t.Run("sends interrupt before waiting for exit", func(t *testing.T) {
		signalFile := filepath.Join(t.TempDir(), "signal.txt")
		backend := newHelperBackend(t, "exit-on-int", signalFile)

		closeDone := make(chan error, 1)
		go func() {
			closeDone <- backend.Close()
		}()

		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close() timed out")
		}

		if err := backend.Wait(); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
		got, err := os.ReadFile(signalFile)
		if err != nil {
			t.Fatalf("ReadFile(%q) error = %v", signalFile, err)
		}
		if strings.TrimSpace(string(got)) != os.Interrupt.String() {
			t.Fatalf("signal marker = %q, want %q", strings.TrimSpace(string(got)), os.Interrupt.String())
		}
	})

	t.Run("kills process after grace period when interrupt is ignored", func(t *testing.T) {
		backend := newHelperBackend(t, "ignore-int", "")
		backend.shutdownGracePeriod = 20 * time.Millisecond

		start := time.Now()
		if err := backend.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if err := backend.Wait(); err == nil {
			t.Fatal("Wait() error = nil, want non-nil after forced kill")
		}
		if elapsed := time.Since(start); elapsed < backend.shutdownGracePeriod {
			t.Fatalf("Close()/Wait elapsed = %v, want at least %v", elapsed, backend.shutdownGracePeriod)
		}
	})
}

func TestReadLoopSuppressesExpectedCloseError(t *testing.T) {
	var logs bytes.Buffer
	logger := zerolog.New(&logs)
	backend := newTestBackend(&captureWriteCloser{})
	backend.logger = &logger

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	defer func() { _ = writer.Close() }()

	done := make(chan struct{})
	go func() {
		backend.readLoop(reader)
		close(done)
	}()

	close(backend.closing)
	if err := reader.Close(); err != nil {
		t.Fatalf("reader.Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("readLoop did not stop")
	}

	if strings.Contains(logs.String(), "bridge backend read loop stopped") {
		t.Fatalf("readLoop logged expected shutdown noise: %q", logs.String())
	}
}

func newHelperBackend(t *testing.T, mode string, signalFile string) *appServerBackend {
	t.Helper()

	readyFile := filepath.Join(t.TempDir(), "ready.txt")
	cmd := exec.Command(os.Args[0], "-test.run=TestBackendHelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_APP_SERVER_BACKEND_HELPER=1",
		"BACKEND_HELPER_MODE="+mode,
		"BACKEND_HELPER_READY_FILE="+readyFile,
		"BACKEND_HELPER_SIGNAL_FILE="+signalFile,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	backend := newTestBackend(stdin)
	backend.cmd = cmd
	close(backend.readDone)
	go backend.waitLoop()
	waitForFile(t, readyFile)
	return backend
}

func TestBackendHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_APP_SERVER_BACKEND_HELPER") != "1" {
		return
	}

	mode := os.Getenv("BACKEND_HELPER_MODE")
	readyFile := os.Getenv("BACKEND_HELPER_READY_FILE")
	signalFile := os.Getenv("BACKEND_HELPER_SIGNAL_FILE")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	if strings.TrimSpace(readyFile) != "" {
		if err := os.WriteFile(readyFile, []byte("ready"), 0o644); err != nil {
			signal.Stop(signals)
			os.Exit(4)
		}
	}

	writeSignal := func(sig os.Signal) {
		if strings.TrimSpace(signalFile) == "" {
			return
		}
		if err := os.WriteFile(signalFile, []byte(sig.String()), 0o644); err != nil {
			os.Exit(2)
		}
	}

	switch mode {
	case "exit-on-int":
		sig := <-signals
		writeSignal(sig)
		signal.Stop(signals)
		os.Exit(0)
	case "ignore-int":
		for range signals {
			writeSignal(os.Interrupt)
		}
	default:
		signal.Stop(signals)
		os.Exit(3)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %q was not created before timeout", path)
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
