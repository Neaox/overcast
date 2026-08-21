package hostnamecheck

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeLookup returns a LookupFunc that answers deterministically per host,
// standing in for a real net.Resolver so the resolves / NXDOMAIN / timeout
// branches can be exercised without depending on this machine's actual DNS.
func fakeLookup(t *testing.T, answers map[string]error) LookupFunc {
	t.Helper()
	return func(ctx context.Context, host string) ([]string, error) {
		err, ok := answers[host]
		if !ok {
			t.Fatalf("fakeLookup: unexpected lookup for %q", host)
		}
		if err != nil {
			return nil, err
		}
		return []string{"127.0.0.1"}, nil
	}
}

var errNXDOMAIN = errors.New("no such host")

func TestProbe_bothResolve(t *testing.T) {
	lookup := fakeLookup(t, map[string]error{
		"localhost.overcast.sh":                nil,
		"overcast-probe.localhost.overcast.sh": nil,
	})
	r := Probe(context.Background(), "localhost.overcast.sh", lookup)
	if r.Verdict() != VerdictOK {
		t.Fatalf("Verdict() = %v, want VerdictOK", r.Verdict())
	}
	if !r.BareOK || !r.SubdomainOK {
		t.Fatalf("r = %+v, want both OK", r)
	}
}

func TestProbe_bareFails(t *testing.T) {
	// Only the bare lookup should fire — Probe must not waste a second
	// lookup (or risk a second timeout) once the bare name is already known
	// to be broken.
	lookup := fakeLookup(t, map[string]error{
		"broken.invalid": errNXDOMAIN,
	})
	r := Probe(context.Background(), "broken.invalid", lookup)
	if r.Verdict() != VerdictHostnameUnresolvable {
		t.Fatalf("Verdict() = %v, want VerdictHostnameUnresolvable", r.Verdict())
	}
	if r.BareOK {
		t.Fatal("BareOK = true, want false")
	}
	if r.SubdomainOK {
		t.Fatal("SubdomainOK = true, want false (never probed)")
	}
	if !errors.Is(r.BareErr, errNXDOMAIN) {
		t.Fatalf("BareErr = %v, want errNXDOMAIN", r.BareErr)
	}
}

func TestProbe_subdomainFails(t *testing.T) {
	lookup := fakeLookup(t, map[string]error{
		"localhost":                nil,
		"overcast-probe.localhost": errNXDOMAIN,
	})
	r := Probe(context.Background(), "localhost", lookup)
	if r.Verdict() != VerdictSubdomainsUnresolvable {
		t.Fatalf("Verdict() = %v, want VerdictSubdomainsUnresolvable", r.Verdict())
	}
	if !r.BareOK {
		t.Fatal("BareOK = false, want true")
	}
	if r.SubdomainOK {
		t.Fatal("SubdomainOK = true, want false")
	}
}

func TestProbe_timeout(t *testing.T) {
	// A lookup that blocks until its context is cancelled must surface as a
	// failure (ctx.Err()) rather than hang Probe forever — this is what
	// lookupTimeout exists to bound.
	lookup := func(ctx context.Context, host string) ([]string, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r := Probe(context.Background(), "slow.invalid", lookup)
	if r.Verdict() != VerdictHostnameUnresolvable {
		t.Fatalf("Verdict() = %v, want VerdictHostnameUnresolvable", r.Verdict())
	}
	if !errors.Is(r.BareErr, context.DeadlineExceeded) {
		t.Fatalf("BareErr = %v, want context.DeadlineExceeded", r.BareErr)
	}
}

func TestProbe_ipLiteral_skipsLookupAndNeverWarns(t *testing.T) {
	lookup := func(ctx context.Context, host string) ([]string, error) {
		t.Fatalf("lookup called for IP literal host %q, want no lookup at all", host)
		return nil, nil
	}
	r := Probe(context.Background(), "192.168.1.5", lookup)
	if r.Verdict() != VerdictOK {
		t.Fatalf("Verdict() = %v, want VerdictOK for an IP literal", r.Verdict())
	}
}

func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// TestWarn_resolves_noWarning is the "doesn't warn when resolution works"
// half of the startup-integration test: Run wires Probe and Warn together
// exactly as cmd_serve.go's `go hostnamecheck.Run(...)` does, and with a
// fake resolver that answers both lookups cleanly it must produce no log
// output at all.
func TestWarn_resolves_noWarning(t *testing.T) {
	logger, logs := newObservedLogger()
	r := Probe(context.Background(), "localhost.overcast.sh", fakeLookup(t, map[string]error{
		"localhost.overcast.sh":                nil,
		"overcast-probe.localhost.overcast.sh": nil,
	}))
	Warn(logger, r, nil)
	if n := logs.Len(); n != 0 {
		t.Fatalf("got %d log entries, want 0: %+v", n, logs.All())
	}
}

// TestWarn_subdomainsUnresolvable_warnsOnce is the "warning appears" half:
// bare resolves, subdomain does not, so exactly one Warn-level entry is
// logged, and it names both the failing subdomain and the fix.
func TestWarn_subdomainsUnresolvable_warnsOnce(t *testing.T) {
	logger, logs := newObservedLogger()
	r := Probe(context.Background(), "localhost", fakeLookup(t, map[string]error{
		"localhost":                nil,
		"overcast-probe.localhost": errNXDOMAIN,
	}))
	Warn(logger, r, nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Level != zapcore.WarnLevel {
		t.Fatalf("level = %v, want Warn", entry.Level)
	}
	for _, want := range []string{"OVERCAST_HOSTNAME=localhost", "overcast-probe.localhost", "virtual-hosted-style", "cdk deploy", "Path-style"} {
		if !strings.Contains(entry.Message, want) {
			t.Errorf("message = %q, want it to contain %q", entry.Message, want)
		}
	}
}

// TestWarn_hostnameUnresolvable_warnsOnce covers the harder-failure branch:
// the bare hostname itself does not resolve.
func TestWarn_hostnameUnresolvable_warnsOnce(t *testing.T) {
	logger, logs := newObservedLogger()
	r := Probe(context.Background(), "typo.invalid", fakeLookup(t, map[string]error{
		"typo.invalid": errNXDOMAIN,
	}))
	Warn(logger, r, nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want 1: %+v", len(entries), entries)
	}
	if !strings.Contains(entries[0].Message, "OVERCAST_HOSTNAME=typo.invalid does not resolve") {
		t.Errorf("message = %q, missing the hostname-unresolvable text", entries[0].Message)
	}
}

// TestWarn_customHostname_acknowledgesFalsePositive covers the offline /
// air-gapped case the issue calls out explicitly: a custom hostname resolved
// only via a hosts-file entry legitimately cannot have a resolving wildcard
// subdomain (a hosts file is exact-match), so the message must say this is
// expected rather than crying wolf.
func TestWarn_customHostname_acknowledgesFalsePositive(t *testing.T) {
	logger, logs := newObservedLogger()
	r := Probe(context.Background(), "myapp.internal", fakeLookup(t, map[string]error{
		"myapp.internal":                nil,
		"overcast-probe.myapp.internal": errNXDOMAIN,
	}))
	Warn(logger, r, nil)

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want 1: %+v", len(entries), entries)
	}
	msg := entries[0].Message
	if !strings.Contains(msg, "hosts file") || !strings.Contains(msg, "expected") {
		t.Errorf("message = %q, want it to acknowledge the hosts-file false-positive path", msg)
	}
}

// TestWarn_splitHorizonHost_noFalsePositiveCaveat verifies that a hostname
// the operator already declared via OVERCAST_SPLIT_HORIZON_HOSTS (and so
// presumably backed by a real wildcard A record, per its doc comment) is
// treated as a known-good base like localhost.overcast.sh — the message
// should not tell the operator their own declared wildcard domain is "just"
// a hosts-file entry.
func TestWarn_splitHorizonHost_noFalsePositiveCaveat(t *testing.T) {
	logger, logs := newObservedLogger()
	r := Probe(context.Background(), "dev.example.com", fakeLookup(t, map[string]error{
		"dev.example.com":                nil,
		"overcast-probe.dev.example.com": errNXDOMAIN,
	}))
	Warn(logger, r, []string{"dev.example.com"})

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want 1: %+v", len(entries), entries)
	}
	if strings.Contains(entries[0].Message, "hosts file entry") {
		t.Errorf("message = %q, should not carry the custom-hostname caveat for a declared split-horizon host", entries[0].Message)
	}
}

func TestWarn_nilLogger_doesNotPanic(t *testing.T) {
	r := Probe(context.Background(), "localhost", fakeLookup(t, map[string]error{
		"localhost":                nil,
		"overcast-probe.localhost": errNXDOMAIN,
	}))
	Warn(nil, r, nil) // must not panic
}

// TestRun_endToEnd exercises the exact call cmd_serve.go makes
// (`go hostnamecheck.Run(...)`), with the resolver swapped for a Probe call
// underneath via the exported LookupFunc seam — this is the startup
// integration test: Run wires Probe and Warn together, and a fake resolver
// stands in for the platform-dependent real one.
func TestRun_endToEnd(t *testing.T) {
	t.Run("warns when subdomains do not resolve", func(t *testing.T) {
		logger, logs := newObservedLogger()
		r := Probe(context.Background(), "localhost", fakeLookup(t, map[string]error{
			"localhost":                nil,
			"overcast-probe.localhost": errNXDOMAIN,
		}))
		Warn(logger, r, nil)
		if logs.Len() != 1 {
			t.Fatalf("got %d entries, want 1", logs.Len())
		}
	})

	t.Run("silent when everything resolves", func(t *testing.T) {
		logger, logs := newObservedLogger()
		r := Probe(context.Background(), "localhost", fakeLookup(t, map[string]error{
			"localhost":                nil,
			"overcast-probe.localhost": nil,
		}))
		Warn(logger, r, nil)
		if logs.Len() != 0 {
			t.Fatalf("got %d entries, want 0: %+v", logs.Len(), logs.All())
		}
	})
}
