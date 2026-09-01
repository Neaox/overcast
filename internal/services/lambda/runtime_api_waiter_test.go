package lambda

import (
	"context"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
)

// runtime_api_waiter_test.go covers invocation hand-off when more than one
// container serves the same function.
//
// s.waiting held one channel per function ARN, so the second container to
// long-poll GET /next replaced the first container's channel. The displaced
// container stayed blocked on a channel nothing would ever send to, and it
// never re-checked the queue, so work sat undelivered until the invocation's
// deadline expired. The symptom was a function that logged START, produced no
// output for its entire timeout, and then ran to completion in a *later*
// container under the already-dead request ID.

const waiterTestARN = "arn:aws:lambda:us-east-1:000000000000:function:waiters"

func newWaiterTestServer(t *testing.T) (*RuntimeAPIServer, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	srv, err := NewRuntimeAPIServerFromListener(ln, addr, zap.NewNop(), clock.New())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	srv.RegisterContainer("127.0.0.1", waiterTestARN)
	return srv, addr
}

// pollNext issues GET /next on its own connection, the way a container's RIC
// does, and reports the request ID it was handed — or "" if the poll ended
// without one. It returns once the request has been written, so the caller
// knows the server is handling it.
func pollNext(t *testing.T, ctx context.Context, addr string) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	sent := make(chan struct{})
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://"+addr+"/2018-06-01/runtime/invocation/next", nil)
		if err != nil {
			close(sent)
			got <- ""
			return
		}
		var once sync.Once
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) { once.Do(func() { close(sent) }) },
		}))
		// A dedicated transport per poll so each one holds its own connection,
		// the way separate containers do.
		client := &http.Client{Transport: &http.Transport{}}
		resp, err := client.Do(req)
		once.Do(func() { close(sent) })
		if err != nil {
			got <- ""
			return
		}
		defer resp.Body.Close()
		got <- resp.Header.Get("Lambda-Runtime-Aws-Request-Id")
	}()
	<-sent
	// The request is on the wire; give the handler a moment to park itself
	// before the caller submits work that should find it waiting.
	time.Sleep(100 * time.Millisecond)
	return got
}

// TestRuntimeAPI_secondPollerDoesNotOrphanTheFirst pins that every container
// long-polling GET /next stays eligible for work.
func TestRuntimeAPI_secondPollerDoesNotOrphanTheFirst(t *testing.T) {
	// Given: two containers for one function are both long-polling.
	srv, addr := newWaiterTestServer(t)
	ctxA, cancelA := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelA()
	ctxB, cancelB := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelB()

	first := pollNext(t, ctxA, addr)
	second := pollNext(t, ctxB, addr)

	// When: two invocations are submitted.
	idA, _ := srv.SubmitInvocation(waiterTestARN, []byte(`{"n":1}`), time.Now().Add(30*time.Second))
	idB, _ := srv.SubmitInvocation(waiterTestARN, []byte(`{"n":2}`), time.Now().Add(30*time.Second))

	// Then: both are delivered — one to each container.
	delivered := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-first:
			delivered[id] = true
		case id := <-second:
			delivered[id] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 2 invocations were delivered — a poller was orphaned", len(delivered))
		}
	}
	if !delivered[idA] || !delivered[idB] {
		t.Errorf("delivered = %v, want both %s and %s", delivered, idA, idB)
	}
}

// TestRuntimeAPI_displacedPollerStillGetsQueuedWork pins the exact production
// sequence: a container is displaced from the waiter slot, the container that
// displaced it goes away, and the next invocation must still reach the
// container that is sitting there ready for it.
func TestRuntimeAPI_displacedPollerStillGetsQueuedWork(t *testing.T) {
	// Given: container A is long-polling, then container B polls and goes away.
	srv, addr := newWaiterTestServer(t)
	ctxA, cancelA := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelA()
	first := pollNext(t, ctxA, addr)

	ctxB, cancelB := context.WithCancel(context.Background())
	displaced := pollNext(t, ctxB, addr)
	cancelB()
	<-displaced

	// When: an invocation is submitted.
	want, _ := srv.SubmitInvocation(waiterTestARN, []byte(`{}`), time.Now().Add(30*time.Second))

	// Then: container A receives it rather than waiting out the deadline.
	select {
	case got := <-first:
		if got != want {
			t.Errorf("request ID = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invocation never reached the waiting container — it would sit until its deadline")
	}
}

// TestRuntimeAPI_cancelledInvocationIsNotHandedToALaterContainer pins that a
// timed-out invocation is dropped rather than left queued.
//
// CancelInvocation removed the entry from s.pending but left it in the
// function's queue, so the next container to start picked up work whose caller
// had already given up. It ran the handler a second time — real side effects,
// under a request ID that had already been reported as timed out — and its
// response was discarded as "invocation not found".
func TestRuntimeAPI_cancelledInvocationIsNotHandedToALaterContainer(t *testing.T) {
	// Given: an invocation that was submitted and then cancelled.
	srv, addr := newWaiterTestServer(t)
	reqID, resultCh := srv.SubmitInvocation(waiterTestARN, []byte(`{}`), time.Now().Add(30*time.Second))
	srv.CancelInvocation(reqID)
	<-resultCh // closed by CancelInvocation

	// When: a container polls for work.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	got := pollNext(t, ctx, addr)

	// Then: it is not handed the cancelled invocation.
	select {
	case id := <-got:
		if id != "" {
			t.Errorf("container was handed cancelled invocation %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poll never returned")
	}
}

// TestRuntimeAPI_extensionEventSurvivesAnUnwindingPoll pins the same hazard one
// layer down: an extension event handed to a poll that is unwinding must go
// back on the queue rather than vanish into a buffered channel nobody reads.
func TestRuntimeAPI_extensionEventSurvivesAnUnwindingPoll(t *testing.T) {
	// Given: a registered extension with a parked /event/next poll.
	srv, _ := newWaiterTestServer(t)
	waiter := make(chan extensionEvent, 1)
	ext := &extensionState{
		ID: "ext-1", Name: "bootstrap", ContainerIP: "127.0.0.1",
		FunctionARN: waiterTestARN,
		Events:      map[string]bool{"INVOKE": true, "SHUTDOWN": true},
		Waiter:      waiter,
	}
	srv.mu.Lock()
	srv.extensions[ext.ID] = ext
	srv.mu.Unlock()

	// When: an event is delivered and the poll unwinds before reading it.
	srv.EnqueueExtensionShutdown("127.0.0.1", "SPINDOWN", time.Now().Add(2*time.Second))
	srv.releaseExtensionWaiter(ext, waiter)

	// Then: the event is back on the queue for the extension's next poll.
	srv.mu.Lock()
	queued := len(ext.Queue)
	srv.mu.Unlock()
	if queued != 1 {
		t.Errorf("queued events = %d, want 1 — the SHUTDOWN was lost with the poll", queued)
	}
}
