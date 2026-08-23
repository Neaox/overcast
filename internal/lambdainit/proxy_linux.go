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

	"github.com/Neaox/overcast/internal/services/lambda/initproto"
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
	mu    sync.RWMutex
	id    string
	lines uint64
}

func (t *requestTracker) begin(id string) {
	t.mu.Lock()
	t.id = id
	t.lines = 0
	t.mu.Unlock()
}

// end clears the current request and reports how many lines were attributed to
// it. Called once the pipes have been drained, so the count is final.
func (t *requestTracker) end() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := t.lines
	t.id = ""
	t.lines = 0
	return lines
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
		// Drain, then forward. By the time this request arrived, everything
		// the runtime wrote for this invocation was already in the pipes —
		// same process, program order — so waiting for each reader to reach
		// EAGAIN is enough to know the init has published it all.
		ctx, cancel := context.WithTimeout(r.Context(), drainMax)
		seq := p.drain(ctx)
		timedOut := ctx.Err() != nil
		cancel()

		r.Header.Set(initproto.HeaderLogSeq, strconv.FormatUint(seq, 10))
		lines := p.tracker.end()
		if timedOut {
			p.diag.printf("request %s end: drain timed out after %s at seq %d (%d lines)", id, drainMax, seq, lines)
		} else {
			p.diag.printf("request %s end (drained %d lines, through seq %d)", id, lines, seq)
		}
	case observeNext, observeNone:
		// Nothing to do before forwarding; /next is observed on the way back.
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
