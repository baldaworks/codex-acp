package appio

import (
	"io"
	"sync"
)

// SyncWriter serializes writes to an underlying writer.
type SyncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

// NewSyncWriter wraps writer with a mutex-protected writer.
func NewSyncWriter(writer io.Writer) *SyncWriter {
	return &SyncWriter{writer: writer}
}

// Write writes p to the wrapped writer while holding a mutex.
func (w *SyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
