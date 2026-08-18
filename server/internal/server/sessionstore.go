package server

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type StoredClientSession struct {
	ClientID   string            `json:"client_id"`
	SessionID  uint64            `json:"session_id"`
	ClientIP   string            `json:"client_ip"`
	DeviceInfo map[string]string `json:"device_info,omitempty"`
	FirstSeen  time.Time         `json:"first_seen"`
	LastSeen   time.Time         `json:"last_seen"`
}

type sessionStoreFile struct {
	NextSessionID uint64                `json:"next_session_id"`
	Clients       []StoredClientSession `json:"clients"`
}

type SessionBinding struct {
	SessionID  uint64
	ClientIP   net.IP
	DeviceInfo map[string]string
	Resumed    bool
}

type SessionStore struct {
	mu            sync.RWMutex
	path          string
	nextSessionID uint64
	byClient      map[string]*StoredClientSession
	bySession     map[uint64]string
}

func NewSessionStore(path string) (*SessionStore, error) {
	s := &SessionStore{
		path:      path,
		byClient:  make(map[string]*StoredClientSession),
		bySession: make(map[uint64]string),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *SessionStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.byClient = make(map[string]*StoredClientSession)
	s.bySession = make(map[uint64]string)
	s.nextSessionID = 0

	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file sessionStoreFile
	if err := json.Unmarshal(b, &file); err != nil {
		return err
	}
	s.nextSessionID = file.NextSessionID
	for i := range file.Clients {
		row := file.Clients[i]
		row.ClientID = strings.TrimSpace(row.ClientID)
		if row.ClientID == "" || row.SessionID == 0 || strings.TrimSpace(row.ClientIP) == "" {
			continue
		}
		cp := row
		s.byClient[row.ClientID] = &cp
		s.bySession[row.SessionID] = row.ClientID
		if row.SessionID >= s.nextSessionID {
			s.nextSessionID = row.SessionID
		}
	}
	return nil
}

func (s *SessionStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	clients := make([]StoredClientSession, 0, len(s.byClient))
	for _, row := range s.byClient {
		clients = append(clients, *row)
	}
	b, err := json.MarshalIndent(sessionStoreFile{
		NextSessionID: s.nextSessionID,
		Clients:       clients,
	}, "", "  ")
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

func (s *SessionStore) ReservedIPs() []net.IP {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]net.IP, 0, len(s.byClient))
	for _, row := range s.byClient {
		if ip := net.ParseIP(row.ClientIP); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

func (s *SessionStore) HasClient(clientID string) bool {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byClient[clientID]
	return ok
}

func (s *SessionStore) Lookup(clientID string) (StoredClientSession, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return StoredClientSession{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.byClient[clientID]
	if !ok {
		return StoredClientSession{}, false
	}
	return *row, true
}

func (s *SessionStore) Resolve(clientID string, device map[string]string) (SessionBinding, bool) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return SessionBinding{}, false
	}
	now := time.Now().UTC()
	device = cloneDeviceInfo(device)

	s.mu.Lock()
	defer s.mu.Unlock()

	if row, ok := s.byClient[clientID]; ok {
		ip := net.ParseIP(row.ClientIP)
		if ip == nil {
			return SessionBinding{}, false
		}
		row.LastSeen = now
		if len(device) > 0 {
			row.DeviceInfo = mergeDeviceInfo(row.DeviceInfo, device)
		}
		_ = s.saveLocked()
		return SessionBinding{
			SessionID:  row.SessionID,
			ClientIP:   append(net.IP(nil), ip.To4()...),
			DeviceInfo: cloneDeviceInfo(row.DeviceInfo),
			Resumed:    true,
		}, true
	}

	sessionID := s.nextSessionID + 1
	s.nextSessionID = sessionID
	row := &StoredClientSession{
		ClientID:   clientID,
		SessionID:  sessionID,
		FirstSeen:  now,
		LastSeen:   now,
		DeviceInfo: device,
	}
	s.byClient[clientID] = row
	s.bySession[sessionID] = clientID
	_ = s.saveLocked()
	return SessionBinding{
		SessionID:  sessionID,
		DeviceInfo: cloneDeviceInfo(device),
		Resumed:    false,
	}, true
}

func (s *SessionStore) AssignIP(clientID string, ip net.IP) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" || ip == nil {
		return
	}
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.byClient[clientID]
	if !ok {
		return
	}
	row.ClientIP = ip.String()
	row.LastSeen = now
	_ = s.saveLocked()
}

func (s *SessionStore) Touch(clientID string, device map[string]string) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.byClient[clientID]
	if !ok {
		return
	}
	row.LastSeen = now
	if len(device) > 0 {
		row.DeviceInfo = mergeDeviceInfo(row.DeviceInfo, device)
	}
	_ = s.saveLocked()
}

func (s *SessionStore) AllocateEphemeralSessionID() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessionID++
	id := s.nextSessionID
	_ = s.saveLocked()
	return id
}

func (s *SessionStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byClient = make(map[string]*StoredClientSession)
	s.bySession = make(map[uint64]string)
	s.nextSessionID = 0
	_ = s.saveLocked()
}

func (s *SessionStore) KnownSessions() []StoredClientSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]StoredClientSession, 0, len(s.byClient))
	for _, row := range s.byClient {
		out = append(out, *row)
	}
	return out
}

func cloneDeviceInfo(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func mergeDeviceInfo(base, update map[string]string) map[string]string {
	if len(base) == 0 && len(update) == 0 {
		return nil
	}
	out := cloneDeviceInfo(base)
	if out == nil {
		out = make(map[string]string, len(update))
	}
	for k, v := range update {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out[k] = v
	}
	return out
}
