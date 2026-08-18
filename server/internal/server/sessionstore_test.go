package server

import (
	"net"
	"path/filepath"
	"testing"
)

func TestSessionStorePersistsBindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	store, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}

	device := map[string]string{
		"client_id":   "client-a",
		"device_name": "Jack's iPhone",
		"model":       "iPhone 15 Pro",
	}
	b1, ok := store.Resolve("client-a", device)
	if !ok || b1.SessionID != 1 || b1.Resumed {
		t.Fatalf("first resolve: %+v ok=%v", b1, ok)
	}
	store.AssignIP("client-a", net.IPv4(10, 8, 0, 42))

	reloaded, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	b2, ok := reloaded.Resolve("client-a", device)
	if !ok || b2.SessionID != 1 || !b2.Resumed {
		t.Fatalf("reloaded resolve: %+v ok=%v", b2, ok)
	}
	row, ok := reloaded.Lookup("client-a")
	if !ok || row.ClientIP != "10.8.0.42" {
		t.Fatalf("lookup: %+v ok=%v", row, ok)
	}
	ips := reloaded.ReservedIPs()
	if len(ips) != 1 || !ips[0].Equal(net.IPv4(10, 8, 0, 42)) {
		t.Fatalf("reserved ips: %+v", ips)
	}

	reloaded.Reset()
	if len(reloaded.KnownSessions()) != 0 {
		t.Fatalf("expected empty store after reset")
	}
	b3, ok := reloaded.Resolve("client-a", device)
	if !ok || b3.SessionID != 1 || b3.Resumed {
		t.Fatalf("post-reset resolve should start fresh ids: %+v ok=%v", b3, ok)
	}
}

func TestSessionStoreEphemeralIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	store, err := NewSessionStore(path)
	if err != nil {
		t.Fatalf("NewSessionStore: %v", err)
	}
	id1 := store.AllocateEphemeralSessionID()
	id2 := store.AllocateEphemeralSessionID()
	if id1 != 1 || id2 != 2 {
		t.Fatalf("ephemeral ids = %d, %d", id1, id2)
	}
}
