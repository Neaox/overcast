//go:build linux

package lambdainit

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// drainMax bounds the wait for the pipes to be drained before a runtime
// response is forwarded. It is a bound on a *broken* reader, not a guess about
// a slow handler: a drain completes as soon as each pipe reads EAGAIN, which is
// one poll cycle after the bytes are in the pipe. If it is ever hit, the invoke
// must still be answered.
const drainMax = 2 * time.Second

// Runtime API paths the proxy observes. Everything else — the rest of
// /2018-06-01/runtime/*, the whole of the extensions API /2020-01-01/extension/*
// and anything AWS adds next — is forwarded untouched.
const (
	runtimeInvocationPrefix = "/2018-06-01/runtime/invocation/"
	runtimeNextPath         = runtimeInvocationPrefix + "next"
)

type observation int

const (
	observeNone observation = iota
	observeNext
	observeResponse
	observeError
)

// classify identifies the three Runtime API calls that bound an invocation.
func classify(method, path string) (observation, string) {
	rest, ok := strings.CutPrefix(path, runtimeInvocationPrefix)
	if !ok {
		return observeNone, ""
	}
	if method == http.MethodGet && rest == "next" {
		return observeNext, ""
	}
	if method != http.MethodPost {
		return observeNone, ""
	}
	id, suffix, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return observeNone, ""
	}
	switch suffix {
	case "response":
		return observeResponse, id
	case "error":
		return observeError, id
	default:
		return observeNone, ""
	}
}

// requestTracker holds the invocation currently in flight. Every line the init
// reads is attributed to whatever this says at the moment of the read.
type requestTracker struct {
	// now is the clock, replaceable by a test; nil means time.Now.
	now func() time.Time

	mu      sync.RWMutex
	id      string
	lines   uint64
	beganAt time.Time
}

func (t *requestTracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *requestTracker) begin(id string) {
	t.mu.Lock()
	t.id = id
	t.lines = 0
	t.beganAt = t.clock()
	t.mu.Unlock()
}

// end clears the current request and reports how many lines were attributed to
// it and when it began. Called once the pipes have been drained, so the count
// is final; the caller measured the answer's arrival itself, before the drain,
// so the drain's cost is not billed to the runtime.
func (t *requestTracker) end() (uint64, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	began := t.beganAt
	t.id = ""
	t.lines = 0
	t.beganAt = time.Time{}
	return lines, began
}

func (t *requestTracker) current() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.id
}

// attribute returns the request a line just read belongs to, counting it.
func (t *requestTracker) attribute() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.id != "" {
		t.lines++
	}
	return t.id
}

// proxy is the Runtime API server the runtime and the extensions talk to. It is
// a transparent reverse proxy to the host's per-environment endpoint —
// method, path, query, headers and bodies pass through unchanged and
// unbuffered — that observes just enough to know where an invocation begins and
// ends.
type proxy struct {
	tracker *requestTracker
	drain   func(ctx context.Context) uint64
	diag    *diagLog
	rp      *httputil.ReverseProxy
	srv     *http.Server
	ln      net.Listener

	// initDone, when set, closes the INIT phase the first time the runtime
	// polls for work, and reports the seq of the last record it published. It
	// runs after the drain and before the request is stamped, so the closing
	// records land at or below the number the host waits for before it writes
	// the first START — see ServeHTTP. Nil in the proxy's own tests, which are
	// about forwarding rather than telemetry.
	initDone func() (uint64, bool)

	// invokeDone, when set, publishes the RecInvokeDone measurement for an
	// answered invocation and reports its seq. Published before the answer is
	// stamped and forwarded, so the host has ingested it by the time it
	// writes END. Nil in the proxy's own tests.
	invokeDone func(req string, durationMs float64, producedBytes *int64, spans []initproto.RecSpan) uint64
}

func newProxy(hostAddr string, tracker *requestTracker, drain func(ctx context.Context) uint64, diag *diagLog) *proxy {
	p := &proxy{tracker: tracker, drain: drain, diag: diag}
	target := &url.URL{Scheme: "http", Host: hostAddr}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = target.Scheme
			r.Out.URL.Host = target.Host
			r.Out.Host = target.Host
			// No X-Forwarded-For: the host identifies an execution
			// environment by the listener the request arrived on and the
			// container's address, and a forwarded-for header from inside the
			// container is not evidence of anything.
		},
		// Flush every write through: /invocation/next is a long poll and a
		// response payload can be 6 MB. Nothing on this path may be buffered.
		FlushInterval:  -1,
		ModifyResponse: p.observeResponse,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.diag.printf("runtime API proxy error: %s %s: %v", r.Method, r.URL.Path, err)
			w.WriteHeader(http.StatusBadGateway)
		},
		Transport: &http.Transport{
			// One transport, kept warm: a per-request dial on the invoke path
			// would be a new connection per /next.
			DialContext:         (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:        8,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     0, // one host for the life of the container
			DisableCompression:  true,
			ForceAttemptHTTP2:   false,
		},
	}
	// No ReadHeaderTimeout, deliberately: the only clients are the runtime and
	// the extensions, on loopback inside this container's network namespace,
	// and the protocol they speak is a long poll. There is no untrusted traffic
	// here for a header timeout to protect against.
	p.srv = &http.Server{Handler: p}
	return p
}

// listen binds the proxy's address and returns the address it actually got, so
// a caller can hand it to the child as AWS_LAMBDA_RUNTIME_API. Binding before
// the runtime starts is deliberate: a runtime interface client polls
// immediately, and a connection refused at that moment is a cold-start failure.
func (p *proxy) listen(addr string) (string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	p.ln = ln
	return ln.Addr().String(), nil
}

func (p *proxy) serve() error { return p.srv.Serve(p.ln) }

func (p *proxy) shutdown(ctx context.Context) { _ = p.srv.Shutdown(ctx) }

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch kind, id := classify(r.Method, r.URL.Path); kind {
	case observeResponse, observeError:
		// The runtime finished the moment this POST arrived; measured here,
		// before the drain, so the drain's cost is not billed to it.
		answeredAt := p.tracker.clock()

		// Drain, then forward. By the time this request arrived, everything
		// the runtime wrote for this invocation was already in the pipes —
		// same process, program order — so waiting for each reader to reach
		// EAGAIN is enough to know the init has published it all.
		ctx, cancel := context.WithTimeout(r.Context(), drainMax)
		seq := p.drain(ctx)
		timedOut := ctx.Err() != nil
		cancel()

		lines, began := p.tracker.end()
		// The measurement rides the stream below the stamped seq, so the
		// host's existing wait for the answer's frames covers it too. The
		// payload size is the Content-Length the runtime declared; a chunked
		// answer declares none, and nil lets the host fall back to the size
		// it measures itself rather than shipping an invented zero.
		if p.invokeDone != nil && !began.IsZero() {
			var produced *int64
			if r.ContentLength >= 0 {
				n := r.ContentLength
				produced = &n
			}
			held := float64(answeredAt.Sub(began).Microseconds()) / 1000.0
			// responseLatency is the one span this vantage point can measure
			// whole before the answer is forwarded: the invocation being
			// handed out to the runtime starting to send its answer — which
			// is this POST arriving. responseDuration ends only when the
			// body has finished streaming through, and runtimeOverhead only
			// at the runtime's next poll; both are after the record must be
			// on the stream, so neither is invented here.
			spans := []initproto.RecSpan{{
				Name:       "responseLatency",
				StartMs:    began.UnixMilli(),
				DurationMs: held,
			}}
			if recSeq := p.invokeDone(id, held, produced, spans); recSeq > seq {
				seq = recSeq
			}
		}

		r.Header.Set(initproto.HeaderLogSeq, strconv.FormatUint(seq, 10))
		if timedOut {
			p.diag.printf("request %s end: drain timed out after %s at seq %d (%d lines)", id, drainMax, seq, lines)
		} else {
			p.diag.printf("request %s end (drained %d lines, through seq %d)", id, lines, seq)
		}
	case observeNext:
		// Drain, then forward — the same guarantee as a response, at the other
		// end of the invocation. The runtime is asking for work, so it has
		// finished writing whatever it was going to write before this point:
		// the INIT phase on the first /next, and anything printed after the
		// previous invocation answered on later ones. Stamping that seq lets
		// the host hold the next START until those lines have landed, which is
		// what keeps INIT output in front of the first START in CloudWatch.
		//
		// The drain costs one poll cycle on a pipe that is almost always
		// already empty here; the request that follows it is a long poll.
		ctx, cancel := context.WithTimeout(r.Context(), drainMax)
		seq := p.drain(ctx)
		timedOut := ctx.Err() != nil
		cancel()

		// The first /next is also the end of the INIT phase. Its records are
		// published here — after the drain, so they follow everything the
		// phase printed, and before the stamp, so the host has waited for them
		// by the time it writes the first START.
		if p.initDone != nil {
			if recSeq, closed := p.initDone(); closed && recSeq > seq {
				seq = recSeq
			}
		}

		r.Header.Set(initproto.HeaderLogSeq, strconv.FormatUint(seq, 10))
		if timedOut {
			p.diag.printf("idle drain timed out after %s at seq %d", drainMax, seq)
		}
	case observeNone:
		// Nothing to observe; forward it untouched.
	}
	p.rp.ServeHTTP(w, r)
}

// observeResponse notes the request ID the host handed out. The header is
// available before the body is streamed, which is before the runtime can print
// anything for that invocation.
func (p *proxy) observeResponse(resp *http.Response) error {
	if resp.Request == nil || resp.Request.URL.Path != runtimeNextPath {
		return nil
	}
	id := resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	if id == "" {
		return nil
	}
	p.tracker.begin(id)
	p.diag.printf("request %s begin", id)
	return nil
}
