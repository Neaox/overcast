package dns

import (
	"net"
	"net/netip"
	"sync"
)

// maxLocatorCache bounds the peer cache. Container addresses are recycled as
// functions scale, so the set is small in practice; the cap only guards against
// unbounded growth over a very long-lived process.
const maxLocatorCache = 1024

// Locator reports which of Overcast's own addresses is reachable from a peer.
//
// It exists because Overcast is attached to more than one Docker network — the
// control plane, the default data plane, and a network per VPC it has work in —
// and therefore has more than one address. A container on one network cannot
// reach Overcast's address on another, so the right answer to "where is
// localhost.overcast.sh" depends on who is asking.
type Locator interface {
	// AddrFor returns Overcast's address as seen from peer, or the zero Addr
	// when it cannot be determined.
	AddrFor(peer netip.Addr) netip.Addr
}

// KernelLocator answers from the host's routing table: it asks the kernel which
// source address it would use to reach the peer. That is exactly the address
// the peer can reach back on, and it stays correct as Docker networks come and
// go — no interface enumeration to refresh, no configuration to drift.
//
// The zero value is ready to use and safe for concurrent use.
type KernelLocator struct {
	mu    sync.RWMutex
	cache map[netip.Addr]netip.Addr
}

// AddrFor implements Locator.
func (l *KernelLocator) AddrFor(peer netip.Addr) netip.Addr {
	if l == nil || !peer.IsValid() {
		return netip.Addr{}
	}
	peer = peer.Unmap()

	l.mu.RLock()
	addr, ok := l.cache[peer]
	l.mu.RUnlock()
	if ok {
		return addr
	}

	addr = sourceAddrTo(peer)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cache == nil {
		l.cache = make(map[netip.Addr]netip.Addr)
	}
	if len(l.cache) >= maxLocatorCache {
		clear(l.cache)
	}
	l.cache[peer] = addr
	return addr
}

// sourceAddrTo returns the local address the kernel would send from to reach
// peer. Connecting a UDP socket transmits nothing — it only binds the route —
// so this is a routing-table lookup rather than network traffic.
//
// Loopback and unspecified results are discarded: they are what a host-local
// caller produces, and handing "127.0.0.1" to a container would name the
// container itself.
func sourceAddrTo(peer netip.Addr) netip.Addr {
	conn, err := net.Dial("udp", net.JoinHostPort(peer.String(), "53"))
	if err != nil {
		return netip.Addr{}
	}
	defer conn.Close()

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}
	}
	addr, ok := netip.AddrFromSlice(udpAddr.IP)
	if !ok {
		return netip.Addr{}
	}
	if addr = addr.Unmap(); !addr.Is4() || addr.IsLoopback() || addr.IsUnspecified() {
		return netip.Addr{}
	}
	return addr
}
