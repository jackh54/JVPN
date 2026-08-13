package protocol

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
)

func TestHandshakeRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	token := "test-secret-token"
	if err := writeClientHandshake(&buf, token); err != nil {
		t.Fatal(err)
	}
	got, err := ReadClientHandshake(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != token {
		t.Fatalf("token %q != %q", got.Token, token)
	}
}

func writeClientHandshake(w *bytes.Buffer, token string) error {
	w.WriteByte(Magic0)
	w.WriteByte(Magic1)
	w.WriteByte(Magic2)
	w.WriteByte(Magic3)
	w.WriteByte(V1)
	_ = binary.Write(w, binary.BigEndian, uint16(len(token)))
	w.WriteString(token)
	return nil
}

func TestHandshakeV2WithDeviceMetadata(t *testing.T) {
	var buf bytes.Buffer
	token := "abc123"
	meta := map[string]string{"device_name": "iPhone", "os": "iOS"}
	raw, _ := json.Marshal(meta)
	buf.Write([]byte{Magic0, Magic1, Magic2, Magic3, V2})
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(token)))
	buf.WriteString(token)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(raw)))
	buf.Write(raw)

	got, err := ReadClientHandshake(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != V2 || got.Token != token || got.Device["device_name"] != "iPhone" {
		t.Fatalf("unexpected hello: %#v", got)
	}
}

func TestHandshakeV3WithResumeToken(t *testing.T) {
	var buf bytes.Buffer
	token := "abc123"
	meta := map[string]string{"client_id": "cid-1"}
	raw, _ := json.Marshal(meta)
	resume := "resume-token-1"
	buf.Write([]byte{Magic0, Magic1, Magic2, Magic3, V3})
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(token)))
	buf.WriteString(token)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(raw)))
	buf.Write(raw)
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(resume)))
	buf.WriteString(resume)

	got, err := ReadClientHandshake(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != V3 || got.ResumeToken != resume || got.Device["client_id"] != "cid-1" {
		t.Fatalf("unexpected v3 hello: %#v", got)
	}
}

func TestServerHandshakeOK(t *testing.T) {
	var buf bytes.Buffer
	ip := net.IPv4(10, 8, 0, 2)
	if err := WriteServerHandshake(&buf, StatusOK, ip, 24); err != nil {
		t.Fatal(err)
	}
	gotIP, plen, err := ReadServerHandshake(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !gotIP.Equal(ip) || plen != 24 {
		t.Fatalf("got %v / %d", gotIP, plen)
	}
}

func TestControlHeartbeat(t *testing.T) {
	payload := []byte{CtrlMagic, CtrlHeartbeat}
	typ, body, ok := ParseControlFrame(payload)
	if !ok || typ != CtrlHeartbeat || body != nil {
		t.Fatalf("got typ=%d body=%v ok=%v", typ, body, ok)
	}
	if IsControlFrame([]byte{0x45, 0x00}) {
		t.Fatal("IPv4 should not be control")
	}
}

func TestControlTelemetry(t *testing.T) {
	pct := 87.5
	charging := true
	lat := 37.7749
	lon := -122.4194
	raw, err := json.Marshal(Telemetry{
		ClientID:   "cid-1",
		DeviceName: "Jack’s iPhone",
		Model:      "iPhone16,1",
		OS:         "iOS 18.5",
		BatteryPct: &pct,
		Charging:   &charging,
		Lat:        &lat,
		Lon:        &lon,
		UpdatedAt:  "2026-08-13T04:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte{CtrlMagic, CtrlTelemetry}, raw...)
	typ, body, ok := ParseControlFrame(payload)
	if !ok || typ != CtrlTelemetry {
		t.Fatalf("parse control: typ=%d ok=%v", typ, ok)
	}
	got, err := ParseTelemetryJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "cid-1" || got.DeviceName == "" || got.BatteryPct == nil || *got.BatteryPct != pct {
		t.Fatalf("unexpected telemetry: %#v", got)
	}
	if got.Lat == nil || *got.Lat != lat || got.Lon == nil || *got.Lon != lon {
		t.Fatalf("unexpected location: %#v", got)
	}

	var framed bytes.Buffer
	if err := WriteFrame(&framed, payload); err != nil {
		t.Fatal(err)
	}
	out, err := ReadFrame(&framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatalf("frame round-trip mismatch")
	}
}

func TestControlTelemetryStringEncoded(t *testing.T) {
	// Matches iOS JVPNControlProtocol.telemetryFrame([String:String]).
	body := []byte(`{"client_id":"cid-1","battery_pct":"87.5","charging":"1","lat":"37.7749","lon":"-122.4194","updated_at":"1723500000"}`)
	got, err := ParseTelemetryJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "cid-1" || got.BatteryPct == nil || *got.BatteryPct != 87.5 {
		t.Fatalf("unexpected telemetry: %#v", got)
	}
	if got.Charging == nil || !*got.Charging {
		t.Fatalf("expected charging true: %#v", got)
	}
	if got.Lat == nil || *got.Lat != 37.7749 || got.Lon == nil || *got.Lon != -122.4194 {
		t.Fatalf("unexpected location: %#v", got)
	}
	if got.UpdatedAt != "1723500000" {
		t.Fatalf("updated_at: %q", got.UpdatedAt)
	}
}
