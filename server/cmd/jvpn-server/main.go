package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jackh54/jvpn-server/internal/bootstrap"
	"github.com/jackh54/jvpn-server/internal/dashboard"
	"github.com/jackh54/jvpn-server/internal/server"
	"github.com/jackh54/jvpn-server/internal/session"
	"github.com/songgao/water"
)

func tuneTCP(conn net.Conn) {
	var tcp *net.TCPConn
	switch c := conn.(type) {
	case *net.TCPConn:
		tcp = c
	case *tls.Conn:
		if nc := c.NetConn(); nc != nil {
			if t, ok := nc.(*net.TCPConn); ok {
				tcp = t
			}
		}
	}
	if tcp == nil {
		return
	}
	_ = tcp.SetReadBuffer(4 * 1024 * 1024)
	_ = tcp.SetWriteBuffer(4 * 1024 * 1024)
}

func defaultTunName() string {
	if runtime.GOOS == "darwin" {
		return "utun9"
	}
	return "jvpn0"
}

func main() {
	listen := flag.String("listen", ":443", "TLS listen address (host:port)")
	transport := flag.String("transport", "tcp", "transport mode: tcp (default) or ws (websocket over TLS; also serves UDP-over-TCP)")
	wsPath := flag.String("ws-path", "/ws", "when -transport=ws, HTTP path to upgrade websocket tunnel")
	uotPath := flag.String("uot-path", "/dns-query", "when -transport=ws, HTTP path for experimental UDP-over-TCP (DoH-style) tunnel")
	dataDir := flag.String("data-dir", "jvpn-data", "when using auto TLS/token, store files here (created on first run)")
	certFile := flag.String("cert", "", "TLS certificate PEM (omit with key and token-file to auto-generate under -data-dir)")
	keyFile := flag.String("key", "", "TLS private key PEM")
	tokenFile := flag.String("token-file", "", "shared secret file, single line (omit with cert and key to auto-generate)")
	tunName := flag.String("tun-name", defaultTunName(), "TUN interface name (Linux: e.g. jvpn0; macOS: e.g. utun9)")
	setup := flag.Bool("setup-tun", false, "assign 10.8.0.1/24 on the TUN (Linux: ip command; macOS: ifconfig; requires root)")
	setupNAT := flag.Bool("setup-nat", false, "Linux only: ip_forward=1 + iptables MASQUERADE/FORWARD for 10.8.0.0/24 (root; WAN from `ip route get 8.8.8.8`; use with -setup-tun)")
	adminListen := flag.String("admin-listen", "", "optional admin dashboard bind address (recommended 127.0.0.1:18080 and access via SSH port-forward)")
	adminUser := flag.String("admin-user", "", "admin dashboard basic-auth username (required when -admin-listen is set)")
	adminPass := flag.String("admin-pass", "", "admin dashboard basic-auth password (required when -admin-listen is set)")
	flag.Parse()

	certPath := *certFile
	keyPath := *keyFile
	tokenPath := *tokenFile
	autoTLS := certPath == "" && keyPath == "" && tokenPath == ""
	if autoTLS {
		certPath = filepath.Join(*dataDir, "tls.crt")
		keyPath = filepath.Join(*dataDir, "tls.key")
		tokenPath = filepath.Join(*dataDir, "token")
	} else if certPath == "" || keyPath == "" || tokenPath == "" {
		log.Fatal("either omit -cert, -key, and -token-file together (auto mode) or set all three")
	}

	bootstrapDataDir := ""
	if autoTLS {
		bootstrapDataDir = *dataDir
	}
	if err := bootstrap.Ensure(bootstrapDataDir, certPath, keyPath, tokenPath); err != nil {
		log.Fatalf("bootstrap: %v", err)
	}

	token, err := server.LoadToken(tokenPath)
	if err != nil {
		log.Fatalf("token: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatalf("tls cert: %v", err)
	}
	tlsCfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}

	cfg := water.Config{DeviceType: water.TUN}
	cfg.Name = *tunName
	ifce, err := water.New(cfg)
	if err != nil {
		if runtime.GOOS == "linux" && (errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file")) {
			log.Fatalf("tun: %v — /dev/net/tun is missing or inaccessible (common in default Pterodactyl/game-server containers). Use a normal VPS with root, or Docker with --device=/dev/net/tun and cap_add: NET_ADMIN (see README “Docker / Pterodactyl”).", err)
		}
		log.Fatalf("tun: %v", err)
	}
	log.Printf("TUN %s created", ifce.Name())

	if *setup {
		if err := configureTUN(ifce.Name()); err != nil {
			log.Fatalf("setup-tun: %v", err)
		}
	}
	if *setupNAT {
		if err := configureNAT(ifce.Name()); err != nil {
			log.Fatalf("setup-nat: %v", err)
		}
	}
	if *setup && runtime.GOOS == "linux" && !*setupNAT {
		if b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err == nil && strings.TrimSpace(string(b)) != "1" {
			log.Printf("warning: net.ipv4.ip_forward is not 1 — clients usually cannot reach the internet without forwarding + NAT (see README; try -setup-nat)")
		}
	}

	hub := server.NewHub()
	hub.SetTUNReady(true)
	pool := session.NewIPPool()
	go hub.RunTUNReader(ifce)
	if *adminListen != "" {
		if *adminUser == "" || *adminPass == "" {
			log.Fatal("admin dashboard requires -admin-user and -admin-pass when -admin-listen is set")
		}
		go func() {
			log.Printf("admin dashboard listening on %s (use SSH tunnel; basic auth enabled)", *adminListen)
			if err := dashboard.Start(*adminListen, *adminUser, *adminPass, hub); err != nil {
				log.Fatalf("admin dashboard: %v", err)
			}
		}()
	}

	var ln net.Listener
	switch strings.ToLower(*transport) {
	case "tcp":
		ln, err = tls.Listen("tcp", *listen, tlsCfg)
		if err != nil {
			log.Fatalf("listen tcp: %v", err)
		}
		log.Printf("jvpn-server listening on %s (TLS 1.3+, transport=tcp)", *listen)
	case "ws":
		path := server.ParseWSPath(*wsPath)
		uot := server.ParseUoTPath(*uotPath)
		ln, err = server.ListenWebSocketTLS(*listen, tlsCfg, path, uot)
		if err != nil {
			log.Fatalf("listen ws: %v", err)
		}
		log.Printf("jvpn-server listening on %s (TLS 1.3+, transport=ws, path=%s, uot=%s)", *listen, path, uot)
	default:
		log.Fatalf("invalid -transport=%q (expected tcp or ws)", *transport)
	}

	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go func(conn net.Conn) {
			tuneTCP(conn)
			server.ServeConn(conn, hub, pool, token, ifce)
		}(c)
	}
}
