package lambda

// runtime_api.go — Lambda Runtime API server.
//
// AWS Lambda containers communicate with the execution environment via the
// Lambda Runtime Interface (RIC). The RIC inside the container:
//
//  1. GET  /2018-06-01/runtime/invocation/next     — long-poll for work
//  2. POST /2018-06-01/runtime/invocation/{id}/response — deliver success result
//  3. POST /2018-06-01/runtime/invocation/{id}/error    — deliver function error
//  4. POST /2018-06-01/runtime/init/error               — cold-start failure
//
// This server listens on a port reachable from Lambda containers (via the
// Docker network). The AWS_LAMBDA_RUNTIME_API env var in each container
// points here. Multiple containers share the same server; each pending
// invocation is keyed by its request ID.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// pendingInvocation represents a single in-flight Lambda invocation waiting
// for a container to pick it up and return a result.
type pendingInvocation struct {
	RequestID   string
	FunctionARN string
	Deadline    time.Time
	TraceID     string // X-Ray trace header (Root=1-...;Parent=...;Sampled=0)
	Event       []byte
	ResultCh    chan invokeResponse
}

// invokeResponse is sent back from the container via the Runtime API.
type invokeResponse struct {
	Payload       []byte
	FunctionError string // "" for success, "Handled" or "Unhandled"
	IsInitError   bool   // true if POST /runtime/init/error was called
	ErrorPayload  []byte // error details JSON when FunctionError != ""
}

// RuntimeAPIServer serves the Lambda Runtime API to containers.
type RuntimeAPIServer struct {
	mu         sync.Mutex
	pending    map[string]*pendingInvocation      // keyed by request ID
	funcQueues map[string][]*pendingInvocation    // keyed by function ARN — FIFO
	waiting    map[string]chan *pendingInvocation // keyed by function ARN — one waiter per container
	containers map[string]string                  // container ID → function ARN (registered on Acquire)
	seenNext   map[string]bool                    // container IP → true after first GET /next
	server     *http.Server
	listener   net.Listener
	logger     *zap.Logger
	addr       string        // host:port as seen by containers
	done       chan struct{} // closed on Stop to unblock long-polling handlers
	clk        clock.Clock

	// OnFirstNext is called (in a goroutine) the first time a container's RIC
	// issues GET /next.  The argument is the function ARN.  Setting this lets
	// the instance tracker transition the instance from "initializing" to
	// "running".
	OnFirstNext func(functionARN string)
}

// NewRuntimeAPIServer creates and starts the Runtime API server.
// listenAddr is the address to bind to (e.g. "0.0.0.0:9001").
// containerAddr is the host:port that containers use to reach this server
// (may differ from listenAddr when Overcast runs inside Docker).
func NewRuntimeAPIServer(listenAddr string, containerAddr string, logger *zap.Logger, clk clock.Clock) (*RuntimeAPIServer, error) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("runtime api: listen %s: %w", listenAddr, err)
	}
	return NewRuntimeAPIServerFromListener(ln, containerAddr, logger, clk)
}

// NewRuntimeAPIServerFromListener is like NewRuntimeAPIServer but accepts a
// pre-created listener. This allows the caller to bind first (e.g. to resolve
// port 0) and then derive containerAddr from the actual port.
func NewRuntimeAPIServerFromListener(ln net.Listener, containerAddr string, logger *zap.Logger, clk clock.Clock) (*RuntimeAPIServer, error) {
	s := &RuntimeAPIServer{
		pending:    make(map[string]*pendingInvocation),
		funcQueues: make(map[string][]*pendingInvocation),
		waiting:    make(map[string]chan *pendingInvocation),
		containers: make(map[string]string),
		seenNext:   make(map[string]bool),
		logger:     logger,
		addr:       containerAddr,
		done:       make(chan struct{}),
		clk:        clk,
	}

	mux := http.NewServeMux()
	// Lambda Runtime API routes (2018-06-01 version).
	mux.HandleFunc("/2018-06-01/runtime/invocation/next", s.handleNext)
	mux.HandleFunc("/2018-06-01/runtime/invocation/", s.handleInvocationAction)
	mux.HandleFunc("/2018-06-01/runtime/init/error", s.handleInitError)

	s.server = &http.Server{Handler: mux}
	s.listener = ln

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("runtime api: serve error", zap.Error(err))
		}
	}()

	logger.Info("lambda runtime API started",
		zap.String("listen", ln.Addr().String()),
		zap.String("container_addr", containerAddr))

	return s, nil
}

// Addr returns the host:port that containers should use to reach this server.
func (s *RuntimeAPIServer) Addr() string { return s.addr }

// RegisterContainer maps the container's IP address to a function ARN so that
// incoming GET /next requests from that container can be routed to the correct
// invocation queue. Call this after the container has started and its IP is known.
func (s *RuntimeAPIServer) RegisterContainer(containerIP, functionARN string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[containerIP] = functionARN
}

// UnregisterContainer removes the container IP from the registry.
func (s *RuntimeAPIServer) UnregisterContainer(containerIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.containers, containerIP)
	delete(s.seenNext, containerIP)
}

// lookupContainerWait resolves a container IP to its function ARN, blocking
// up to maxWait for the registration if it hasn't landed yet. Returns ok=false
// if the wait expires or the request is cancelled first.
func (s *RuntimeAPIServer) lookupContainerWait(ctx context.Context, ip string, maxWait time.Duration) (string, bool) {
	s.mu.Lock()
	arn, ok := s.containers[ip]
	s.mu.Unlock()
	if ok {
		return arn, true
	}
	deadline := s.clk.Now().Add(maxWait)
	ticker := s.clk.Ticker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", false
		case <-s.done:
			return "", false
		case <-ticker.C:
			s.mu.Lock()
			arn, ok = s.containers[ip]
			s.mu.Unlock()
			if ok {
				return arn, true
			}
			if s.clk.Now().After(deadline) {
				return "", false
			}
		}
	}
}

// CancelInvocation removes a pending invocation from the map and closes its
// ResultCh so that any goroutine blocked on <-resultCh is unblocked. This
// must be called when the container crashes or the invoke times out to prevent
// goroutine leaks from drain goroutines that would otherwise block forever.
func (s *RuntimeAPIServer) CancelInvocation(reqID string) {
	s.mu.Lock()
	inv, ok := s.pending[reqID]
	if ok {
		delete(s.pending, reqID)
	}
	s.mu.Unlock()
	if ok {
		close(inv.ResultCh)
	}
}

// SubmitInvocation enqueues an invocation for a container to pick up.
// It returns the request ID and a channel that will receive the result.
func (s *RuntimeAPIServer) SubmitInvocation(functionARN string, event []byte, deadline time.Time) (string, <-chan invokeResponse) {
	reqID := uuid.New().String()
	ch := make(chan invokeResponse, 1)

	inv := &pendingInvocation{
		RequestID:   reqID,
		FunctionARN: functionARN,
		Deadline:    deadline,
		TraceID:     newXRayTraceID(s.clk.Now()),
		Event:       event,
		ResultCh:    ch,
	}

	s.mu.Lock()
	s.pending[reqID] = inv

	// If a container for this function is already waiting (long-polling /next),
	// deliver immediately.
	if waiter, ok := s.waiting[functionARN]; ok {
		select {
		case waiter <- inv:
			delete(s.waiting, functionARN)
			s.mu.Unlock()
			return reqID, ch
		default:
		}
	}

	// No waiter — enqueue for later pickup.
	s.funcQueues[functionARN] = append(s.funcQueues[functionARN], inv)
	s.mu.Unlock()

	return reqID, ch
}

// handleNext serves GET /2018-06-01/runtime/invocation/next.
// This is a long-poll: the container blocks here until an invocation arrives.
func (s *RuntimeAPIServer) handleNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Identify the calling container's function ARN by its source IP.
	// Containers register their IP → function ARN via RegisterContainer.
	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)

	// Race window: the container's runtime starts polling /next the instant
	// it boots, but overcast can't call RegisterContainer until InspectContainer
	// reports the IP (up to several seconds later on slow Docker hosts). Early
	// polls would 403 and the RIC would give up with an init error. Wait a
	// bounded window for registration to catch up — the registration is almost
	// always in flight when we land here.
	functionARN, known := s.lookupContainerWait(r.Context(), remoteIP, 15*time.Second)
	if !known {
		http.Error(w, "unknown container", http.StatusForbidden)
		return
	}

	// Detect the first GET /next from this container — signals that the RIC
	// (and the language runtime + handler code) has finished initialising.
	s.mu.Lock()
	if !s.seenNext[remoteIP] {
		s.seenNext[remoteIP] = true
		cb := s.OnFirstNext
		s.mu.Unlock()
		if cb != nil {
			go cb(functionARN)
		}
		s.mu.Lock()
	}

	// Check the function's invocation queue first.
	if queue := s.funcQueues[functionARN]; len(queue) > 0 {
		inv := queue[0]
		s.funcQueues[functionARN] = queue[1:]
		// Do NOT delete from s.pending here — handleInvocationAction needs it
		// to route the container's response POST back to the caller's ResultCh.
		s.mu.Unlock()
		s.writeNextResponse(w, inv)
		return
	}

	// No pending invocation — register a waiter channel and long-poll.
	waiterCh := make(chan *pendingInvocation, 1)
	s.waiting[functionARN] = waiterCh
	s.mu.Unlock()

	ctx := r.Context()
	select {
	case inv := <-waiterCh:
		s.writeNextResponse(w, inv)
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.waiting, functionARN)
		s.mu.Unlock()
	case <-s.done:
		s.mu.Lock()
		delete(s.waiting, functionARN)
		s.mu.Unlock()
	}
}

// writeNextResponse sends the invocation event to the container with
// the required Runtime API headers.
func (s *RuntimeAPIServer) writeNextResponse(w http.ResponseWriter, inv *pendingInvocation) {
	w.Header().Set("Lambda-Runtime-Aws-Request-Id", inv.RequestID)
	w.Header().Set("Lambda-Runtime-Deadline-Ms", fmt.Sprintf("%d", inv.Deadline.UnixMilli()))
	w.Header().Set("Lambda-Runtime-Invoked-Function-Arn", inv.FunctionARN)
	w.Header().Set("Lambda-Runtime-Trace-Id", inv.TraceID)
	// Lambda-Runtime-Client-Context and Lambda-Runtime-Cognito-Identity are
	// only set by real Lambda for mobile-SDK invocations. Omitting them when
	// absent matches AWS behaviour and keeps RICs that check for header
	// presence happy.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(inv.Event)
}

// newXRayTraceID generates a syntactically-valid X-Ray trace header with
// Sampled=0 so SDKs don't attempt to ship segments to a daemon that isn't
// running. Format: Root=1-{8-hex-epoch}-{24-hex-random};Parent={16-hex};Sampled=0
func newXRayTraceID(now time.Time) string {
	var buf [20]byte
	_, _ = rand.Read(buf[:])
	rootRand := hex.EncodeToString(buf[0:12])
	parent := hex.EncodeToString(buf[12:20])
	return fmt.Sprintf("Root=1-%08x-%s;Parent=%s;Sampled=0", uint32(now.Unix()), rootRand, parent)
}

// handleInvocationAction routes POST .../invocation/{id}/response and .../invocation/{id}/error.
func (s *RuntimeAPIServer) handleInvocationAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request ID and action from the path.
	// Path format: /2018-06-01/runtime/invocation/{id}/response
	//           or /2018-06-01/runtime/invocation/{id}/error
	path := strings.TrimPrefix(r.URL.Path, "/2018-06-01/runtime/invocation/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	reqID := parts[0]
	action := parts[1]

	body, err := io.ReadAll(io.LimitReader(r.Body, 6*1024*1024+1024)) // 6MB + buffer
	if err != nil {
		http.Error(w, "read body failed", http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	inv, ok := s.pending[reqID]
	if ok {
		delete(s.pending, reqID)
	}
	s.mu.Unlock()

	if !ok {
		// The invocation was already delivered or timed out.
		// This can happen if the caller's context was cancelled.
		s.logger.Debug("runtime api: invocation not found (already completed or timed out)",
			zap.String("request_id", reqID), zap.String("action", action))
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch action {
	case "response":
		inv.ResultCh <- invokeResponse{Payload: body}
	case "error":
		// Parse the error type from Lambda-Runtime-Function-Error-Type header.
		errorType := r.Header.Get("Lambda-Runtime-Function-Error-Type")
		funcError := "Handled"
		if errorType == "Runtime.ExitError" || errorType == "Runtime.Unknown" {
			funcError = "Unhandled"
		}
		inv.ResultCh <- invokeResponse{
			FunctionError: funcError,
			ErrorPayload:  body,
		}
	default:
		http.Error(w, "unknown action: "+action, http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// handleInitError serves POST /2018-06-01/runtime/init/error.
// Called when the container fails during cold start (e.g. import error).
func (s *RuntimeAPIServer) handleInitError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	errorType := r.Header.Get("Lambda-Runtime-Function-Error-Type")

	s.logger.Error("lambda container init error",
		zap.String("error_type", errorType),
		zap.String("body", string(body)))

	// Find the pending invocation for this container. The container sends the
	// request ID in the Lambda-Runtime-Aws-Request-Id header (set by the RIC
	// from the /next response). If not present, we try to match any pending
	// invocation — in practice there's usually exactly one.
	reqID := r.Header.Get("Lambda-Runtime-Aws-Request-Id")

	s.mu.Lock()
	var inv *pendingInvocation
	if reqID != "" {
		inv = s.pending[reqID]
		delete(s.pending, reqID)
	} else {
		// Grab any pending invocation (best effort).
		for id, p := range s.pending {
			inv = p
			delete(s.pending, id)
			break
		}
	}
	s.mu.Unlock()

	if inv != nil {
		// Build an error payload matching AWS format.
		errResp := map[string]string{
			"errorMessage": "Runtime initialization failed: " + errorType,
			"errorType":    errorType,
		}
		payload, _ := json.Marshal(errResp)
		inv.ResultCh <- invokeResponse{
			FunctionError: "Unhandled",
			IsInitError:   true,
			ErrorPayload:  payload,
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// Stop gracefully shuts down the Runtime API server.
func (s *RuntimeAPIServer) Stop(ctx context.Context) error {
	// Close the done channel first so long-polling handleNext requests
	// unblock and complete. Without this, Shutdown blocks waiting for
	// in-flight requests that will never finish on their own.
	close(s.done)
	return s.server.Shutdown(ctx)
}
