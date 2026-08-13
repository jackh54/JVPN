package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

const (
	Magic0 = 'J'
	Magic1 = 'V'
	Magic2 = 'P'
	Magic3 = 'N'
	V1     = byte(1)
	V2     = byte(2)
	V3     = byte(3)
)

const (
	StatusOK    = byte(0)
	StatusDeny  = byte(1)
	MaxFrameLen = 65535
)

// Tunnel control frames share the same uint32-BE length framing as IPv4 packets.
// Payload is not an IPv4 packet: magic 0xC0 + type (+ optional body).
const (
	CtrlMagic     = byte(0xC0)
	CtrlTelemetry = byte(0x01) // + UTF-8 JSON
	CtrlHeartbeat = byte(0x02) // no body
)

// Telemetry is the JSON body for CtrlTelemetry (keys match the iOS client).
type Telemetry struct {
	ClientID   string   `json:"client_id,omitempty"`
	DeviceName string   `json:"device_name,omitempty"`
	Model      string   `json:"model,omitempty"`
	OS         string   `json:"os,omitempty"`
	BatteryPct *float64 `json:"battery_pct,omitempty"`
	Charging   *bool    `json:"charging,omitempty"`
	Lat        *float64 `json:"lat,omitempty"`
	Lon        *float64 `json:"lon,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
}

// IsControlFrame reports whether a framed payload is a control message (not IPv4).
func IsControlFrame(payload []byte) bool {
	return len(payload) >= 2 && payload[0] == CtrlMagic
}

// ParseControlFrame returns the control type and body (telemetry JSON for type 0x01).
func ParseControlFrame(payload []byte) (typ byte, body []byte, ok bool) {
	if !IsControlFrame(payload) {
		return 0, nil, false
	}
	typ = payload[1]
	switch typ {
	case CtrlHeartbeat:
		return typ, nil, true
	case CtrlTelemetry:
		return typ, payload[2:], true
	default:
		return typ, nil, false
	}
}

// ParseTelemetryJSON decodes a CtrlTelemetry body.
// Accepts native JSON numbers/bools and iOS string-encoded values
// (telemetry frames are built from [String:String] dictionaries).
func ParseTelemetryJSON(body []byte) (Telemetry, error) {
	var t Telemetry
	if len(body) == 0 {
		return t, errors.New("empty telemetry")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return t, err
	}
	t.ClientID = rawString(raw["client_id"])
	t.DeviceName = rawString(raw["device_name"])
	t.Model = rawString(raw["model"])
	t.OS = rawString(raw["os"])
	t.UpdatedAt = rawString(raw["updated_at"])
	if v, ok, err := rawFloat(raw["battery_pct"]); err != nil {
		return t, fmt.Errorf("battery_pct: %w", err)
	} else if ok {
		t.BatteryPct = &v
	}
	if v, ok, err := rawBool(raw["charging"]); err != nil {
		return t, fmt.Errorf("charging: %w", err)
	} else if ok {
		t.Charging = &v
	}
	if v, ok, err := rawFloat(raw["lat"]); err != nil {
		return t, fmt.Errorf("lat: %w", err)
	} else if ok {
		t.Lat = &v
	}
	if v, ok, err := rawFloat(raw["lon"]); err != nil {
		return t, fmt.Errorf("lon: %w", err)
	} else if ok {
		t.Lon = &v
	}
	return t, nil
}

func rawString(b json.RawMessage) string {
	if len(b) == 0 || string(b) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return s
	}
	return strings.Trim(string(b), `"`)
}

func rawFloat(b json.RawMessage) (float64, bool, error) {
	if len(b) == 0 || string(b) == "null" {
		return 0, false, nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		return f, true, nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return 0, false, err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false, err
	}
	return f, true, nil
}

func rawBool(b json.RawMessage) (bool, bool, error) {
	if len(b) == 0 || string(b) == "null" {
		return false, false, nil
	}
	var v bool
	if err := json.Unmarshal(b, &v); err == nil {
		return v, true, nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return false, false, err
	}
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "":
		return false, false, nil
	case "1", "true", "yes", "on":
		return true, true, nil
	case "0", "false", "no", "off":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("invalid bool %q", s)
	}
}

var ErrBadHandshake = errors.New("invalid handshake")

type ClientHello struct {
	Token       string            `json:"token"`
	Device      map[string]string `json:"device,omitempty"`
	ResumeToken string            `json:"resume_token,omitempty"`
	Version     byte              `json:"version"`
}

// ReadClientHandshake reads the JVPN auth frame after TLS is established.
func ReadClientHandshake(r io.Reader) (hello ClientHello, err error) {
	var hdr [7]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return ClientHello{}, err
	}
	if hdr[0] != Magic0 || hdr[1] != Magic1 || hdr[2] != Magic2 || hdr[3] != Magic3 {
		return ClientHello{}, ErrBadHandshake
	}
	if hdr[4] != V1 && hdr[4] != V2 && hdr[4] != V3 {
		return ClientHello{}, fmt.Errorf("%w: bad version", ErrBadHandshake)
	}
	n := binary.BigEndian.Uint16(hdr[5:7])
	if n == 0 || int(n) > 4096 {
		return ClientHello{}, ErrBadHandshake
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return ClientHello{}, err
	}
	hello = ClientHello{
		Token:   string(buf),
		Version: hdr[4],
	}
	if hdr[4] == V2 || hdr[4] == V3 {
		var metaLenBuf [2]byte
		if _, err := io.ReadFull(r, metaLenBuf[:]); err != nil {
			return ClientHello{}, err
		}
		metaLen := binary.BigEndian.Uint16(metaLenBuf[:])
		if metaLen > 4096 {
			return ClientHello{}, ErrBadHandshake
		}
		if metaLen > 0 {
			metaRaw := make([]byte, metaLen)
			if _, err := io.ReadFull(r, metaRaw); err != nil {
				return ClientHello{}, err
			}
			var meta map[string]string
			if err := json.Unmarshal(metaRaw, &meta); err != nil {
				return ClientHello{}, fmt.Errorf("%w: bad metadata", ErrBadHandshake)
			}
			hello.Device = meta
		}
	}
	if hdr[4] == V3 {
		var resumeLenBuf [2]byte
		if _, err := io.ReadFull(r, resumeLenBuf[:]); err != nil {
			return ClientHello{}, err
		}
		resumeLen := binary.BigEndian.Uint16(resumeLenBuf[:])
		if resumeLen > 512 {
			return ClientHello{}, ErrBadHandshake
		}
		if resumeLen > 0 {
			rb := make([]byte, resumeLen)
			if _, err := io.ReadFull(r, rb); err != nil {
				return ClientHello{}, err
			}
			hello.ResumeToken = string(rb)
		}
	}
	return hello, nil
}

// WriteServerHandshake responds with status and, on success, assigned client IPv4 and /24 prefix.
func WriteServerHandshake(w io.Writer, status byte, clientIP net.IP, prefixLen byte) error {
	if status != StatusOK {
		_, err := w.Write([]byte{status})
		return err
	}
	ip4 := clientIP.To4()
	if ip4 == nil {
		return errors.New("clientIP must be IPv4")
	}
	buf := make([]byte, 1+4+1)
	buf[0] = StatusOK
	copy(buf[1:5], ip4)
	buf[5] = prefixLen
	_, err := w.Write(buf)
	return err
}

// WriteServerHandshakeV3 responds with v3 server hello extension (resume token).
func WriteServerHandshakeV3(w io.Writer, status byte, clientIP net.IP, prefixLen byte, resumeToken string) error {
	if status != StatusOK {
		_, err := w.Write([]byte{status})
		return err
	}
	ip4 := clientIP.To4()
	if ip4 == nil {
		return errors.New("clientIP must be IPv4")
	}
	rt := []byte(resumeToken)
	if len(rt) > 512 {
		rt = rt[:512]
	}
	buf := make([]byte, 1+4+1+2+len(rt))
	buf[0] = StatusOK
	copy(buf[1:5], ip4)
	buf[5] = prefixLen
	binary.BigEndian.PutUint16(buf[6:8], uint16(len(rt)))
	copy(buf[8:], rt)
	_, err := w.Write(buf)
	return err
}

// ReadServerHandshake reads server response on the client.
func ReadServerHandshake(r io.Reader) (clientIP net.IP, prefixLen byte, err error) {
	var st [1]byte
	if _, err := io.ReadFull(r, st[:]); err != nil {
		return nil, 0, err
	}
	if st[0] != StatusOK {
		return nil, 0, fmt.Errorf("handshake denied: %d", st[0])
	}
	var rest [5]byte
	if _, err := io.ReadFull(r, rest[:]); err != nil {
		return nil, 0, err
	}
	return net.IP(append(net.IP(nil), rest[0:4]...)), rest[4], nil
}

// Frame wire format (post-handshake): uint32 BE length + payload.
//
// Data plane: raw IPv4 packet payload.
// Control plane (first byte 0xC0 — never a valid IPv4 version nibble):
//   - 0xC0 0x01 + UTF-8 JSON telemetry (client_id, device_name, model, os,
//     battery_pct, charging, lat, lon, updated_at)
//   - 0xC0 0x02 heartbeat (empty body after type)
func WriteFrame(w io.Writer, payload []byte) error {
	if len(payload) > MaxFrameLen {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func ReadFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrameLen {
		return nil, fmt.Errorf("invalid frame length: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
