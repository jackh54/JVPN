package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jackh54/jvpn-server/internal/protocol"
)

func TestParseUoTPath(t *testing.T) {
	if ParseUoTPath("") != "/dns-query" {
		t.Fatalf("empty: %q", ParseUoTPath(""))
	}
	if ParseUoTPath("dns-query") != "/dns-query" {
		t.Fatalf("relative: %q", ParseUoTPath("dns-query"))
	}
	if ParseUoTPath("/dns-query") != "/dns-query" {
		t.Fatalf("absolute: %q", ParseUoTPath("/dns-query"))
	}
	if ParseUoTPath("/") != "/dns-query" {
		t.Fatalf("slash: %q", ParseUoTPath("/"))
	}
}

func TestUoTRecordRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte("hello-uot")
	if err := writeUoTRecord(&buf, payload); err != nil {
		t.Fatal(err)
	}
	got, err := readUoTRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
}

func TestUoTRecordRejectsEmpty(t *testing.T) {
	if err := writeUoTRecord(io.Discard, nil); err == nil {
		t.Fatal("expected error")
	}
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.BigEndian, uint16(0))
	if _, err := readUoTRecord(&buf); err == nil {
		t.Fatal("expected invalid length")
	}
}

func TestUoTConnStreamCoalesce(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	client := newUoTConn(a)
	server := newUoTConn(b)

	want := make([]byte, 0, 2048)
	var frame bytes.Buffer
	pkt1 := bytes.Repeat([]byte{0x45, 0x00}, 40)
	pkt2 := bytes.Repeat([]byte{0x45, 0x01}, 30)
	if err := protocol.WriteFrame(&frame, pkt1); err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFrame(&frame, pkt2); err != nil {
		t.Fatal(err)
	}
	want = append(want, frame.Bytes()...)

	errCh := make(chan error, 1)
	go func() {
		_, err := client.Write(frame.Bytes())
		errCh <- err
	}()

	got := make([]byte, len(want))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("stream mismatch")
	}

	r := bytes.NewReader(got)
	p1, err := protocol.ReadFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := protocol.ReadFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p1, pkt1) || !bytes.Equal(p2, pkt2) {
		t.Fatal("inner frames mismatch")
	}
}

func TestUoTHTTPUpgradeAndHandshake(t *testing.T) {
	cert, err := testTLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := ListenWebSocketTLS("127.0.0.1:0", tlsCfg, "/ws", "/dns-query")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	token := "test-uot-token"
	hello := []byte{'J', 'V', 'P', 'N', 1, 0, byte(len(token))}
	hello = append(hello, token...)

	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		got, err := protocol.ReadClientHandshake(c)
		if err != nil {
			done <- err
			return
		}
		if got.Token != token {
			done <- errTokenMismatch(got.Token, token)
			return
		}
		done <- protocol.WriteServerHandshake(c, protocol.StatusOK, net.IPv4(10, 8, 0, 9), 24)
	}()

	addr := ln.Addr().String()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	var raw net.Conn
	var dialErr error
	for i := 0; i < 20; i++ {
		raw, dialErr = net.DialTimeout("tcp", addr, time.Second)
		if dialErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	defer raw.Close()
	cli := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		ServerName:         host,
	})
	if err := cli.Handshake(); err != nil {
		t.Fatal(err)
	}

	req := "POST /dns-query HTTP/1.1\r\n" +
		"Host: " + host + ":" + port + "\r\n" +
		"Content-Type: application/dns-message\r\n" +
		"Accept: application/dns-message\r\n" +
		"Content-Length: 0\r\n" +
		"Connection: keep-alive\r\n" +
		"\r\n"
	if _, err := cli.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	hdr, err := readUntilHTTPHeaders(cli, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hdr, "200") {
		t.Fatalf("upgrade status: %q", hdr)
	}

	if err := writeUoTRecord(cli, hello); err != nil {
		t.Fatal(err)
	}
	rec, err := readUoTRecord(cli)
	if err != nil {
		t.Fatal(err)
	}
	ip, plen, err := protocol.ReadServerHandshake(bytes.NewReader(rec))
	if err != nil {
		t.Fatal(err)
	}
	if !ip.Equal(net.IPv4(10, 8, 0, 9)) || plen != 24 {
		t.Fatalf("assigned %v/%d", ip, plen)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func errTokenMismatch(got, want string) error {
	return &mismatchError{got: got, want: want}
}

type mismatchError struct{ got, want string }

func (e *mismatchError) Error() string {
	return "token mismatch: got " + e.got + " want " + e.want
}

func readUntilHTTPHeaders(r io.Reader, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var buf []byte
	tmp := make([]byte, 256)
	for {
		if time.Now().After(deadline) {
			return string(buf), fmt.Errorf("timeout reading HTTP headers")
		}
		if conn, ok := r.(net.Conn); ok {
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		}
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if bytes.Contains(buf, []byte("\r\n\r\n")) {
				if conn, ok := r.(net.Conn); ok {
					_ = conn.SetReadDeadline(time.Time{})
				}
				return string(buf), nil
			}
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return string(buf), err
		}
	}
}

func testTLSCertificate() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "jvpn-uot-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}
