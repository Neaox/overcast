package lambda

// telemetry_relay.go — the host end of the in-container init's Telemetry API
// channel.
//
// The Telemetry API and the Logs API are the one Lambda surface whose traffic
// runs host-to-sandbox: an extension stands its listener up inside the
// execution environment, subscribes a loopback destination, and AWS's platform
// POSTs the record batches to it from in there. Overcast used to make that POST
// itself, rewriting the loopback to the container's bridge IP — which needs
// this process and the daemon to share a kernel. Docker Desktop runs the engine
// in a VM with no route back, so on Windows and macOS every delivery timed out
// and extension telemetry was inert (#1799).
//
// So the delivery turns around: the init holds one long poll open here, this
// hands it a batch when a subscriber's is cut, and the init POSTs it inside the
// sandbox. Nothing dials into a container. What does *not* change is everything
// above the transport — the buffering bounds, the batch cut, the retry, the
// shedding, the platform.logsDropped accounting and the record envelopes all
// stay exactly where they were, on the side that owns the AWS schema. A relay
// attempt takes the place of one HTTP POST and reports the same two outcomes:
// the destination answered, or the transport failed.
//
// See docs/plans/lambda-in-container-init.md § 3.5 and initproto.TelemetryPath.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

const (
	// telemetryPollWait is how long a poll with nothing to deliver is held open
	// before it is answered 204 and the init asks again. It is long enough that
	// an idle environment is one parked request rather than a poll loop, and
	// short enough that a stuck connection is replaced within it.
	telemetryPollWait = 20 * time.Second

	// telemetryRelayAttempt bounds one delivery attempt made through a relay:
	// handing the batch to the init, its POST inside the sandbox, and the
	// result coming back on the next poll. It is wider than the one second the
	// init gives the destination itself, so a destination that is merely slow
	// is judged by the init's timeout rather than by a race between the two
	// sides' budgets. The direct-post path keeps its own second.
	telemetryRelayAttempt = 2 * time.Second
)

// telemetryRelay is one execution environment's telemetry delivery channel.
//
// It is a rendezvous, not a queue: the batching in telemetryBuffer and the
// bounded s.logsDeliveries queue in front of it already decide what is sent and
// what is shed, and a second buffer here would be a second place for records to
// pile up unaccounted. A delivery worker hands one attempt over and waits for
// the init's verdict, exactly as it used to wait for an HTTP response.
type telemetryRelay struct {
	// handoff carries an attempt from a delivery worker to the init's parked
	// poll. Unbuffered: a send succeeds only when a poll is there to take it,
	// which is what makes "the init is not collecting" a timeout on the
	// attempt rather than a record that vanished into a channel.
	handoff chan *telemetryAttempt

	mu sync.Mutex
	// nextID numbers the attempts so a result arriving on the next poll can be
	// matched to the worker waiting for it.
	nextID uint64
	// inflight is the attempts the init has been handed and not yet reported
	// on. An entry is removed by the result, or by the worker giving up.
	inflight map[uint64]*telemetryAttempt
}

// telemetryAttempt is one delivery in flight between a worker and the init.
type telemetryAttempt struct {
	id       uint64
	delivery extensionLogDelivery
	// done carries the verdict. Buffered so completing an attempt never blocks
	// on a worker that has already walked away.
	done chan error
	// abandoned is set when the worker gave up. It is read where the attempt
	// is filed as in-flight, under the same lock, because giving up can happen
	// between the handoff and the filing — and an attempt filed after that
	// would be waited on by nobody and removed by nothing. Guarded by
	// telemetryRelay.mu.
	abandoned bool
}

func newTelemetryRelay() *telemetryRelay {
	return &telemetryRelay{
		handoff:  make(chan *telemetryAttempt),
		inflight: make(map[uint64]*telemetryAttempt),
	}
}

// deliver hands one batch to the container's init and waits for the outcome.
// The error it returns is what the delivery worker's retry loop sees, so a
// relay that cannot be reached costs the same attempts and produces the same
// platform.logsDropped accounting as a destination that refused the connection.
func (r *telemetryRelay) deliver(ctx context.Context, shutdown <-chan struct{}, delivery extensionLogDelivery) error {
	r.mu.Lock()
	r.nextID++
	attempt := &telemetryAttempt{id: r.nextID, delivery: delivery, done: make(chan error, 1)}
	r.mu.Unlock()

	select {
	case r.handoff <- attempt:
	case <-ctx.Done():
		return fmt.Errorf("the container's init did not collect the delivery: %w", ctx.Err())
	case <-shutdown:
		return errors.New("the runtime API is shutting down")
	}

	select {
	case err := <-attempt.done:
		return err
	case <-ctx.Done():
		r.abandon(attempt)
		return fmt.Errorf("the container's init did not report the delivery: %w", ctx.Err())
	case <-shutdown:
		r.abandon(attempt)
		return errors.New("the runtime API is shutting down")
	}
}

// take waits for an attempt to hand to a parked poll. It reports false when the
// wait elapsed, the request went away or the server is stopping — all of which
// mean "answer the poll with nothing".
func (r *telemetryRelay) take(ctx context.Context, wait <-chan time.Time, shutdown <-chan struct{}) (*telemetryAttempt, bool) {
	select {
	case attempt := <-r.handoff:
		r.mu.Lock()
		if !attempt.abandoned {
			r.inflight[attempt.id] = attempt
		}
		r.mu.Unlock()
		return attempt, true
	case <-wait:
		return nil, false
	case <-ctx.Done():
		return nil, false
	case <-shutdown:
		return nil, false
	}
}

// complete settles the attempt a result names. A result for an attempt nobody
// is waiting on any more — the worker's budget ran out first, and it has
// already retried — is ignored.
func (r *telemetryRelay) complete(result initproto.TelemetryResult) {
	r.mu.Lock()
	attempt, ok := r.inflight[result.ID]
	delete(r.inflight, result.ID)
	r.mu.Unlock()
	if !ok {
		return
	}
	if result.Error != "" {
		attempt.done <- fmt.Errorf("the sandbox could not reach the destination: %s", result.Error)
		return
	}
	attempt.done <- nil
}

// abandon forgets an attempt whose worker has given up waiting. The batch may
// still be delivered — the init has it — and its result is simply ignored.
func (r *telemetryRelay) abandon(attempt *telemetryAttempt) {
	r.mu.Lock()
	attempt.abandoned = true
	delete(r.inflight, attempt.id)
	r.mu.Unlock()
}

// telemetryRelayFor returns the relay of the environment at containerIP, or nil
// when it has none.
//
// Nil is the answer for a Runtime API server with no per-environment listener —
// which in production is no environment at all, and in the unit tests is every
// one of them, because they drive the subscription surface directly against a
// destination on this host. That is the whole capability check: an environment
// with a listener is a container running under our init, and its telemetry goes
// through the init. See RuntimeAPIServer.postExtensionLog.
func (s *RuntimeAPIServer) telemetryRelayFor(containerIP string) *telemetryRelay {
	if containerIP == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.telemetryRelays[containerIP]
}

// handleTelemetryRelay serves POST /overcast/v1/telemetry: the init's long poll
// for the next batch to carry into the sandbox.
//
// One exchange does both halves of the channel. The request body reports what
// became of the batch the previous poll handed out, and the response is the
// next one — so an acknowledgement never needs a request of its own, and the
// init is one loop with one connection.
//
// Attribution is the same as every other call on this mux: the per-environment
// listener the request arrived on first, its source address second. The relay
// is created with that listener, before the container exists, so the init's
// first poll is answered rather than made to wait out a registration.
func (s *RuntimeAPIServer) handleTelemetryRelay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	relay := s.telemetryRelayForPort(localPortOf(r))
	if relay == nil {
		if ip, _, ok := s.lookupContainerWait(r.Context(), r); ok {
			relay = s.telemetryRelayFor(ip)
		}
	}
	if relay == nil {
		// The init retries with backoff, so a registration that has not landed
		// costs nothing but the delay — the same answer the log channel gets.
		s.logUnknownContainer(r)
		http.Error(w, "unknown container", http.StatusNotFound)
		return
	}

	var poll initproto.TelemetryPoll
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&poll); err != nil {
		http.Error(w, "invalid telemetry poll", http.StatusBadRequest)
		return
	}
	if poll.Result != nil {
		relay.complete(*poll.Result)
	}

	wait := s.clk.Timer(telemetryPollWait)
	defer wait.Stop()
	attempt, ok := relay.take(r.Context(), wait.C, s.done)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(initproto.TelemetryDelivery{
		ID:   attempt.id,
		URI:  attempt.delivery.URI,
		Body: json.RawMessage(attempt.delivery.Body),
	}); err != nil {
		// The init never received this batch, so nothing will ever report on
		// it. Failing it now returns the attempt to the worker's retry loop
		// instead of leaving it to time out.
		relay.complete(initproto.TelemetryResult{
			ID:    attempt.id,
			Error: "the delivery could not be written to the init: " + err.Error(),
		})
	}
}
