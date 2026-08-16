package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackh54/jvpn-server/internal/protocol"
	ipool "github.com/jackh54/jvpn-server/internal/session"
)

// IdleTimeout closes a session if no framed traffic (IP, telemetry, or heartbeat) arrives.
const IdleTimeout = 5 * time.Minute

// Session ties one authenticated client to a tunnel IP and downstream queue.
type Session struct {
	id              uint64
	clientIP        net.IP
	remoteAddr      string
	connectedAt     time.Time
	hub             *Hub
	deviceInfo      map[string]string
	deviceMu        sync.RWMutex
	telemetryAt     time.Time
	downstream      chan []byte
	connMu          sync.Mutex
	conn            net.Conn
	upstreamBytes   atomic.Uint64
	downstreamBytes atomic.Uint64
	upstreamPackets atomic.Uint64
	downstreamPkts  atomic.Uint64
}

// ServeConn runs the tunnel for one client connection (TLS already terminated).
func ServeConn(c net.Conn, hub *Hub, pool *ipool.IPPool, token []byte, tun io.Writer) {
	hello, err := protocol.ReadClientHandshake(c)
	if err != nil {
		log.Printf("handshake read: %v", err)
		_ = c.Close()
		return
	}
	clientTok := normalizePSK(hello.Token)
	serverTok := normalizePSK(string(token))
	if subtle.ConstantTimeCompare(clientTok, serverTok) != 1 {
		_ = protocol.WriteServerHandshake(c, protocol.StatusDeny, nil, 0)
		hub.RecordAuthFailure(c.RemoteAddr().String())
		log.Printf("auth failed from %s (client token len=%d server token len=%d; must match server token file exactly)",
			c.RemoteAddr(), len(clientTok), len(serverTok))
		_ = c.Close()
		return
	}
	if blocked, reason := hub.IsClientBlocked(c.RemoteAddr().String(), hello.Device); blocked {
		_ = protocol.WriteServerHandshake(c, protocol.StatusDeny, nil, 0)
		log.Printf("blocked client denied from %s (%s)", c.RemoteAddr(), reason)
		_ = c.Close()
		return
	}
	clientID := strings.TrimSpace(hello.Device["client_id"])
	if clientID == "" && hello.ResumeToken != "" {
		if cid, ok := hub.ResolveResumeToken(hello.ResumeToken); ok {
			clientID = cid
			if hello.Device == nil {
				hello.Device = map[string]string{}
			}
			hello.Device["client_id"] = clientID
		}
	}
	var clientIP net.IP
	if pref, ok := hub.PreferredIPForClient(clientID); ok {
		clientIP = pool.AllocatePreferred(pref)
		if clientIP != nil {
			log.Printf("resumed client ip for %s -> %s", clientID, clientIP.String())
		}
	}
	if clientIP == nil {
		clientIP = pool.Allocate()
	}
	if clientIP == nil {
		_ = protocol.WriteServerHandshake(c, protocol.StatusDeny, nil, 0)
		log.Printf("pool exhausted for %s", c.RemoteAddr())
		_ = c.Close()
		return
	}
	defer pool.Release(clientIP)

	prefix := byte(ipool.PrefixLength)
	var hsErr error
	if hello.Version >= protocol.V3 {
		hsErr = protocol.WriteServerHandshakeV3(c, protocol.StatusOK, clientIP, prefix, hub.IssueResumeToken(clientID))
	} else {
		hsErr = protocol.WriteServerHandshake(c, protocol.StatusOK, clientIP, prefix)
	}
	if hsErr != nil {
		log.Printf("handshake write: %v", hsErr)
		_ = c.Close()
		return
	}

	s := &Session{
		id:          hub.NextSessionID(),
		clientIP:    append(net.IP(nil), clientIP...),
		remoteAddr:  c.RemoteAddr().String(),
		connectedAt: time.Now().UTC(),
		hub:         hub,
		deviceInfo:  sanitizeDeviceInfo(hello.Device),
		downstream:  make(chan []byte, 16384),
	}
	hub.Register(clientIP, s)
	defer hub.Unregister(clientIP)
	log.Printf("client connected: session=%d remote=%s assigned=%s", s.id, s.remoteAddr, s.clientIP.String())
	s.setConn(c)
	_ = c.SetReadDeadline(time.Now().Add(IdleTimeout))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(s.downstream)
		s.tlsToTUN(c, tun)
		_ = c.Close()
	}()
	s.tunToTLS(c)
	wg.Wait()
	log.Printf("client disconnected: session=%d remote=%s assigned=%s", s.id, s.remoteAddr, s.clientIP.String())
}

func (s *Session) setConn(c net.Conn) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	s.conn = c
}

func (s *Session) closeConn() error {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (s *Session) clientID() string {
	s.deviceMu.RLock()
	defer s.deviceMu.RUnlock()
	if s.deviceInfo == nil {
		return ""
	}
	return strings.TrimSpace(s.deviceInfo["client_id"])
}

func (s *Session) touchReadDeadline(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(IdleTimeout))
}

func (s *Session) tlsToTUN(c net.Conn, tun io.Writer) {
	for {
		payload, err := protocol.ReadFrame(c)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				log.Printf("session %d idle timeout (%s); closing", s.id, IdleTimeout)
			} else if err != io.EOF {
				log.Printf("read frame: %v", err)
			}
			return
		}
		s.touchReadDeadline(c)

		if typ, body, ok := protocol.ParseControlFrame(payload); ok {
			switch typ {
			case protocol.CtrlHeartbeat:
				// keepalive only — deadline already refreshed
			case protocol.CtrlTelemetry:
				s.applyTelemetry(body)
			}
			continue
		}

		if len(payload) < 20 {
			continue
		}
		if payload[0]>>4 != 4 {
			continue
		}
		src := net.IP(payload[12:16])
		if !src.Equal(s.clientIP) {
			continue
		}
		if _, err := tun.Write(payload); err != nil {
			log.Printf("tun write: %v", err)
			return
		}
		n := uint64(len(payload))
		s.upstreamBytes.Add(n)
		s.upstreamPackets.Add(1)
		s.hub.AddUpstream(n)
		s.maybeTrackDNSQuery(payload)
	}
}

func (s *Session) applyTelemetry(body []byte) {
	t, err := protocol.ParseTelemetryJSON(body)
	if err != nil {
		log.Printf("session %d bad telemetry: %v", s.id, err)
		return
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.deviceInfo == nil {
		s.deviceInfo = make(map[string]string, 8)
	}
	set := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if len(v) > 256 {
			v = v[:256]
		}
		s.deviceInfo[k] = v
	}
	set("client_id", t.ClientID)
	set("device_name", t.DeviceName)
	set("model", t.Model)
	set("os", t.OS)
	if t.BatteryPct != nil {
		set("battery_pct", strconv.FormatFloat(*t.BatteryPct, 'f', -1, 64))
	}
	if t.Charging != nil {
		if *t.Charging {
			s.deviceInfo["charging"] = "true"
		} else {
			s.deviceInfo["charging"] = "false"
		}
	}
	if t.Lat != nil {
		set("lat", strconv.FormatFloat(*t.Lat, 'f', -1, 64))
	}
	if t.Lon != nil {
		set("lon", strconv.FormatFloat(*t.Lon, 'f', -1, 64))
	}
	if strings.TrimSpace(t.UpdatedAt) != "" {
		set("updated_at", t.UpdatedAt)
		if parsed, e := time.Parse(time.RFC3339, strings.TrimSpace(t.UpdatedAt)); e == nil {
			s.telemetryAt = parsed.UTC()
		} else {
			s.telemetryAt = time.Now().UTC()
		}
	} else {
		s.telemetryAt = time.Now().UTC()
		s.deviceInfo["updated_at"] = s.telemetryAt.Format(time.RFC3339)
	}
}

func (s *Session) tunToTLS(c net.Conn) {
	const maxBatchBytes = 64 * 1024
	for pkt := range s.downstream {
		var batch bytes.Buffer
		_ = protocol.WriteFrame(&batch, pkt)
		for batch.Len() < maxBatchBytes {
			select {
			case more, ok := <-s.downstream:
				if !ok {
					break
				}
				_ = protocol.WriteFrame(&batch, more)
			default:
				goto SEND
			}
		}
	SEND:
		_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := c.Write(batch.Bytes()); err != nil {
			log.Printf("write frame: %v", err)
			return
		}
		_ = c.SetWriteDeadline(time.Time{})
	}
}

func (s *Session) Snapshot(now time.Time) SessionSnapshot {
	s.deviceMu.RLock()
	var info map[string]string
	if len(s.deviceInfo) > 0 {
		info = make(map[string]string, len(s.deviceInfo))
		for k, v := range s.deviceInfo {
			info[k] = v
		}
	}
	telAt := s.telemetryAt
	s.deviceMu.RUnlock()

	snap := SessionSnapshot{
		SessionID:         s.id,
		ClientIP:          s.clientIP.String(),
		RemoteAddr:        s.remoteAddr,
		DeviceInfo:        info,
		ConnectedAt:       s.connectedAt,
		DurationSeconds:   int64(now.Sub(s.connectedAt).Seconds()),
		UpstreamBytes:     s.upstreamBytes.Load(),
		DownstreamBytes:   s.downstreamBytes.Load(),
		UpstreamPackets:   s.upstreamPackets.Load(),
		DownstreamPackets: s.downstreamPkts.Load(),
	}
	if info != nil {
		snap.ClientID = info["client_id"]
		snap.DeviceName = info["device_name"]
		snap.Model = info["model"]
		snap.OS = info["os"]
		if v := info["battery_pct"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				snap.BatteryPct = &f
			}
		}
		if v := info["charging"]; v != "" {
			b := v == "true" || v == "1"
			snap.Charging = &b
		}
		if v := info["lat"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				snap.Lat = &f
			}
		}
		if v := info["lon"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				snap.Lon = &f
			}
		}
	}
	if !telAt.IsZero() {
		snap.TelemetryAt = &telAt
	} else if info != nil && info["updated_at"] != "" {
		if parsed, err := time.Parse(time.RFC3339, info["updated_at"]); err == nil {
			t := parsed.UTC()
			snap.TelemetryAt = &t
		}
	}
	return snap
}

func sanitizeDeviceInfo(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		if len(k) > 64 {
			k = k[:64]
		}
		if len(v) > 256 {
			v = v[:256]
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Session) maybeTrackDNSQuery(packet []byte) {
	if len(packet) < 20 || packet[0]>>4 != 4 {
		return
	}
	ihl := int(packet[0]&0x0F) * 4
	if ihl < 20 || len(packet) < ihl+8 {
		return
	}
	if packet[9] != 17 { // UDP
		return
	}
	udp := packet[ihl:]
	dstPort := binary.BigEndian.Uint16(udp[2:4])
	if dstPort != 53 {
		return
	}
	if len(udp) < 20 { // UDP hdr + minimal DNS
		return
	}
	dns := udp[8:]
	if len(dns) < 12 {
		return
	}
	qd := binary.BigEndian.Uint16(dns[4:6])
	if qd == 0 {
		return
	}
	qname, ok := parseDNSQname(dns[12:])
	if !ok || qname == "" {
		return
	}
	s.hub.RecordDNS(s.id, qname)
}

func parseDNSQname(b []byte) (string, bool) {
	labels := make([]string, 0, 4)
	i := 0
	for i < len(b) {
		l := int(b[i])
		i++
		if l == 0 {
			break
		}
		if l > 63 || i+l > len(b) {
			return "", false
		}
		labels = append(labels, string(b[i:i+l]))
		i += l
		if len(labels) > 12 {
			return "", false
		}
	}
	if len(labels) == 0 {
		return "", false
	}
	return strings.ToLower(strings.Join(labels, ".")), true
}

// LoadToken reads a shared secret from path (first line, trimmed).
func LoadToken(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil, io.ErrUnexpectedEOF
	}
	return normalizePSK(s), nil
}

// normalizePSK trims whitespace; if the secret is all hex digits (typical auto token), lowercases for stable compare.
func normalizePSK(s string) []byte {
	s = strings.TrimSpace(s)
	if isAllHex(s) {
		return []byte(strings.ToLower(s))
	}
	return []byte(s)
}

func isAllHex(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
