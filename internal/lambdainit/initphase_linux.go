//go:build linux

package lambdainit

import (
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// initPhase reports the execution environment's INIT phase as the three
// platform-telemetry records AWS emits for it: initStart when the environment
// begins initialising, then initRuntimeDone and initReport, in that order, when
// the runtime asks for its first invocation.
//
// It lives in the init because the init is the only thing that can see either
// boundary honestly. The start is the moment before anything is spawned — no
// output can precede it. The end is the runtime's first GET /next, observed by
// the proxy before that request is forwarded, which is what puts both closing
// records ahead of the X-Overcast-Log-Seq the host waits for before it writes
// the first START. Nothing here synchronises with the host: the records ride
// the frame stream, so their position is the sequence, and the sequence is
// already the host's ordering authority.
//
// The duration is measured here for the same reason. The host's own
// Init Duration — container start to first /next — is a different span and is
// not recorded at all for a proactively initialised environment, which still
// gets an initReport on AWS.
type initPhase struct {
	now     func() time.Time
	publish func(initproto.Record) uint64
	diag    *diagLog

	mu        sync.Mutex
	startedAt time.Time
	done      bool
}

// begin publishes initStart and starts the clock. It is called before the
// extensions and the runtime are spawned, so no frame can precede it.
func (p *initPhase) begin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.startedAt.IsZero() {
		return
	}
	p.startedAt = p.now()
	p.publish(initproto.Record{Type: initproto.RecInitStart})
}

// complete closes the phase with initRuntimeDone and initReport, and reports
// the sequence of the last record it published — the number a caller stamps on
// the request it is about to forward — and whether this call was the one that
// closed the phase. Every call after the first is a no-op: the phase happens
// once per execution environment, and the runtime polls for work many times.
func (p *initPhase) complete(status string) (uint64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done || p.startedAt.IsZero() {
		return 0, false
	}
	p.done = true

	elapsed := p.now().Sub(p.startedAt)
	if elapsed < 0 {
		// A clock that went backwards is not evidence of a negative init.
		elapsed = 0
	}
	p.publish(initproto.Record{Type: initproto.RecInitRuntimeDone, Status: status})
	seq := p.publish(initproto.Record{
		Type:       initproto.RecInitReport,
		Status:     status,
		DurationMs: float64(elapsed.Microseconds()) / 1000.0,
	})
	p.diag.printf("init phase %s after %s", status, elapsed)
	return seq, true
}
