package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type wsConn struct {
	c      *websocket.Conn
	rbuf   []byte
	rdMu   sync.Mutex
	wrMu   sync.Mutex
	closed chan struct{}
	once   sync.Once
}

func newWSConn(c *websocket.Conn) *wsConn {
	// Do not run a server-side WS ping loop: Network.framework clients often drop
	// every ping interval (~25s) and open a new VPN session. App-level heartbeats
	// refresh session IdleTimeout instead.
	w := &wsConn{c: c, closed: make(chan struct{})}
	c.SetPongHandler(func(_ string) error { return nil })
	c.SetPingHandler(func(appData string) error {
		w.wrMu.Lock()
		defer w.wrMu.Unlock()
		return c.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(2*time.Second))
	})
	return w
}

func (w *wsConn) Read(p []byte) (int, error) {
	w.rdMu.Lock()
	defer w.rdMu.Unlock()
	for len(w.rbuf) == 0 {
		mt, data, err := w.c.ReadMessage()
		if err != nil {
			return 0, err
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		w.rbuf = data
	}
	n := copy(p, w.rbuf)
	w.rbuf = w.rbuf[n:]
	return n, nil
}

func (w *wsConn) Write(p []byte) (int, error) {
	w.wrMu.Lock()
	defer w.wrMu.Unlock()
	// Avoid one giant WS message under download floods (speedtests).
	const maxMsg = 32 * 1024
	offset := 0
	for offset < len(p) {
		end := offset + maxMsg
		if end > len(p) {
			end = len(p)
		}
		if err := w.c.WriteMessage(websocket.BinaryMessage, p[offset:end]); err != nil {
			return offset, err
		}
		offset = end
	}
	return len(p), nil
}

func (w *wsConn) Close() error {
	w.signalClosed()
	_ = w.c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(2*time.Second))
	return w.c.Close()
}

func (w *wsConn) signalClosed() {
	w.once.Do(func() {
		close(w.closed)
	})
}

func (w *wsConn) LocalAddr() net.Addr  { return w.c.LocalAddr() }
func (w *wsConn) RemoteAddr() net.Addr { return w.c.RemoteAddr() }
func (w *wsConn) SetDeadline(t time.Time) error {
	if err := w.c.SetReadDeadline(t); err != nil {
		return err
	}
	return w.c.SetWriteDeadline(t)
}
func (w *wsConn) SetReadDeadline(t time.Time) error  { return w.c.SetReadDeadline(t) }
func (w *wsConn) SetWriteDeadline(t time.Time) error { return w.c.SetWriteDeadline(t) }

type wsListener struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	stop   context.CancelFunc
}

func (l *wsListener) Accept() (net.Conn, error) {
	select {
	case <-l.closed:
		return nil, net.ErrClosed
	case c := <-l.conns:
		return c, nil
	}
}

func (l *wsListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		l.stop()
	})
	return nil
}

func (l *wsListener) Addr() net.Addr { return l.addr }

// ListenWebSocketTLS exposes websocket upgrades on path and returns a net.Listener of upgraded stream conns.
func ListenWebSocketTLS(addr string, tlsCfg *tls.Config, path string) (net.Listener, error) {
	baseLn, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	tlsLn := tls.NewListener(baseLn, tlsCfg)

	ctx, cancel := context.WithCancel(context.Background())
	ln := &wsListener{
		addr:   tlsLn.Addr(),
		conns:  make(chan net.Conn, 64),
		closed: make(chan struct{}),
		stop:   cancel,
	}

	upgrader := websocket.Upgrader{
		ReadBufferSize:    256 * 1024,
		WriteBufferSize:   256 * 1024,
		EnableCompression: false,
		CheckOrigin: func(_ *http.Request) bool {
			return true
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		select {
		case <-ln.closed:
			_ = c.Close()
		case ln.conns <- newWSConn(c):
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "not found\n")
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	go func() {
		<-ln.closed
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	go func() {
		err := srv.Serve(tlsLn)
		if err == nil || errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return
		}
		_ = ln.Close()
	}()

	return ln, nil
}

func ParseWSPath(p string) string {
	if p == "" {
		return "/ws"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	clean := path.Clean(p)
	if clean == "." {
		return "/ws"
	}
	return clean
}
