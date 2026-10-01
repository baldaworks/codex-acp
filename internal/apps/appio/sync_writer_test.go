package appio

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type serializedProbeWriter struct {
	active     int32
	concurrent int32
	writes     int32
}

func (w *serializedProbeWriter) Write(p []byte) (int, error) {
	if atomic.AddInt32(&w.active, 1) != 1 {
		atomic.StoreInt32(&w.concurrent, 1)
	}
	time.Sleep(200 * time.Microsecond)
	atomic.AddInt32(&w.writes, 1)
	atomic.AddInt32(&w.active, -1)
	return len(p), nil
}

func TestNewSyncWriterWritePassThrough(t *testing.T) {
	var buf bytes.Buffer
	writer := NewSyncWriter(&buf)

	n, err := writer.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len("hello") {
		t.Fatalf("Write() bytes = %d, want %d", n, len("hello"))
	}
	if got := buf.String(); got != "hello" {
		t.Fatalf("buffer = %q, want %q", got, "hello")
	}
}

func TestSyncWriterSerializesConcurrentWrites(t *testing.T) {
	const totalWrites = 32
	probe := &serializedProbeWriter{}
	writer := NewSyncWriter(probe)

	var wg sync.WaitGroup
	wg.Add(totalWrites)
	for i := 0; i < totalWrites; i++ {
		go func() {
			defer wg.Done()
			if _, err := writer.Write([]byte("x")); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got, want := atomic.LoadInt32(&probe.writes), int32(totalWrites); got != want {
		t.Fatalf("writes = %d, want %d", got, want)
	}
	if atomic.LoadInt32(&probe.concurrent) != 0 {
		t.Fatal("underlying writer observed concurrent writes, want serialized writes")
	}
}
