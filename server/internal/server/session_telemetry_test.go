package server

import (
	"testing"
	"time"
)

func TestApplyTelemetryDoesNotDeadlock(t *testing.T) {
	h := NewHub()
	s := &Session{hub: h}
	done := make(chan struct{})
	go func() {
		s.applyTelemetry([]byte(`{"client_id":"abc","device_name":"Jack's iPhone","model":"iPhone 15 Pro"}`))
		_ = s.Snapshot(time.Now().UTC())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("applyTelemetry deadlocked")
	}
}
