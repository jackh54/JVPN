package session

import (
	"net"
	"sync"
)

// IPPool hands out client addresses in 10.8.0.0/24 (server uses .1; clients .2–.254).
type IPPool struct {
	mu   sync.Mutex
	used map[byte]struct{}
}

func NewIPPool() *IPPool {
	return &IPPool{used: make(map[byte]struct{})}
}

// ServerGateway returns 10.8.0.1 (tunnel server side).
func ServerGateway() net.IP {
	return net.IPv4(10, 8, 0, 1)
}

// Allocate returns the next free client IP in 10.8.0.0/24 or nil if exhausted.
func (p *IPPool) Allocate() net.IP {
	p.mu.Lock()
	defer p.mu.Unlock()
	for last := byte(2); last <= 254; last++ {
		if _, ok := p.used[last]; ok {
			continue
		}
		p.used[last] = struct{}{}
		return net.IPv4(10, 8, 0, last)
	}
	return nil
}

// AllocatePreferred tries to reserve a specific client last octet in 10.8.0.0/24.
// Returns nil if the requested IP is invalid or already in use.
func (p *IPPool) AllocatePreferred(last byte) net.IP {
	if last < 2 || last > 254 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.used[last]; ok {
		return nil
	}
	p.used[last] = struct{}{}
	return net.IPv4(10, 8, 0, last)
}

func (p *IPPool) Release(ip net.IP) {
	ip4 := ip.To4()
	if ip4 == nil || ip4[0] != 10 || ip4[1] != 8 || ip4[2] != 0 {
		return
	}
	last := ip4[3]
	if last < 2 || last > 254 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.used, last)
}
