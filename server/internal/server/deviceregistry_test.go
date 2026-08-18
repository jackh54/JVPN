package server

import (
	"path/filepath"
	"testing"
)

func TestDeviceRegistryPendingAndUpsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	reg, err := NewDeviceRegistry(path)
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	if _, err := reg.RegisterPending("Jack", "personal phone"); err != nil {
		t.Fatalf("RegisterPending: %v", err)
	}
	pending := reg.ListViews(nil)
	if len(pending) != 1 || pending[0].Label != "Jack" || pending[0].ClientID != "" {
		t.Fatalf("pending device: %+v", pending)
	}

	reg.TouchClient("abc123", "Jack's iPhone", "iPhone 15 Pro")
	online := map[string]uint64{"abc123": 7}
	views := reg.ListViews(online)
	if len(views) != 1 {
		t.Fatalf("expected 1 registered client, got %d: %+v", len(views), views)
	}
	if views[0].Label != "Jack" || !views[0].Online || views[0].SessionID != 7 {
		t.Fatalf("registered view: %+v", views[0])
	}

	if _, err := reg.UpsertClient("abc123", "Jack Harris", "work"); err != nil {
		t.Fatalf("UpsertClient: %v", err)
	}
	if got := reg.LabelForClient("abc123"); got != "Jack Harris" {
		t.Fatalf("label = %q", got)
	}

	reg2, err := NewDeviceRegistry(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reg2.LabelForClient("abc123"); got != "Jack Harris" {
		t.Fatalf("persisted label = %q", got)
	}
}
