package server

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultSchedulePolicy(t *testing.T) {
	p := DefaultSchedulePolicy()
	if !p.AutoConnect || p.OnTime != "07:30" {
		t.Fatalf("expected auto-connect at 07:30, got %+v", p)
	}
	if p.AutoDisconnect {
		t.Fatal("auto-disconnect must be opt-in")
	}
	if p.Timezone != "America/Chicago" {
		t.Fatalf("unexpected default timezone %q", p.Timezone)
	}
}

func TestScheduleStoreApplyPersistsAndBumpsRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")
	store, err := NewScheduleStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	base := store.Get()

	on := true
	off := "15:00"
	updated, err := store.Apply(ScheduleUpdate{AutoDisconnect: &on, OffTime: &off})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !updated.AutoDisconnect || updated.OffTime != "15:00" {
		t.Fatalf("update not applied: %+v", updated)
	}
	if updated.Revision != base.Revision+1 {
		t.Fatalf("revision %d did not follow %d", updated.Revision, base.Revision)
	}
	if updated.OnTime != base.OnTime {
		t.Fatalf("untouched field changed: %q -> %q", base.OnTime, updated.OnTime)
	}

	reopened, err := NewScheduleStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.Get(); !got.AutoDisconnect || got.OffTime != "15:00" || got.Revision != updated.Revision {
		t.Fatalf("not persisted: %+v", got)
	}
}

func TestScheduleStoreRejectsBadInput(t *testing.T) {
	store, err := NewScheduleStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	bad := "25:00"
	if _, err := store.Apply(ScheduleUpdate{OnTime: &bad}); err == nil {
		t.Fatal("expected an error for hour 25")
	}
	zone := "Mars/Olympus"
	if _, err := store.Apply(ScheduleUpdate{Timezone: &zone}); err == nil {
		t.Fatal("expected an error for an unknown timezone")
	}
	days := []int{0, 9}
	if _, err := store.Apply(ScheduleUpdate{Days: &days}); err == nil {
		t.Fatal("expected an error for weekday 9")
	}
	if got := store.Get(); got.OnTime != "07:30" || got.Timezone != "America/Chicago" {
		t.Fatalf("rejected update leaked into the policy: %+v", got)
	}
}

func TestScheduleStoreChangeHandlerFires(t *testing.T) {
	store, err := NewScheduleStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	got := make(chan SchedulePolicy, 1)
	store.SetChangeHandler(func(p SchedulePolicy) { got <- p })

	newOn := "08:15"
	if _, err := store.Apply(ScheduleUpdate{OnTime: &newOn}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	select {
	case p := <-got:
		if p.OnTime != "08:15" {
			t.Fatalf("handler saw %q", p.OnTime)
		}
	case <-time.After(time.Second):
		t.Fatal("change handler never fired")
	}
}

func TestNextTransitionSkipsDisabledDays(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	p := DefaultSchedulePolicy()
	p.Days = []int{1, 2, 3, 4, 5} // weekdays only

	// Saturday 09:00 local — the next 07:30 must be Monday.
	sat := time.Date(2026, 9, 19, 9, 0, 0, 0, chicago)
	next, ok := p.NextTransition(sat, p.OnTime)
	if !ok {
		t.Fatal("expected a transition")
	}
	local := next.In(chicago)
	if local.Weekday() != time.Monday || local.Hour() != 7 || local.Minute() != 30 {
		t.Fatalf("expected Monday 07:30, got %s", local)
	}
}

func TestNextTransitionSameDayLaterToday(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	p := DefaultSchedulePolicy()
	morning := time.Date(2026, 9, 22, 6, 0, 0, 0, chicago)
	next, ok := p.NextTransition(morning, p.OnTime)
	if !ok {
		t.Fatal("expected a transition")
	}
	if local := next.In(chicago); local.Day() != 22 || local.Hour() != 7 || local.Minute() != 30 {
		t.Fatalf("expected today 07:30, got %s", local)
	}
}
