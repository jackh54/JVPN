package server

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"sync"
	"time"
)

const (
	uotMaxRecord     = 65535
	defaultUoTPath   = "/dns-query"
	uotHTTPOKHeaders = "HTTP/1.1 200 OK\r\n" +
		"Content-Type: application/dns-message\r\n" +
		"Cache-Control: no-store\r\n" +
		"Connection: keep-alive\r\n" +
		"\r\n"
)

// uotConn frames the JVPN byte stream as UDP-over-TCP records:
// uint16 BE length (1..65535) + payload. Payloads concatenate to the
// existing handshake + uint32-framed data plane.
type uotConn struct {
	net.Conn
	rdMu sync.Mutex
	wrMu sync.Mutex
	rbuf []byte
}

func newUoTConn(c net.Conn) *uotConn {
	return &uotConn{Conn: c}
}

func (c *uotConn) NetConn() net.Conn {
	return c.Conn
}

func writeUoTRecord(w io.Writer, payload []byte) error {
	n := len(payload)
	if n == 0 || n > uotMaxRecord {
		return fmt.Errorf("uot record length %d", n)
	}
	buf := make([]byte, 2+n)
	binary.BigEndian.PutUint16(buf[:2], uint16(n))
	copy(buf[2:], payload)
	_, err := w.Write(buf)
	return err
}

func readUoTRecord(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	if n == 0 {
		return nil, fmt.Errorf("invalid uot record length: %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *uotConn) Read(p []byte) (int, error) {
	c.rdMu.Lock()
	defer c.rdMu.Unlock()
	if len(c.rbuf) == 0 {
		rec, err := readUoTRecord(c.Conn)
		if err != nil {
			return 0, err
		}
		c.rbuf = rec
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}

func (c *uotConn) Write(p []byte) (int, error) {
	c.wrMu.Lock()
	defer c.wrMu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(p) {
		n := len(p) - written
		if n > uotMaxRecord {
			n = uotMaxRecord
		}
		if err := writeUoTRecord(c.Conn, p[written:written+n]); err != nil {
			return written, err
		}
		written += n
	}
	return written, nil
}

type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func connWithBuffered(conn net.Conn, br *bufio.Reader) net.Conn {
	n := br.Buffered()
	if n <= 0 {
		return conn
	}
	extra, err := br.Peek(n)
	if err != nil || len(extra) == 0 {
		return conn
	}
	return &prefixConn{Conn: conn, prefix: append([]byte(nil), extra...)}
}

func handleUoT(ln *wsListener, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	if buf != nil && buf.Writer != nil {
		_ = buf.Writer.Flush()
	}
	if _, err := conn.Write([]byte(uotHTTPOKHeaders)); err != nil {
		_ = conn.Close()
		return
	}
	inner := net.Conn(conn)
	if buf != nil && buf.Reader != nil {
		inner = connWithBuffered(conn, buf.Reader)
	}
	uot := newUoTConn(inner)
	select {
	case <-ln.closed:
		_ = uot.Close()
	case ln.conns <- uot:
	case <-time.After(5 * time.Second):
		_ = uot.Close()
	}
}

// ParseUoTPath normalizes the UDP-over-TCP HTTP path (DoH-style /dns-query).
func ParseUoTPath(p string) string {
	if p == "" {
		return defaultUoTPath
	}
	if p[0] != '/' {
		p = "/" + p
	}
	clean := path.Clean(p)
	if clean == "." || clean == "/" {
		return defaultUoTPath
	}
	return clean
}
