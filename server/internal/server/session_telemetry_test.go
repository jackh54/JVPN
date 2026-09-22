package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jackh54/jvpn-server/internal/protocol"
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

func TestSendPolicyQueuesAControlFrame(t *testing.T) {
	s := &Session{hub: NewHub(), downstream: make(chan []byte, 4)}
	s.SendPolicy(DefaultSchedulePolicy())

	select {
	case payload := <-s.downstream:
		typ, body, ok := protocol.ParseControlFrame(payload)
		if !ok || typ != protocol.CtrlPolicy {
			t.Fatalf("queued payload is not a policy frame: ok=%v typ=%#x", ok, typ)
		}
		var got SchedulePolicy
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("policy body is not valid JSON: %v", err)
		}
		if got.OnTime != "07:30" || got.AutoDisconnect {
			t.Fatalf("unexpected policy on the wire: %+v", got)
		}
	default:
		t.Fatal("SendPolicy queued nothing")
	}
}

func TestSendPolicyAfterCloseDoesNotPanic(t *testing.T) {
	s := &Session{hub: NewHub(), downstream: make(chan []byte, 1)}
	s.closeDownstream()
	s.closeDownstream() // idempotent

	// A policy broadcast (or a TUN packet) racing session teardown must be dropped,
	// not sent on a closed channel.
	s.SendPolicy(DefaultSchedulePolicy())
	if s.enqueueDownstream([]byte{0x45}) {
		t.Fatal("enqueue succeeded after the queue was closed")
	}
}
