package dashboard

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackh54/jvpn-server/internal/server"
	"github.com/jackh54/jvpn-server/internal/session"
)

const (
	testUser = "admin"
	testPass = "hunter2"
)

// startDashboard runs the real dashboard on an ephemeral port and returns its base URL.
func startDashboard(t *testing.T) (string, *server.Hub) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	hub := server.NewHub()
	sched, err := server.NewScheduleStore(t.TempDir() + "/schedule.json")
	if err != nil {
		t.Fatalf("schedule store: %v", err)
	}
	hub.SetScheduleStore(sched)

	go func() {
		_ = Start(addr, testUser, testPass, hub, session.NewIPPool())
	}()

	base := "http://" + addr
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/healthz"); err == nil {
			_ = resp.Body.Close()
			return base, hub
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("dashboard did not come up")
	return "", nil
}

func do(t *testing.T, method, url, body string, auth bool) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if auth {
		req.SetBasicAuth(testUser, testPass)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	return resp
}

func decodeSchedule(t *testing.T, resp *http.Response, key string) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if key == "" {
		return raw
	}
	nested, ok := raw[key].(map[string]any)
	if !ok {
		t.Fatalf("response has no %q object: %v", key, raw)
	}
	return nested
}

func TestScheduleAPIRequiresAuth(t *testing.T) {
	base, _ := startDashboard(t)
	resp := do(t, http.MethodGet, base+"/api/schedule", "", false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", resp.StatusCode)
	}
}

func TestScheduleAPIDefaultsAndUpdate(t *testing.T) {
	base, hub := startDashboard(t)

	got := decodeSchedule(t, do(t, http.MethodGet, base+"/api/schedule", "", true), "")
	if got["on_time"] != "07:30" {
		t.Fatalf("default on_time = %v", got["on_time"])
	}
	if got["auto_connect"] != true {
		t.Fatalf("auto-connect should default on, got %v", got["auto_connect"])
	}
	if got["auto_disconnect"] != false {
		t.Fatalf("auto-disconnect should default off, got %v", got["auto_disconnect"])
	}
	if _, present := got["next_off_at"]; present {
		t.Fatal("next_off_at must be absent while auto-disconnect is off")
	}
	if _, present := got["next_on_at"]; !present {
		t.Fatal("next_on_at should be resolved while auto-connect is on")
	}

	body := `{"auto_disconnect":true,"off_time":"15:00","days":[1,2,3,4,5],"notify_off":true}`
	updated := decodeSchedule(t, do(t, http.MethodPost, base+"/api/schedule", body, true), "schedule")
	if updated["auto_disconnect"] != true || updated["off_time"] != "15:00" {
		t.Fatalf("update not applied: %v", updated)
	}
	if updated["on_time"] != "07:30" {
		t.Fatalf("on_time should be untouched, got %v", updated["on_time"])
	}
	if _, present := updated["next_off_at"]; !present {
		t.Fatal("next_off_at should be resolved once auto-disconnect is on")
	}

	// The hub's store is the one the tunnel broadcasts from.
	if p := hub.ScheduleStore().Get(); !p.AutoDisconnect || p.OffTime != "15:00" || len(p.Days) != 5 {
		t.Fatalf("hub store out of sync: %+v", p)
	}

	// And a re-read returns the persisted value, not the default.
	reread := decodeSchedule(t, do(t, http.MethodGet, base+"/api/schedule", "", true), "")
	if reread["off_time"] != "15:00" {
		t.Fatalf("re-read lost the update: %v", reread)
	}
}

func TestScheduleAPIRejectsInvalidInput(t *testing.T) {
	base, hub := startDashboard(t)

	for _, body := range []string{
		`{"on_time":"99:99"}`,
		`{"timezone":"Mars/Olympus"}`,
		`{"days":[0,42]}`,
		`not json`,
	} {
		resp := do(t, http.MethodPost, base+"/api/schedule", body, true)
		code := resp.StatusCode
		_ = resp.Body.Close()
		if code != http.StatusBadRequest {
			t.Fatalf("body %q: expected 400, got %d", body, code)
		}
	}
	if p := hub.ScheduleStore().Get(); p.OnTime != "07:30" || p.Timezone != server.DefaultScheduleTimezone {
		t.Fatalf("a rejected update leaked into the policy: %+v", p)
	}
}

func TestScheduleAPIRejectsOtherMethods(t *testing.T) {
	base, _ := startDashboard(t)
	resp := do(t, http.MethodDelete, base+"/api/schedule", "", true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestIndexPageServesSchedulePanel(t *testing.T) {
	base, _ := startDashboard(t)
	resp := do(t, http.MethodGet, base+"/", "", true)
	defer resp.Body.Close()
	buf := make([]byte, 64*1024)
	var page strings.Builder
	for {
		n, err := resp.Body.Read(buf)
		page.Write(buf[:n])
		if err != nil {
			break
		}
	}
	html := page.String()
	for _, needle := range []string{"VPN schedule", "schedAutoConnect", "schedAutoDisconnect", "saveSchedule()"} {
		if !strings.Contains(html, needle) {
			t.Fatalf("dashboard HTML is missing %q", needle)
		}
	}
}
