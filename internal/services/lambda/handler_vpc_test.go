package lambda

import (
	"context"
	"sync"
	"testing"
)

type testVPCResolver struct{}

func (testVPCResolver) VpcIDForSubnet(context.Context, string) string { return "vpc-test" }

func (testVPCResolver) VPCNetworkStatus(context.Context, string) string { return "ok" }

func (testVPCResolver) DockerNetworkForVpc(context.Context, string) string { return "network-test" }

func TestHandlerVPCResolver_concurrentAccess(t *testing.T) {
	// Given: a handler with resolver accessors used by router wiring and requests
	h := &Handler{}
	resolver := testVPCResolver{}

	// When: one goroutine wires the resolver while another reads it
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			h.setVPCResolver(resolver)
		}()
		go func() {
			defer wg.Done()
			_ = h.getVPCResolver()
		}()
	}
	wg.Wait()

	// Then: the final resolver is available
	if h.getVPCResolver() == nil {
		t.Fatal("expected resolver to be set")
	}
}
