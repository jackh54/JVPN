package server

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackh54/jvpn-server/internal/protocol"
)

type Hub struct {
	mu             sync.RWMutex
	sessions       map[uint32]*Session // IPv4 destination (client) -> session for tun->client
	recentClosed   []SessionSnapshot
	deviceRegistry *DeviceRegistry
	sessionStore   *SessionStore
	startedAt      time.Time
	tunReady       atomic.Bool
	nextSessionID  atomic.Uint64
	totalSessions  atomic.Uint64
	authFailures   atomic.Uint64
	totalUpBytes   atomic.Uint64
	totalDownBytes atomic.Uint64
	totalUpPkts    atomic.Uint64
	totalDownPkts  atomic.Uint64
	dnsBySession   map[uint64][]string
	blockedClients map[string]time.Time // client_id -> expiry
	blockedRemotes map[string]time.Time // remote ip:port host part -> expiry
	resumeByClient map[string]resumeEntry
	resumeTokenMap map[string]resumeTokenEntry
}

type resumeEntry struct {
	ip        net.IP
	expiresAt time.Time
}

type resumeTokenEntry struct {
	clientID  string
	expiresAt time.Time
}

type SessionSnapshot struct {
	SessionID         uint64            `json:"session_id"`
	ClientIP          string            `json:"client_ip"`
	RemoteAddr        string            `json:"remote_addr"`
	ClientID          string            `json:"client_id,omitempty"`
	Label             string            `json:"label,omitempty"`
	DisplayName       string            `json:"display_name,omitempty"`
	DeviceName        string            `json:"device_name,omitempty"`
	Model             string            `json:"model,omitempty"`
	OS                string            `json:"os,omitempty"`
	BatteryPct        *float64          `json:"battery_pct,omitempty"`
	Charging          *bool             `json:"charging,omitempty"`
	Lat               *float64          `json:"lat,omitempty"`
	Lon               *float64          `json:"lon,omitempty"`
	TelemetryAt       *time.Time        `json:"telemetry_at,omitempty"`
	DeviceInfo        map[string]string `json:"device_info,omitempty"`
	DNSRecent         []string          `json:"dns_recent,omitempty"`
	ConnectedAt       time.Time         `json:"connected_at"`
	DurationSeconds   int64             `json:"duration_seconds"`
	UpstreamBytes     uint64            `json:"upstream_bytes"`
	DownstreamBytes   uint64            `json:"downstream_bytes"`
	UpstreamPackets   uint64            `json:"upstream_packets"`
	DownstreamPackets uint64            `json:"downstream_packets"`
}

type DashboardSnapshot struct {
	Now              time.Time         `json:"now"`
	UptimeSeconds    int64             `json:"uptime_seconds"`
	ActiveCount      int               `json:"active_count"`
	TotalSessions    uint64            `json:"total_sessions"`
	AuthFailures     uint64            `json:"auth_failures"`
	TotalUpBytes     uint64            `json:"total_up_bytes"`
	TotalDownBytes   uint64            `json:"total_down_bytes"`
	TotalUpPackets   uint64            `json:"total_up_packets"`
	TotalDownPackets uint64            `json:"total_down_packets"`
	GoRoutines       int               `json:"go_routines"`
	MemAllocBytes    uint64            `json:"mem_alloc_bytes"`
	TUNReady         bool              `json:"tun_ready"`
	Active           []SessionSnapshot      `json:"active"`
	RecentClosed     []SessionSnapshot      `json:"recent_closed"`
	RegisteredDevices []RegisteredDeviceView `json:"registered_devices,omitempty"`
}

func NewHub() *Hub {
	return &Hub{
		sessions:       make(map[uint32]*Session),
		dnsBySession:   make(map[uint64][]string),
		blockedClients: make(map[string]time.Time),
		blockedRemotes: make(map[string]time.Time),
		resumeByClient: make(map[string]resumeEntry),
		resumeTokenMap: make(map[string]resumeTokenEntry),
		startedAt:      time.Now().UTC(),
	}
}

func ipKey(ip net.IP) uint32 {
	ip4 := ip.To4()
	if ip4 == nil {
		return 0
	}
	return binary.BigEndian.Uint32(ip4)
}

func (h *Hub) SetSessionStore(s *SessionStore) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessionStore = s
}

func (h *Hub) SessionStore() *SessionStore {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessionStore
}

func (h *Hub) SetDeviceRegistry(r *DeviceRegistry) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deviceRegistry = r
}

func (h *Hub) DeviceRegistry() *DeviceRegistry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.deviceRegistry
}

func (h *Hub) TouchDevice(clientID, deviceName, model string) {
	h.mu.RLock()
	reg := h.deviceRegistry
	h.mu.RUnlock()
	if reg == nil {
		return
	}
	reg.TouchClient(clientID, deviceName, model)
}

func (h *Hub) enrichSnapshot(snap SessionSnapshot, reg *DeviceRegistry) SessionSnapshot {
	label := ""
	if reg != nil {
		label = reg.LabelForClient(snap.ClientID)
	}
	snap.Label = label
	snap.DisplayName = displayNameForSession(snap, label)
	return snap
}

func displayNameForSession(snap SessionSnapshot, label string) string {
	if label != "" {
		parts := []string{label}
		if snap.DeviceName != "" && !strings.EqualFold(snap.DeviceName, label) {
			parts = append(parts, snap.DeviceName)
		}
		if snap.Model != "" {
			parts = append(parts, snap.Model)
		}
		return strings.Join(parts, " · ")
	}
	name := snap.DeviceName
	if name == "" {
		name = snap.Model
	}
	if name == "" {
		name = "Unknown device"
	}
	if snap.Model != "" && snap.DeviceName != "" && snap.Model != snap.DeviceName {
		return name + " · " + snap.Model
	}
	if snap.OS != "" {
		return name + " · " + snap.OS
	}
	return name
}

func (h *Hub) OnlineClientIDs() map[string]uint64 {
	out := make(map[string]uint64)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		if cid := s.clientID(); cid != "" {
			out[cid] = s.id
		}
	}
	return out
}

func (h *Hub) Register(clientIP net.IP, s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[ipKey(clientIP)] = s
	h.totalSessions.Add(1)
}

func (h *Hub) Unregister(s *Session) {
	if s == nil || s.clientIP == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	k := ipKey(s.clientIP)
	if h.sessions[k] != s {
		return
	}
	delete(h.sessions, k)
	snap := s.Snapshot(time.Now().UTC())
	if dns := h.dnsBySession[s.id]; len(dns) > 0 {
		snap.DNSRecent = append([]string(nil), dns...)
	}
	if cid := s.clientID(); cid != "" && s.clientIP != nil {
		h.resumeByClient[cid] = resumeEntry{
			ip:        append(net.IP(nil), s.clientIP.To4()...),
			expiresAt: time.Now().UTC().Add(24 * time.Hour),
		}
	}
	delete(h.dnsBySession, s.id)
	h.recentClosed = append([]SessionSnapshot{snap}, h.recentClosed...)
	if len(h.recentClosed) > 200 {
		h.recentClosed = h.recentClosed[:200]
	}
}

// DispatchToClient sends a packet read from TUN toward the VPN client (dst must be client tunnel IP).
func (h *Hub) DispatchToClient(packet []byte) {
	if len(packet) < 20 {
		return
	}
	if packet[0]>>4 != 4 {
		return
	}
	dst := net.IP(packet[16:20])
	k := ipKey(dst)
	h.mu.RLock()
	s := h.sessions[k]
	h.mu.RUnlock()
	if s == nil {
		return
	}
	pkt := append([]byte(nil), packet...)
	select {
	case s.downstream <- pkt:
		n := uint64(len(pkt))
		s.downstreamBytes.Add(n)
		s.downstreamPkts.Add(1)
		h.AddDownstream(n)
	default:
		// drop if client is slow
	}
}

func (h *Hub) RunTUNReader(ifce tunReader) {
	buf := make([]byte, protocol.MaxFrameLen)
	for {
		n, err := ifce.Read(buf)
		if err != nil {
			return
		}
		if n <= 0 {
			continue
		}
		h.DispatchToClient(buf[:n])
	}
}

type tunReader interface {
	Read([]byte) (int, error)
}

func (h *Hub) NextSessionID() uint64 {
	if store := h.SessionStore(); store != nil {
		return store.AllocateEphemeralSessionID()
	}
	return h.nextSessionID.Add(1)
}

func (h *Hub) SetTUNReady(ready bool) {
	h.tunReady.Store(ready)
}

func (h *Hub) TUNReady() bool {
	return h.tunReady.Load()
}

func (h *Hub) RecordAuthFailure(_ string) {
	h.authFailures.Add(1)
}

func (h *Hub) DashboardSnapshot() DashboardSnapshot {
	now := time.Now().UTC()
	h.mu.RLock()
	active := make([]SessionSnapshot, 0, len(h.sessions))
	for _, s := range h.sessions {
		snap := s.Snapshot(now)
		if dns := h.dnsBySession[s.id]; len(dns) > 0 {
			snap.DNSRecent = append([]string(nil), dns...)
		}
		active = append(active, snap)
	}
	recent := append([]SessionSnapshot(nil), h.recentClosed...)
	reg := h.deviceRegistry
	store := h.sessionStore
	h.mu.RUnlock()

	for i := range active {
		active[i] = h.enrichSnapshot(active[i], reg)
	}
	for i := range recent {
		recent[i] = h.enrichSnapshot(recent[i], reg)
	}

	var registered []RegisteredDeviceView
	if reg != nil {
		registered = reg.ListViews(h.OnlineClientIDs())
		if store != nil {
			for i := range registered {
				if registered[i].SessionID != 0 {
					continue
				}
				if row, ok := store.Lookup(registered[i].ClientID); ok {
					registered[i].SessionID = row.SessionID
				}
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return DashboardSnapshot{
		Now:               now,
		UptimeSeconds:     int64(now.Sub(h.startedAt).Seconds()),
		ActiveCount:       len(active),
		TotalSessions:     h.totalSessions.Load(),
		AuthFailures:      h.authFailures.Load(),
		TotalUpBytes:      h.totalUpBytes.Load(),
		TotalDownBytes:    h.totalDownBytes.Load(),
		TotalUpPackets:    h.totalUpPkts.Load(),
		TotalDownPackets:  h.totalDownPkts.Load(),
		GoRoutines:        runtime.NumGoroutine(),
		MemAllocBytes:     m.Alloc,
		TUNReady:          h.tunReady.Load(),
		Active:            active,
		RecentClosed:      recent,
		RegisteredDevices: registered,
	}
}

func (h *Hub) AddUpstream(n uint64) {
	h.totalUpBytes.Add(n)
	h.totalUpPkts.Add(1)
}

func (h *Hub) AddDownstream(n uint64) {
	h.totalDownBytes.Add(n)
	h.totalDownPkts.Add(1)
}

func (h *Hub) RecordDNS(sessionID uint64, qname string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	row := append([]string{qname}, h.dnsBySession[sessionID]...)
	if len(row) > 50 {
		row = row[:50]
	}
	h.dnsBySession[sessionID] = row
}

func (h *Hub) findSession(id uint64) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.sessions {
		if s.id == id {
			return s
		}
	}
	return nil
}

// DisconnectSession closes an active session without adding a temporary block.
func (h *Hub) DisconnectSession(id uint64) bool {
	target := h.findSession(id)
	if target == nil {
		return false
	}
	_ = target.closeConn()
	return true
}

func (h *Hub) DisconnectAndBlockSession(id uint64, d time.Duration) bool {
	target := h.findSession(id)
	if target == nil {
		return false
	}

	exp := time.Now().UTC().Add(d)
	h.mu.Lock()
	if cid := target.clientID(); cid != "" {
		h.blockedClients[cid] = exp
	}
	host, _, err := net.SplitHostPort(target.remoteAddr)
	if err == nil && host != "" {
		h.blockedRemotes[host] = exp
	}
	h.mu.Unlock()
	_ = target.closeConn()
	return true
}

func (h *Hub) IsClientBlocked(remoteAddr string, device map[string]string) (bool, string) {
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, exp := range h.blockedClients {
		if now.After(exp) {
			delete(h.blockedClients, k)
		}
	}
	for k, exp := range h.blockedRemotes {
		if now.After(exp) {
			delete(h.blockedRemotes, k)
		}
	}
	for k, v := range h.resumeByClient {
		if now.After(v.expiresAt) {
			delete(h.resumeByClient, k)
		}
	}
	for k, v := range h.resumeTokenMap {
		if now.After(v.expiresAt) {
			delete(h.resumeTokenMap, k)
		}
	}
	if cid := strings.TrimSpace(device["client_id"]); cid != "" {
		if exp, ok := h.blockedClients[cid]; ok && now.Before(exp) {
			return true, "client_id blocked"
		}
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && host != "" {
		if exp, ok := h.blockedRemotes[host]; ok && now.Before(exp) {
			return true, "remote blocked"
		}
	}
	return false, ""
}

func (h *Hub) PreferredIPForClient(clientID string) (net.IP, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return nil, false
	}
	if store := h.SessionStore(); store != nil {
		if row, ok := store.Lookup(clientID); ok {
			ip := net.ParseIP(row.ClientIP)
			if ip != nil {
				return append(net.IP(nil), ip.To4()...), true
			}
		}
	}
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, ok := h.resumeByClient[clientID]
	if !ok {
		return nil, false
	}
	if now.After(entry.expiresAt) {
		delete(h.resumeByClient, clientID)
		return nil, false
	}
	if entry.ip == nil {
		return nil, false
	}
	return append(net.IP(nil), entry.ip...), true
}

func (h *Hub) ResetAllSessions() int {
	store := h.SessionStore()
	h.mu.Lock()
	active := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		active = append(active, s)
	}
	h.recentClosed = nil
	h.resumeByClient = make(map[string]resumeEntry)
	h.resumeTokenMap = make(map[string]resumeTokenEntry)
	h.dnsBySession = make(map[uint64][]string)
	h.mu.Unlock()

	for _, s := range active {
		_ = s.closeConn()
	}
	if store != nil {
		store.Reset()
	}
	return len(active)
}

func (h *Hub) IssueResumeToken(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	tok := hex.EncodeToString(raw)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resumeTokenMap[tok] = resumeTokenEntry{
		clientID:  clientID,
		expiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	return tok
}

func (h *Hub) ResolveResumeToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	now := time.Now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.resumeTokenMap[token]
	if !ok {
		return "", false
	}
	if now.After(e.expiresAt) {
		delete(h.resumeTokenMap, token)
		return "", false
	}
	return e.clientID, true
}
