package codexacp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

type sharedBackendSpy struct {
	*appServerSessionSpy

	mu          sync.Mutex
	events      chan appServerEvent
	threads     int
	turns       int
	closeEvents sync.Once
}

func newSharedBackendSpy() *sharedBackendSpy {
	return &sharedBackendSpy{
		appServerSessionSpy: &appServerSessionSpy{
			initializeResponse: appServerInitializeResponse{UserAgent: "codex-test/1"},
		},
		events: make(chan appServerEvent, 16),
	}
}

func (s *sharedBackendSpy) Events() <-chan appServerEvent { return s.events }

func (s *sharedBackendSpy) ThreadStart(context.Context, map[string]any) (appServerThreadStartResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads++
	threadID := fmt.Sprintf("thread-%d", s.threads)
	return appServerThreadStartResponse{Thread: appServerThread{ID: threadID, SessionID: threadID}}, nil
}

func (s *sharedBackendSpy) TurnStart(context.Context, map[string]any) (appServerTurnStartResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns++
	resp := appServerTurnStartResponse{}
	resp.Turn.ID = fmt.Sprintf("turn-%d", s.turns)
	return resp, nil
}

func (s *sharedBackendSpy) Close() error {
	s.mu.Lock()
	s.closeCalls++
	s.mu.Unlock()
	s.closeEvents.Do(func() { close(s.events) })
	return nil
}

func (s *sharedBackendSpy) Wait() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitCalls++
	return nil
}

func TestSharedBackendUsesOneProcessAndRoutesIndependentThreads(t *testing.T) {
	backend := newSharedBackendSpy()
	builds := 0
	shared := newSharedAppServerBackend(func(context.Context, string) (appServerSession, error) {
		builds++
		return backend, nil
	})

	first, err := shared.Session(context.Background(), "/first")
	if err != nil {
		t.Fatalf("first Session() error: %v", err)
	}
	second, err := shared.Session(context.Background(), "/second")
	if err != nil {
		t.Fatalf("second Session() error: %v", err)
	}
	if builds != 1 {
		t.Fatalf("backend builds = %d, want 1", builds)
	}

	firstThread, err := first.ThreadStart(context.Background(), nil)
	if err != nil {
		t.Fatalf("first ThreadStart() error: %v", err)
	}
	secondThread, err := second.ThreadStart(context.Background(), nil)
	if err != nil {
		t.Fatalf("second ThreadStart() error: %v", err)
	}
	if firstThread.Thread.ID == secondThread.Thread.ID {
		t.Fatalf("thread ids = %q and %q, want independent threads", firstThread.Thread.ID, secondThread.Thread.ID)
	}

	firstItem := appServerTestNotification(t, methodItemStarted, map[string]any{
		"threadId": firstThread.Thread.ID,
		"turnId":   "turn-first",
		"item":     map[string]any{"id": "item-first", "type": "agentMessage"},
	})
	secondItem := appServerTestNotification(t, methodItemStarted, map[string]any{
		"threadId": secondThread.Thread.ID,
		"turnId":   "turn-second",
		"item":     map[string]any{"id": "item-second", "type": "agentMessage"},
	})
	backend.events <- firstItem
	backend.events <- secondItem

	assertSharedEvent(t, first.Events(), firstThread.Thread.ID, "item-first")
	assertSharedEvent(t, second.Events(), secondThread.Thread.ID, "item-second")

	requestRaw, err := json.Marshal(map[string]any{"itemId": "item-first"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	backend.events <- appServerEvent{Request: &appServerRequest{ID: json.RawMessage("1"), Method: "approval", Params: requestRaw}}
	select {
	case event := <-first.Events():
		if event.Request == nil || event.Request.Method != "approval" {
			t.Fatalf("first request event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("item-correlated request was not routed to first session")
	}
	select {
	case event := <-second.Events():
		t.Fatalf("second session received first request: %#v", event)
	case <-time.After(20 * time.Millisecond):
	}

	backend.events <- appServerTestNotification(t, methodTurnCompleted, map[string]any{
		"threadId": firstThread.Thread.ID,
		"turnId":   "turn-first",
		"turn":     map[string]any{"id": "turn-first", "status": "completed"},
	})
	assertSharedEvent(t, first.Events(), firstThread.Thread.ID, "")
	shared.mu.Lock()
	_, keepsTurn := shared.byTurnID["turn-first"]
	_, keepsItem := shared.byItemID["item-first"]
	shared.mu.Unlock()
	if keepsTurn || keepsItem {
		t.Fatalf("completed turn correlation retained: turn=%t item=%t", keepsTurn, keepsItem)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if backend.closeCalls != 0 || backend.waitCalls != 0 {
		t.Fatalf("physical backend close/wait = (%d,%d), want (0,0) after session close", backend.closeCalls, backend.waitCalls)
	}
	if err := shared.Close(); err != nil {
		t.Fatalf("shared Close() error: %v", err)
	}
	if backend.closeCalls != 1 || backend.waitCalls != 1 {
		t.Fatalf("physical backend close/wait = (%d,%d), want (1,1)", backend.closeCalls, backend.waitCalls)
	}
}

func appServerTestNotification(t *testing.T, method string, params map[string]any) appServerEvent {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return appServerEvent{Notification: &appServerNotification{Method: method, Params: raw}}
}

func assertSharedEvent(t *testing.T, events <-chan appServerEvent, wantThreadID, wantItemID string) {
	t.Helper()
	select {
	case event := <-events:
		threadID, _, itemID := appServerEventIDs(event)
		if threadID != wantThreadID || itemID != wantItemID {
			t.Fatalf("event correlation = (%q,%q), want (%q,%q)", threadID, itemID, wantThreadID, wantItemID)
		}
	case <-time.After(time.Second):
		t.Fatalf("event for thread %q was not routed", wantThreadID)
	}
}
