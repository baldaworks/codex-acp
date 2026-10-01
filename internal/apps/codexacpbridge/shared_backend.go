package codexacp

import (
	"context"
	"errors"
	"sync"
)

// sharedAppServerBackend owns one app-server process and exposes isolated
// per-ACP-session views over it. Codex app-server already multiplexes threads;
// starting one process per ACP session wastes resources and leaks processes in
// clients that intentionally create short-lived sessions.
type sharedAppServerBackend struct {
	factory appServerBackendFactory

	mu         sync.Mutex
	backend    appServerSession
	starting   chan struct{}
	closed     bool
	views      map[*sharedAppServerSession]struct{}
	byThreadID map[string]*sharedAppServerSession
	byTurnID   map[string]*sharedAppServerSession
	byItemID   map[string]*sharedAppServerSession
}

type sharedAppServerSession struct {
	owner    *sharedAppServerBackend
	backend  appServerSession
	incoming chan appServerEvent
	events   chan appServerEvent
	done     chan struct{}

	closeOnce sync.Once
	threadID  string
	turnIDs   map[string]struct{}
	itemIDs   map[string]struct{}
	active    bool
}

func newSharedAppServerBackend(factory appServerBackendFactory) *sharedAppServerBackend {
	return &sharedAppServerBackend{
		factory:    factory,
		views:      make(map[*sharedAppServerSession]struct{}),
		byThreadID: make(map[string]*sharedAppServerSession),
		byTurnID:   make(map[string]*sharedAppServerSession),
		byItemID:   make(map[string]*sharedAppServerSession),
	}
}

func (b *sharedAppServerBackend) Session(ctx context.Context, cwd string) (appServerSession, error) {
	backend, err := b.ensureBackend(ctx, cwd)
	if err != nil {
		return nil, err
	}
	view := &sharedAppServerSession{
		owner:    b,
		backend:  backend,
		incoming: make(chan appServerEvent, 256),
		events:   make(chan appServerEvent, 256),
		done:     make(chan struct{}),
		turnIDs:  make(map[string]struct{}),
		itemIDs:  make(map[string]struct{}),
	}
	b.mu.Lock()
	if b.closed || b.backend != backend {
		b.mu.Unlock()
		return nil, errors.New("shared bridge backend stopped")
	}
	b.views[view] = struct{}{}
	b.mu.Unlock()
	go view.dispatch()
	return view, nil
}

func (b *sharedAppServerBackend) ensureBackend(ctx context.Context, cwd string) (appServerSession, error) {
	for {
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return nil, errors.New("shared bridge backend stopped")
		}
		if b.backend != nil {
			backend := b.backend
			b.mu.Unlock()
			return backend, nil
		}
		if b.starting != nil {
			started := b.starting
			b.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-started:
				continue
			}
		}
		started := make(chan struct{})
		b.starting = started
		b.mu.Unlock()

		backend, err := b.factory(ctx, cwd)
		if err == nil && backend == nil {
			err = errors.New("shared bridge backend factory returned nil")
		}

		b.mu.Lock()
		b.starting = nil
		if err == nil && !b.closed {
			b.backend = backend
			go b.dispatch(backend)
		}
		close(started)
		closed := b.closed
		b.mu.Unlock()

		if err != nil {
			return nil, err
		}
		if closed {
			_ = backend.Close()
			_ = backend.Wait()
			return nil, errors.New("shared bridge backend stopped")
		}
		return backend, nil
	}
}

func (b *sharedAppServerBackend) dispatch(backend appServerSession) {
	for event := range backend.Events() {
		b.dispatchEvent(backend, event)
	}
	b.mu.Lock()
	if b.backend != backend {
		b.mu.Unlock()
		return
	}
	b.backend = nil
	views := make([]*sharedAppServerSession, 0, len(b.views))
	for view := range b.views {
		if view.backend == backend {
			views = append(views, view)
			b.unregisterViewLocked(view)
		}
	}
	b.mu.Unlock()
	for _, view := range views {
		view.closeEvents()
	}
}

func (b *sharedAppServerBackend) dispatchEvent(backend appServerSession, event appServerEvent) {
	threadID, turnID, itemID := appServerEventIDs(event)
	b.mu.Lock()
	if b.backend != backend {
		b.mu.Unlock()
		return
	}
	view := b.byThreadID[threadID]
	if view == nil {
		view = b.byTurnID[turnID]
	}
	if view == nil {
		view = b.byItemID[itemID]
	}
	if view == nil {
		view = b.onlyActiveViewLocked()
	}
	if view == nil && len(b.views) == 1 {
		for candidate := range b.views {
			view = candidate
		}
	}
	if view != nil {
		b.registerEventIDsLocked(view, threadID, turnID, itemID)
		if event.Notification != nil && event.Notification.Method == methodTurnCompleted {
			b.completeTurnLocked(view)
		}
	}
	b.mu.Unlock()
	if view != nil {
		view.sendEvent(event)
	}
}

func appServerEventIDs(event appServerEvent) (threadID string, turnID string, itemID string) {
	var raw []byte
	switch {
	case event.Notification != nil:
		raw = event.Notification.Params
	case event.Request != nil:
		raw = event.Request.Params
	default:
		return "", "", ""
	}
	params, err := decodeJSONMap(raw)
	if err != nil {
		return "", "", ""
	}
	threadID = stringValue(params, "threadId")
	if threadID == "" {
		threadID = stringValue(params, "conversationId")
	}
	if threadID == "" {
		threadID = stringValue(mapValue(params, "thread"), "id")
	}
	turnID = stringValue(params, "turnId")
	if turnID == "" {
		turnID = stringValue(mapValue(params, "turn"), "id")
	}
	itemID = stringValue(params, "itemId")
	if itemID == "" {
		itemID = stringValue(mapValue(params, "item"), "id")
	}
	return threadID, turnID, itemID
}

func (b *sharedAppServerBackend) onlyActiveViewLocked() *sharedAppServerSession {
	var active *sharedAppServerSession
	for view := range b.views {
		if !view.active {
			continue
		}
		if active != nil {
			return nil
		}
		active = view
	}
	return active
}

func (b *sharedAppServerBackend) registerThread(view *sharedAppServerSession, threadID string) {
	if threadID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.views[view]; !ok {
		return
	}
	if view.threadID != "" {
		delete(b.byThreadID, view.threadID)
	}
	view.threadID = threadID
	b.byThreadID[threadID] = view
}

func (b *sharedAppServerBackend) registerTurn(view *sharedAppServerSession, turnID string) {
	if turnID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.views[view]; !ok {
		return
	}
	view.turnIDs[turnID] = struct{}{}
	b.byTurnID[turnID] = view
}

func (b *sharedAppServerBackend) setActive(view *sharedAppServerSession, active bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.views[view]; ok {
		view.active = active
	}
}

func (b *sharedAppServerBackend) registerEventIDsLocked(view *sharedAppServerSession, threadID, turnID, itemID string) {
	if threadID != "" {
		b.byThreadID[threadID] = view
		view.threadID = threadID
	}
	if turnID != "" {
		b.byTurnID[turnID] = view
		view.turnIDs[turnID] = struct{}{}
	}
	if itemID != "" {
		b.byItemID[itemID] = view
		view.itemIDs[itemID] = struct{}{}
	}
}

func (b *sharedAppServerBackend) completeTurnLocked(view *sharedAppServerSession) {
	view.active = false
	for id := range view.turnIDs {
		if b.byTurnID[id] == view {
			delete(b.byTurnID, id)
		}
	}
	for id := range view.itemIDs {
		if b.byItemID[id] == view {
			delete(b.byItemID, id)
		}
	}
	view.turnIDs = make(map[string]struct{})
	view.itemIDs = make(map[string]struct{})
}

func (b *sharedAppServerBackend) unregisterView(view *sharedAppServerSession) {
	b.mu.Lock()
	b.unregisterViewLocked(view)
	b.mu.Unlock()
}

func (b *sharedAppServerBackend) unregisterViewLocked(view *sharedAppServerSession) {
	delete(b.views, view)
	if view.threadID != "" && b.byThreadID[view.threadID] == view {
		delete(b.byThreadID, view.threadID)
	}
	for id := range view.turnIDs {
		if b.byTurnID[id] == view {
			delete(b.byTurnID, id)
		}
	}
	for id := range view.itemIDs {
		if b.byItemID[id] == view {
			delete(b.byItemID, id)
		}
	}
}

func (b *sharedAppServerBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	backend := b.backend
	b.backend = nil
	views := make([]*sharedAppServerSession, 0, len(b.views))
	for view := range b.views {
		views = append(views, view)
		b.unregisterViewLocked(view)
	}
	b.mu.Unlock()
	for _, view := range views {
		view.closeEvents()
	}
	if backend == nil {
		return nil
	}
	return errors.Join(backend.Close(), backend.Wait())
}

func (s *sharedAppServerSession) InitializeResponse() appServerInitializeResponse {
	return s.backend.InitializeResponse()
}

func (s *sharedAppServerSession) Events() <-chan appServerEvent { return s.events }

func (s *sharedAppServerSession) ThreadList(ctx context.Context, params map[string]any) (appServerThreadListResponse, error) {
	return s.backend.ThreadList(ctx, params)
}

func (s *sharedAppServerSession) ThreadUnsubscribe(ctx context.Context, threadID string) (appServerThreadUnsubscribeResponse, error) {
	return s.backend.ThreadUnsubscribe(ctx, threadID)
}

func (s *sharedAppServerSession) ThreadStart(ctx context.Context, params map[string]any) (appServerThreadStartResponse, error) {
	resp, err := s.backend.ThreadStart(ctx, params)
	if err == nil {
		s.owner.registerThread(s, resp.Thread.ID)
	}
	return resp, err
}

func (s *sharedAppServerSession) ThreadResume(ctx context.Context, params map[string]any) (appServerThreadResumeResponse, error) {
	resp, err := s.backend.ThreadResume(ctx, params)
	if err == nil {
		s.owner.registerThread(s, resp.Thread.ID)
	}
	return resp, err
}

func (s *sharedAppServerSession) ThreadSettingsUpdate(ctx context.Context, params map[string]any) error {
	return s.backend.ThreadSettingsUpdate(ctx, params)
}

func (s *sharedAppServerSession) TurnStart(ctx context.Context, params map[string]any) (appServerTurnStartResponse, error) {
	s.owner.setActive(s, true)
	resp, err := s.backend.TurnStart(ctx, params)
	if err == nil {
		s.owner.registerTurn(s, resp.Turn.ID)
	} else {
		s.owner.setActive(s, false)
	}
	return resp, err
}

func (s *sharedAppServerSession) ModelList(ctx context.Context, params map[string]any) (appServerModelListResponse, error) {
	return s.backend.ModelList(ctx, params)
}

func (s *sharedAppServerSession) TurnInterrupt(ctx context.Context, threadID string, turnID string) error {
	return s.backend.TurnInterrupt(ctx, threadID, turnID)
}

func (s *sharedAppServerSession) RespondRequest(ctx context.Context, req *appServerRequest, result any) error {
	return s.backend.RespondRequest(ctx, req, result)
}

func (s *sharedAppServerSession) RespondRequestError(ctx context.Context, req *appServerRequest, code int, message string, data any) error {
	return s.backend.RespondRequestError(ctx, req, code, message, data)
}

func (s *sharedAppServerSession) Close() error {
	s.owner.unregisterView(s)
	s.closeEvents()
	return nil
}

func (s *sharedAppServerSession) Wait() error { return nil }

func (s *sharedAppServerSession) sendEvent(event appServerEvent) {
	select {
	case <-s.done:
	case s.incoming <- event:
	}
}

func (s *sharedAppServerSession) closeEvents() {
	s.closeOnce.Do(func() { close(s.done) })
}

func (s *sharedAppServerSession) dispatch() {
	defer close(s.events)
	for {
		select {
		case <-s.done:
			return
		case event := <-s.incoming:
			select {
			case <-s.done:
				return
			case s.events <- event:
			}
		}
	}
}
