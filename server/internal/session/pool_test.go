package session

import (
	"net"
	"testing"
)

func TestAllocatePreferredAndRelease(t *testing.T) {
	p := NewIPPool()
	want := net.IPv4(10, 8, 1, 50)
	got := p.AllocatePreferred(want)
	if got == nil || !got.Equal(want) {
		t.Fatalf("preferred: got %v want %v", got, want)
	}
	if again := p.AllocatePreferred(want); again != nil {
		t.Fatalf("expected collision, got %v", again)
	}
	p.Release(want)
	if again := p.AllocatePreferred(want); again == nil || !again.Equal(want) {
		t.Fatalf("after release: got %v", again)
	}
}

func TestAllocateSkipsGateway(t *testing.T) {
	p := NewIPPool()
	ip := p.Allocate()
	if ip == nil {
		t.Fatal("empty pool")
	}
	if ip.Equal(ServerGateway()) {
		t.Fatal("allocated gateway")
	}
	host, ok := HostOf(ip)
	if !ok || host < firstHost {
		t.Fatalf("bad host %v", ip)
	}
}

func TestAllocateMany(t *testing.T) {
	p := NewIPPool()
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		ip := p.Allocate()
		if ip == nil {
			t.Fatalf("exhausted at %d", i)
		}
		s := ip.String()
		if _, dup := seen[s]; dup {
			t.Fatalf("duplicate %s", s)
		}
		seen[s] = struct{}{}
	}
}
