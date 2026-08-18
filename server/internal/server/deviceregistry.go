package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RegisteredDevice struct {
	ClientID       string     `json:"client_id,omitempty"`
	Label          string     `json:"label"`
	Notes          string     `json:"notes,omitempty"`
	AddedAt        time.Time  `json:"added_at"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	LastDeviceName string     `json:"last_device_name,omitempty"`
	LastModel      string     `json:"last_model,omitempty"`
}

type RegisteredDeviceView struct {
	ClientID       string     `json:"client_id,omitempty"`
	Label          string     `json:"label"`
	Notes          string     `json:"notes,omitempty"`
	AddedAt        time.Time  `json:"added_at"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	LastDeviceName string     `json:"last_device_name,omitempty"`
	LastModel      string     `json:"last_model,omitempty"`
	Online         bool       `json:"online"`
	SessionID      uint64     `json:"session_id,omitempty"`
}

type deviceRegistryFile struct {
	Devices []RegisteredDevice `json:"devices"`
}

type DeviceRegistry struct {
	mu      sync.RWMutex
	path    string
	byID    map[string]*RegisteredDevice
	pending []*RegisteredDevice
}

func NewDeviceRegistry(path string) (*DeviceRegistry, error) {
	r := &DeviceRegistry{
		path: path,
		byID: make(map[string]*RegisteredDevice),
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *DeviceRegistry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.byID = make(map[string]*RegisteredDevice)
	r.pending = nil

	if r.path == "" {
		return nil
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var file deviceRegistryFile
	if err := json.Unmarshal(b, &file); err != nil {
		return err
	}
	for i := range file.Devices {
		d := file.Devices[i]
		d.Label = strings.TrimSpace(d.Label)
		d.ClientID = strings.TrimSpace(d.ClientID)
		if d.Label == "" {
			continue
		}
		if d.ClientID != "" {
			cp := d
			r.byID[d.ClientID] = &cp
			continue
		}
		cp := d
		r.pending = append(r.pending, &cp)
	}
	return nil
}

func (r *DeviceRegistry) matchPending(deviceName string) (*RegisteredDevice, int) {
	deviceName = strings.ToLower(strings.TrimSpace(deviceName))
	for i, p := range r.pending {
		label := strings.ToLower(strings.TrimSpace(p.Label))
		if label == "" {
			continue
		}
		if deviceName == label || strings.Contains(deviceName, label) || strings.Contains(label, deviceName) {
			return p, i
		}
	}
	return nil, -1
}

func (r *DeviceRegistry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	devices := make([]RegisteredDevice, 0, len(r.byID)+len(r.pending))
	for _, d := range r.byID {
		devices = append(devices, *d)
	}
	for _, d := range r.pending {
		devices = append(devices, *d)
	}
	b, err := json.MarshalIndent(deviceRegistryFile{Devices: devices}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *DeviceRegistry) LabelForClient(clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if d, ok := r.byID[clientID]; ok {
		return d.Label
	}
	return ""
}

func (r *DeviceRegistry) TouchClient(clientID, deviceName, model string) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return
	}
	deviceName = strings.TrimSpace(deviceName)
	model = strings.TrimSpace(model)
	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	d, ok := r.byID[clientID]
	if !ok {
		label := deviceName
		if label == "" {
			label = clientID[:min(8, len(clientID))]
		}
		notes := ""
		if linked, idx := r.matchPending(deviceName); linked != nil {
			label = linked.Label
			notes = linked.Notes
			r.pending = append(r.pending[:idx], r.pending[idx+1:]...)
		}
		d = &RegisteredDevice{
			ClientID: clientID,
			Label:    label,
			Notes:    notes,
			AddedAt:  now,
		}
		r.byID[clientID] = d
	}
	d.LastSeenAt = &now
	if deviceName != "" {
		d.LastDeviceName = deviceName
	}
	if model != "" {
		d.LastModel = model
	}
	_ = r.saveLocked()
}

func (r *DeviceRegistry) RegisterPending(label, notes string) (RegisteredDevice, error) {
	label = strings.TrimSpace(label)
	notes = strings.TrimSpace(notes)
	if label == "" {
		return RegisteredDevice{}, errDeviceLabelRequired
	}
	now := time.Now().UTC()
	d := RegisteredDevice{
		Label:   label,
		Notes:   notes,
		AddedAt: now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, &d)
	if err := r.saveLocked(); err != nil {
		return RegisteredDevice{}, err
	}
	return d, nil
}

func (r *DeviceRegistry) UpsertClient(clientID, label, notes string) (RegisteredDevice, error) {
	clientID = strings.TrimSpace(clientID)
	label = strings.TrimSpace(label)
	notes = strings.TrimSpace(notes)
	if clientID == "" {
		return RegisteredDevice{}, errDeviceClientIDRequired
	}
	if label == "" {
		return RegisteredDevice{}, errDeviceLabelRequired
	}
	now := time.Now().UTC()

	r.mu.Lock()
	defer r.mu.Unlock()

	d, ok := r.byID[clientID]
	if !ok {
		d = &RegisteredDevice{
			ClientID: clientID,
			AddedAt:  now,
		}
		r.byID[clientID] = d
	}
	d.Label = label
	if notes != "" {
		d.Notes = notes
	}
	if err := r.saveLocked(); err != nil {
		return RegisteredDevice{}, err
	}
	return *d, nil
}

func (r *DeviceRegistry) ListViews(online map[string]uint64) []RegisteredDeviceView {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]RegisteredDeviceView, 0, len(r.byID)+len(r.pending))
	for _, d := range r.byID {
		v := registeredView(*d, online)
		out = append(out, v)
	}
	for _, d := range r.pending {
		out = append(out, RegisteredDeviceView{
			Label:   d.Label,
			Notes:   d.Notes,
			AddedAt: d.AddedAt,
		})
	}
	return out
}

func registeredView(d RegisteredDevice, online map[string]uint64) RegisteredDeviceView {
	v := RegisteredDeviceView{
		ClientID:       d.ClientID,
		Label:          d.Label,
		Notes:          d.Notes,
		AddedAt:        d.AddedAt,
		LastSeenAt:     d.LastSeenAt,
		LastDeviceName: d.LastDeviceName,
		LastModel:      d.LastModel,
	}
	if sid, ok := online[d.ClientID]; ok {
		v.Online = true
		v.SessionID = sid
	}
	return v
}

var (
	errDeviceLabelRequired    = &deviceRegistryError{"label is required"}
	errDeviceClientIDRequired = &deviceRegistryError{"client_id is required"}
)

type deviceRegistryError struct {
	msg string
}

func (e *deviceRegistryError) Error() string { return e.msg }
