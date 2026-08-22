package route53

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/dns"
	"github.com/Neaox/overcast/internal/state"
)

// TestDNSIntegration_CreateZoneAndRecordThenResolve exercises the whole path
// end to end: a real HTTP request against Route 53's REST-XML API creates a
// hosted zone and a record set, and Overcast's own DNS resolver — wired to
// this same Service exactly as internal/router.New wires it — answers a
// query for that record. This is the "hosted zone provisions and reports
// success, and the record actually resolves" proof issue #1189 asks for.
func TestDNSIntegration_CreateZoneAndRecordThenResolve(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	svc := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	r := chi.NewRouter()
	svc.RegisterRoutes(r)
	httpSrv := httptest.NewServer(r)
	t.Cleanup(httpSrv.Close)

	// ---- CreateHostedZone -------------------------------------------------
	createBody := `<?xml version="1.0" encoding="UTF-8"?>
<CreateHostedZoneRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <Name>integration.example.</Name>
  <CallerReference>dns-integration-test</CallerReference>
</CreateHostedZoneRequest>`
	req, err := http.NewRequest(http.MethodPost, httpSrv.URL+"/2013-04-01/hostedzone", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("build CreateHostedZone request: %v", err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("CreateHostedZone status = %d, want 201", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	// Location looks like ".../2013-04-01/hostedzone/Z...". The bare zone ID
	// is the last path segment.
	bareZoneID := location[strings.LastIndex(location, "/")+1:]
	if bareZoneID == "" {
		t.Fatalf("no zone ID in Location header %q", location)
	}

	// ---- ChangeResourceRecordSets: create an A record ----------------------
	changeBody := `<?xml version="1.0" encoding="UTF-8"?>
<ChangeResourceRecordSetsRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <ChangeBatch>
    <Changes>
      <Change>
        <Action>CREATE</Action>
        <ResourceRecordSet>
          <Name>app.integration.example.</Name>
          <Type>A</Type>
          <TTL>42</TTL>
          <ResourceRecords>
            <ResourceRecord><Value>10.5.5.5</Value></ResourceRecord>
          </ResourceRecords>
        </ResourceRecordSet>
      </Change>
    </Changes>
  </ChangeBatch>
</ChangeResourceRecordSetsRequest>`
	changeReq, err := http.NewRequest(http.MethodPost,
		httpSrv.URL+"/2013-04-01/hostedzone/"+bareZoneID+"/rrset", strings.NewReader(changeBody))
	if err != nil {
		t.Fatalf("build ChangeResourceRecordSets request: %v", err)
	}
	changeReq.Header.Set("Content-Type", "application/xml")
	changeResp, err := http.DefaultClient.Do(changeReq)
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets: %v", err)
	}
	defer changeResp.Body.Close()
	if changeResp.StatusCode != http.StatusOK {
		t.Fatalf("ChangeResourceRecordSets status = %d, want 200", changeResp.StatusCode)
	}

	// ---- Wire the DNS resolver to this same service, as router.New does ---
	dnsSrv := dns.NewServer("127.0.0.1:0", dns.NewZone(netip.Addr{}), nil)
	dnsSrv.SetRoute53(svc)
	if err := dnsSrv.Listen(); err != nil {
		t.Fatalf("dns Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); dnsSrv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("dns Serve did not return within 5s")
		}
	})

	// ---- The record just created over HTTP must now resolve ---------------
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, dnsSrv.UDPAddr())
		},
	}
	lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer lookupCancel()
	addrs, err := resolver.LookupHost(lookupCtx, "app.integration.example")
	if err != nil {
		t.Fatalf("LookupHost(app.integration.example): %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "10.5.5.5" {
		t.Fatalf("LookupHost = %v, want [10.5.5.5]", addrs)
	}

	// A name Route 53 never heard of must not resolve through this same
	// server — it is authoritative only for zones it actually holds.
	if _, err := resolver.LookupHost(lookupCtx, "never-created.integration.example"); err == nil {
		t.Fatalf("LookupHost(never-created...) unexpectedly succeeded")
	}
}
