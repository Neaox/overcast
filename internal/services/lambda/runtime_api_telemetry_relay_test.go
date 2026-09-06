package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
	"go.uber.org/zap"
)

// runtime_api_telemetry_relay_test.go — how a Telemetry API batch gets from the
// emulator to an extension's listener.
//
// It gets there over the channel the execution environment's own init opened,
// and it is the init that makes the POST, from inside the sandbox — which is
// where AWS's platform makes it from too. Nothing in this path connects to a
// container. That is the whole point: the previous arrangement rewrote the
// extension's loopback destination to the container's bridge IP and posted
// there from this process, which only works where the host and the daemon share
// a kernel, and so delivered nothing at all on Docker Desktop (#1799).
//
// The tests here drive the real HTTP surfaces on both sides: a per-environment
// listener, an extension registering and subscribing through it, and a stand-in
// for the init long-polling initproto.TelemetryPath.

// telemetryRelayEnv is a Runtime API server with one execution environment
// registered on its own per-environment listener — the production shape, and
// the one that has a relay.
type telemetryRelayEnv struct {
	srv         *RuntimeAPIServer
	listener    *containerListener
	addr        string // the environment's endpoint, as the container would dial it
	containerIP string
}

func newTelemetryRelayEnv(t *testing.T) *telemetryRelayEnv {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewRuntimeAPIServerFromListener(ln, ln.Addr().String(), zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	listener, err := srv.AddContainerListener()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	// The address Docker would report for the container. It is identity here
	// and nothing more — if anything in the delivery path treated it as a
	// destination, these tests would be posting at whatever happens to be
	// listening on this host, which is the bug they exist to prevent.
	const containerIP = "172.18.0.9"
	srv.RegisterContainerConfig(containerIP, runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	listener.Attach(containerIP)

	return &telemetryRelayEnv{srv: srv, listener: listener, addr: listener.addr, containerIP: containerIP}
}

// poll is one exchange on the init's channel: report the previous delivery's
// outcome and collect the next. It returns false when the server answered with
// nothing.
func (e *telemetryRelayEnv) poll(t *testing.T, result *initproto.TelemetryResult) (initproto.TelemetryDelivery, bool) {
	t.Helper()
	body, err := json.Marshal(initproto.TelemetryPoll{Result: result})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+e.addr+initproto.TelemetryPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("telemetry poll: %v", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return initproto.TelemetryDelivery{}, false
	case http.StatusOK:
		var delivery initproto.TelemetryDelivery
		if err := json.NewDecoder(resp.Body).Decode(&delivery); err != nil {
			t.Fatalf("undecodable delivery: %v", err)
		}
		return delivery, true
	default:
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("telemetry poll status = %d: %s", resp.StatusCode, payload)
		return initproto.TelemetryDelivery{}, false
	}
}

// subscribeThroughTheEnvironment registers an extension and subscribes it to
// platform records at destinationURI, over the environment's own endpoint, the
// way one inside the container does.
func (e *telemetryRelayEnv) subscribeThroughTheEnvironment(t *testing.T, destinationURI string) string {
	t.Helper()
	return registerAndSubscribe(t, e.addr, destinationURI)
}

// TestTelemetryDelivery_goesThroughTheEnvironmentsInit is the change's subject:
// a subscriber's batch is handed to the container's init, carrying the
// destination exactly as it was subscribed, and no connection is made to the
// container.
func TestTelemetryDelivery_goesThroughTheEnvironmentsInit(t *testing.T) {
	env := newTelemetryRelayEnv(t)

	// A loopback destination — what an extension inside a sandbox subscribes,
	// and the shape that used to be rewritten to the container's bridge IP.
	const destination = "http://127.0.0.1:9999"
	env.subscribeThroughTheEnvironment(t, destination)

	// The subscription itself publishes platform.telemetrySubscription, so a
	// batch is already on its way; take deliveries until the record under test
	// arrives, exactly as the init's loop does.
	const event = `{"time":"2026-09-06T00:00:00.000Z","type":"platform.initRuntimeDone","record":{"initializationType":"on-demand","phase":"init","status":"success"}}`
	env.srv.PublishInitPhaseRecord(env.containerIP, event)

	var result *initproto.TelemetryResult
	deadline := time.Now().Add(15 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("the init's channel was never handed the record")
		}
		delivery, ok := env.poll(t, result)
		if !ok {
			result = nil
			continue
		}
		result = &initproto.TelemetryResult{ID: delivery.ID}
		if delivery.URI != destination {
			t.Fatalf("the delivery names destination %q, want %q — the host must not rewrite what the extension subscribed", delivery.URI, destination)
		}
		if delivery.ID == 0 {
			t.Error("the delivery has no ID, so its result can never be matched to it")
		}
		var batch []map[string]any
		if err := json.Unmarshal(delivery.Body, &batch); err != nil {
			t.Fatalf("the delivery body is not a batch of events: %v (%s)", err, delivery.Body)
		}
		for _, got := range batch {
			if got["type"] != "platform.initRuntimeDone" {
				continue
			}
			// The envelope the init carries is the AWS one, untouched: the
			// init never parses it, so what the extension receives is what
			// PublishInitPhaseRecord produced.
			record, _ := got["record"].(map[string]any)
			if record["status"] != "success" || record["phase"] != "init" {
				t.Errorf("the record the init was handed = %#v", got)
			}
			return
		}
	}
}

// TestTelemetryDelivery_neverDialsTheContainer is the constraint the transport
// change exists to satisfy, asserted where it can actually be observed: with a
// server listening at the address the old code would have rewritten the
// destination to, a delivery must not arrive there.
func TestTelemetryDelivery_neverDialsTheContainer(t *testing.T) {
	// Stand a listener up on this host and register it as the container's
	// address. On the old path the loopback destination was rewritten to
	// <container IP>:<port> and posted from this process, so this is precisely
	// what a delivery would have hit.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var dialed int
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			dialed++
			mu.Unlock()
			_ = conn.Close()
		}
	}()

	env := newTelemetryRelayEnv(t)
	// Re-point the environment at the loopback address the listener is on, so
	// "the container's address" is somewhere a POST could actually land.
	env.srv.RegisterContainerConfig(host, runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	env.listener.Attach(host)
	env.containerIP = host

	env.subscribeThroughTheEnvironment(t, "http://127.0.0.1:"+port)
	env.srv.PublishInitPhaseRecord(env.containerIP, `{"time":"2026-09-06T00:00:00.000Z","type":"platform.initStart","record":{"phase":"init"}}`)

	// Give the batch every chance to be posted the old way before anything
	// collects it: past the subscription's default one-second buffering
	// timeout, so the batch has been cut and handed to a delivery worker.
	time.Sleep(1500 * time.Millisecond)
	mu.Lock()
	opened := dialed
	mu.Unlock()
	if opened != 0 {
		t.Fatalf("the delivery path opened %d connection(s) to the container's address", opened)
	}

	// And it is waiting on the channel, not lost: the init collects it, with
	// the destination exactly as subscribed.
	delivery, ok := env.poll(t, nil)
	if !ok {
		t.Fatal("the init's channel was never handed the batch")
	}
	if delivery.URI != "http://127.0.0.1:"+port {
		t.Errorf("the delivery names %q, want the subscribed destination", delivery.URI)
	}
}

// TestTelemetryDelivery_aFailedRelayDeliveryIsRetriedThenReported keeps the
// host's semantics where they were: the init reporting a destination it could
// not reach is one failed attempt, not a lost record, and only an endpoint that
// stays dead costs the batch — reported to the subscriber as
// platform.logsDropped on the next batch, exactly as before.
func TestTelemetryDelivery_aFailedRelayDeliveryIsRetriedThenReported(t *testing.T) {
	env := newTelemetryRelayEnv(t)
	env.subscribeThroughTheEnvironment(t, "http://127.0.0.1:9999")

	env.srv.PublishInitPhaseRecord(env.containerIP, `{"time":"2026-09-06T00:00:00.000Z","type":"platform.initStart","record":{"phase":"init"}}`)

	// Refuse everything the init is handed, the way a sandbox with no listener
	// on that port does, and count how many times the same batch comes back.
	attempts := 0
	var result *initproto.TelemetryResult
	deadline := time.Now().Add(20 * time.Second)
	for attempts < extensionLogDeliveryAttempts && time.Now().Before(deadline) {
		delivery, ok := env.poll(t, result)
		if !ok {
			result = nil
			continue
		}
		if strings.Contains(string(delivery.Body), "platform.initStart") {
			attempts++
		}
		result = &initproto.TelemetryResult{ID: delivery.ID, Error: "connection refused"}
	}
	if attempts != extensionLogDeliveryAttempts {
		t.Fatalf("the batch was offered to the init %d times, want %d — the host's retry must not change with the transport", attempts, extensionLogDeliveryAttempts)
	}

	// The drop is now the subscriber's business: the next batch opens with it.
	env.srv.PublishInitPhaseRecord(env.containerIP, `{"time":"2026-09-06T00:00:01.000Z","type":"platform.initReport","record":{"status":"success"}}`)
	for time.Now().Before(deadline) {
		delivery, ok := env.poll(t, result)
		result = nil
		if !ok {
			continue
		}
		result = &initproto.TelemetryResult{ID: delivery.ID}
		if strings.Contains(string(delivery.Body), "platform.logsDropped") {
			return
		}
	}
	t.Fatal("the destination was never told about the batch that was dropped")
}

// TestTelemetryBuffers_areNotSharedBetweenEnvironments pins what stopped being
// free once destinations are no longer rewritten per container: two
// environments' extensions routinely subscribe the same loopback address, and
// their batches must not fall into one buffer and one delivery.
func TestTelemetryBuffers_areNotSharedBetweenEnvironments(t *testing.T) {
	srv, _ := newRuntimeAPITestServer(t)
	const uri = "http://127.0.0.1:9999"
	first := telemetryTarget{containerIP: "172.18.0.9", uri: uri, buffering: bufferingConfig{Timeout: time.Hour, MaxBytes: 1 << 20, MaxItems: 1000}}
	second := telemetryTarget{containerIP: "172.18.0.10", uri: uri, buffering: first.buffering}

	firstBuffer, secondBuffer := srv.telemetryBufferFor(first), srv.telemetryBufferFor(second)
	if firstBuffer == secondBuffer {
		t.Fatal("two execution environments subscribing the same loopback destination share one buffer")
	}
	if again := srv.telemetryBufferFor(first); again != firstBuffer {
		t.Fatal("one environment's destination resolves to a different buffer each time")
	}
}
