package session

import (
	"encoding/binary"
	"net"
	"sync"
)

const (
	// PrefixLength is the tunnel IPv4 prefix advertised to clients (10.8.0.0/16).
	PrefixLength = 16
	networkA     = 10
	networkB     = 8
	gatewayHost  = 1
	firstHost    = 2
	lastHost     = 0xfffe
)

// IPPool hands out client addresses in 10.8.0.0/16 (server uses 10.8.0.1).
type IPPool struct {
	mu     sync.Mutex
	used   map[uint16]struct{}
	cursor uint16
}

func NewIPPool() *IPPool {
	return &IPPool{used: make(map[uint16]struct{}), cursor: firstHost}
}

func ServerGateway() net.IP {
	return net.IPv4(networkA, networkB, 0, gatewayHost)
}

func ipFromHost(host uint16) net.IP {
	return net.IPv4(networkA, networkB, byte(host>>8), byte(host))
}

func HostOf(ip net.IP) (uint16, bool) {
	ip4 := ip.To4()
	if ip4 == nil || ip4[0] != networkA || ip4[1] != networkB {
		return 0, false
	}
	return binary.BigEndian.Uint16(ip4[2:4]), true
}

// Allocate returns the next free client IP or nil if exhausted.
func (p *IPPool) Allocate() net.IP {
	p.mu.Lock()
	defer p.mu.Unlock()
	for n := 0; n < int(lastHost-firstHost+1); n++ {
		h := p.cursor
		if h < firstHost || h > lastHost {
			h = firstHost
		}
		p.cursor = h + 1
		if _, ok := p.used[h]; ok {
			continue
		}
		p.used[h] = struct{}{}
		return ipFromHost(h)
	}
	return nil
}

// AllocatePreferred reserves a specific tunnel IP. Returns nil if invalid or in use.
func (p *IPPool) AllocatePreferred(ip net.IP) net.IP {
	host, ok := HostOf(ip)
	if !ok || host < firstHost || host > lastHost {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, used := p.used[host]; used {
		return nil
	}
	p.used[host] = struct{}{}
	return ipFromHost(host)
}

func (p *IPPool) Release(ip net.IP) {
	host, ok := HostOf(ip)
	if !ok || host < firstHost || host > lastHost {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, host)
}
