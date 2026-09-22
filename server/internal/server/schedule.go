package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultScheduleTimezone is the IANA zone used when the admin has not picked one.
const DefaultScheduleTimezone = "America/Chicago"

// SchedulePolicy is the VPN on/off schedule managed from the admin dashboard and
// pushed to every connected client as a CtrlPolicy control frame.
//
// Auto-connect is on by default: clients bring the tunnel up at OnTime and keep
// it up (always-on). Auto-disconnect is opt-in — with AutoDisconnect false the
// tunnel is never torn down on a timer.
type SchedulePolicy struct {
	Revision       uint64    `json:"revision"`
	Timezone       string    `json:"timezone"`
	AutoConnect    bool      `json:"auto_connect"`
	OnTime         string    `json:"on_time"` // "HH:MM" in Timezone
	AutoDisconnect bool      `json:"auto_disconnect"`
	OffTime        string    `json:"off_time"` // "HH:MM" in Timezone
	Days           []int     `json:"days"`     // 0=Sunday … 6=Saturday; empty means every day
	NotifyOn       bool      `json:"notify_on"`
	NotifyOff      bool      `json:"notify_off"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// SchedulePolicyView adds resolved next-transition timestamps for the dashboard.
type SchedulePolicyView struct {
	SchedulePolicy
	NextOnAt  *time.Time `json:"next_on_at,omitempty"`
	NextOffAt *time.Time `json:"next_off_at,omitempty"`
}

// DefaultSchedulePolicy matches the product default: on at 07:30 America/Chicago
// every day, never automatically off until an admin enables it.
func DefaultSchedulePolicy() SchedulePolicy {
	return SchedulePolicy{
		Revision:       1,
		Timezone:       DefaultScheduleTimezone,
		AutoConnect:    true,
		OnTime:         "07:30",
		AutoDisconnect: false,
		OffTime:        "15:00",
		Days:           nil,
		NotifyOn:       true,
		NotifyOff:      true,
		UpdatedAt:      time.Now().UTC(),
	}
}

// ScheduleUpdate is the partial document accepted by the admin API. Absent
// fields keep their current value.
type ScheduleUpdate struct {
	Timezone       *string `json:"timezone,omitempty"`
	AutoConnect    *bool   `json:"auto_connect,omitempty"`
	OnTime         *string `json:"on_time,omitempty"`
	AutoDisconnect *bool   `json:"auto_disconnect,omitempty"`
	OffTime        *string `json:"off_time,omitempty"`
	Days           *[]int  `json:"days,omitempty"`
	NotifyOn       *bool   `json:"notify_on,omitempty"`
	NotifyOff      *bool   `json:"notify_off,omitempty"`
}

// ScheduleStore persists the policy to disk and notifies a listener on change.
type ScheduleStore struct {
	mu       sync.RWMutex
	path     string
	policy   SchedulePolicy
	onChange func(SchedulePolicy)
}

func NewScheduleStore(path string) (*ScheduleStore, error) {
	s := &ScheduleStore{path: path, policy: DefaultSchedulePolicy()}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var loaded SchedulePolicy
	if err := json.Unmarshal(b, &loaded); err != nil {
		return nil, err
	}
	normalized, err := normalizeSchedule(loaded)
	if err != nil {
		// A hand-edited file should not stop the server from booting.
		return s, nil
	}
	if normalized.Revision == 0 {
		normalized.Revision = 1
	}
	s.policy = normalized
	return s, nil
}

// SetChangeHandler registers a callback fired (outside the lock) after every
// successful Apply.
func (s *ScheduleStore) SetChangeHandler(fn func(SchedulePolicy)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

func (s *ScheduleStore) Get() SchedulePolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy.clone()
}

// Apply merges an update, validates it, persists it, and bumps the revision.
func (s *ScheduleStore) Apply(u ScheduleUpdate) (SchedulePolicy, error) {
	s.mu.Lock()
	next := s.policy.clone()
	if u.Timezone != nil {
		next.Timezone = *u.Timezone
	}
	if u.AutoConnect != nil {
		next.AutoConnect = *u.AutoConnect
	}
	if u.OnTime != nil {
		next.OnTime = *u.OnTime
	}
	if u.AutoDisconnect != nil {
		next.AutoDisconnect = *u.AutoDisconnect
	}
	if u.OffTime != nil {
		next.OffTime = *u.OffTime
	}
	if u.Days != nil {
		next.Days = append([]int(nil), (*u.Days)...)
	}
	if u.NotifyOn != nil {
		next.NotifyOn = *u.NotifyOn
	}
	if u.NotifyOff != nil {
		next.NotifyOff = *u.NotifyOff
	}
	normalized, err := normalizeSchedule(next)
	if err != nil {
		s.mu.Unlock()
		return SchedulePolicy{}, err
	}
	normalized.Revision = s.policy.Revision + 1
	normalized.UpdatedAt = time.Now().UTC()
	s.policy = normalized
	handler := s.onChange
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return SchedulePolicy{}, err
	}
	out := s.policy.clone()
	s.mu.Unlock()

	if handler != nil {
		handler(out.clone())
	}
	return out, nil
}

func (s *ScheduleStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.policy, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (p SchedulePolicy) clone() SchedulePolicy {
	out := p
	out.Days = append([]int(nil), p.Days...)
	return out
}

// View resolves the next on/off transition for display.
func (p SchedulePolicy) View(now time.Time) SchedulePolicyView {
	v := SchedulePolicyView{SchedulePolicy: p.clone()}
	if p.AutoConnect {
		if t, ok := p.NextTransition(now, p.OnTime); ok {
			v.NextOnAt = &t
		}
	}
	if p.AutoDisconnect {
		if t, ok := p.NextTransition(now, p.OffTime); ok {
			v.NextOffAt = &t
		}
	}
	return v
}

// NextTransition returns the next UTC instant at which "HH:MM" occurs in the
// policy timezone on an enabled weekday.
func (p SchedulePolicy) NextTransition(now time.Time, hhmm string) (time.Time, bool) {
	loc, err := time.LoadLocation(p.timezoneOrDefault())
	if err != nil {
		return time.Time{}, false
	}
	hour, minute, err := parseHHMM(hhmm)
	if err != nil {
		return time.Time{}, false
	}
	local := now.In(loc)
	for i := 0; i < 8; i++ {
		day := local.AddDate(0, 0, i)
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		if !candidate.After(local) {
			continue
		}
		if !p.dayEnabled(int(candidate.Weekday())) {
			continue
		}
		return candidate.UTC(), true
	}
	return time.Time{}, false
}

func (p SchedulePolicy) dayEnabled(weekday int) bool {
	if len(p.Days) == 0 {
		return true
	}
	for _, d := range p.Days {
		if d == weekday {
			return true
		}
	}
	return false
}

func (p SchedulePolicy) timezoneOrDefault() string {
	if strings.TrimSpace(p.Timezone) == "" {
		return DefaultScheduleTimezone
	}
	return strings.TrimSpace(p.Timezone)
}

func normalizeSchedule(p SchedulePolicy) (SchedulePolicy, error) {
	out := p.clone()
	out.Timezone = strings.TrimSpace(out.Timezone)
	if out.Timezone == "" {
		out.Timezone = DefaultScheduleTimezone
	}
	if _, err := time.LoadLocation(out.Timezone); err != nil {
		return SchedulePolicy{}, fmt.Errorf("unknown timezone %q", out.Timezone)
	}
	onTime, err := normalizeHHMM(out.OnTime, "07:30")
	if err != nil {
		return SchedulePolicy{}, fmt.Errorf("on_time: %w", err)
	}
	out.OnTime = onTime
	offTime, err := normalizeHHMM(out.OffTime, "15:00")
	if err != nil {
		return SchedulePolicy{}, fmt.Errorf("off_time: %w", err)
	}
	out.OffTime = offTime

	seen := make(map[int]bool, 7)
	days := make([]int, 0, 7)
	for _, d := range out.Days {
		if d < 0 || d > 6 {
			return SchedulePolicy{}, fmt.Errorf("days: %d is not 0-6", d)
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		days = append(days, d)
	}
	sort.Ints(days)
	if len(days) == 7 || len(days) == 0 {
		out.Days = nil
	} else {
		out.Days = days
	}
	return out, nil
}

func normalizeHHMM(raw, fallback string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	h, m, err := parseHHMM(raw)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

func parseHHMM(raw string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("expected HH:MM, got %q", raw)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour in %q", raw)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute in %q", raw)
	}
	return h, m, nil
}
