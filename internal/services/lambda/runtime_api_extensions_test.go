package lambda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"go.uber.org/zap"
)

func newRuntimeAPITestServer(t *testing.T) (*RuntimeAPIServer, string) {
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
	return srv, addr
}

func TestRuntimeAPIExtensionRegister_success(t *testing.T) {
	// Given: a Runtime API server with a registered Lambda container.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})

	// When: an external extension registers for invoke and shutdown events.
	body := bytes.NewBufferString(`{"events":["INVOKE","SHUTDOWN"]}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/register", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "parameters-secrets-extension")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the response matches the AWS Extensions API shape.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Lambda-Extension-Identifier"); got == "" {
		t.Fatal("missing Lambda-Extension-Identifier header")
	}
	var out struct {
		FunctionName    string `json:"functionName"`
		FunctionVersion string `json:"functionVersion"`
		Handler         string `json:"handler"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.FunctionName != "demo" || out.FunctionVersion != "$LATEST" || out.Handler != "index.handler" {
		t.Fatalf("register response = %+v", out)
	}
}

func TestRuntimeAPIExtensionNext_receivesInvoke(t *testing.T) {
	// Given: an extension registered for INVOKE events.
	srv, addr := newRuntimeAPITestServer(t)
	functionARN := "arn:aws:lambda:us-east-1:000000000000:function:demo"
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  functionARN,
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	body := bytes.NewBufferString(`{"events":["INVOKE"]}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/register", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "parameters-secrets-extension")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	extID := resp.Header.Get("Lambda-Extension-Identifier")
	resp.Body.Close()
	if extID == "" {
		t.Fatal("missing extension identifier")
	}

	// When: an invocation is submitted, the runtime accepts it, and the extension polls /event/next.
	srv.SubmitInvocation(functionARN, []byte(`{"ok":true}`), time.Now().Add(30*time.Second))
	runtimeResp := runtimeNext(t, http.DefaultClient, addr)
	runtimeResp.Body.Close()
	if runtimeResp.StatusCode != http.StatusOK {
		t.Fatalf("runtime next status = %d, want 200", runtimeResp.StatusCode)
	}
	nextReq, err := http.NewRequest(http.MethodGet, "http://"+addr+"/2020-01-01/extension/event/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	nextReq.Header.Set("Lambda-Extension-Identifier", extID)
	nextResp, err := http.DefaultClient.Do(nextReq)
	if err != nil {
		t.Fatal(err)
	}
	defer nextResp.Body.Close()

	// Then: the extension receives an AWS-shaped INVOKE lifecycle event.
	if nextResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", nextResp.StatusCode)
	}
	if got := nextResp.Header.Get("Lambda-Extension-Event-Identifier"); got == "" {
		t.Fatal("missing Lambda-Extension-Event-Identifier header")
	}
	var event struct {
		EventType          string `json:"eventType"`
		DeadlineMs         int64  `json:"deadlineMs"`
		RequestID          string `json:"requestId"`
		InvokedFunctionARN string `json:"invokedFunctionArn"`
	}
	if err := json.NewDecoder(nextResp.Body).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.EventType != "INVOKE" || event.RequestID == "" || event.InvokedFunctionARN != functionARN || event.DeadlineMs == 0 {
		t.Fatalf("extension event = %+v", event)
	}
}

func TestRuntimeAPIReadyChan_waitsForExpectedExtension(t *testing.T) {
	// Given: a container that expects an external extension to register.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:        "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName:       "demo",
		Handler:            "index.handler",
		ExpectedExtensions: []string{"parameters-secrets-extension"},
	})
	ready := srv.ReadyChan("127.0.0.1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/2018-06-01/runtime/invocation/next", nil)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	waitUntil(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return srv.seenNext["127.0.0.1"]
	})

	// Then: first runtime /next alone does not mark the container ready.
	select {
	case <-ready:
		t.Fatal("container became ready before expected extension registered")
	case <-time.After(25 * time.Millisecond):
	}

	// When: the expected extension registers.
	body := bytes.NewBufferString(`{"events":["INVOKE"]}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/register", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", "parameters-secrets-extension")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want 200", resp.StatusCode)
	}

	// Then: readiness is released.
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("container did not become ready after extension registered")
	}
	cancel()
	<-done
}

func TestRuntimeAPIExtensionNext_onlyReceivesEventsForItsContainer(t *testing.T) {
	// Given: two warm containers for the same function, each with an extension.
	srv, addr := newRuntimeAPITestServer(t)
	functionARN := "arn:aws:lambda:us-east-1:000000000000:function:demo"
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{FunctionARN: functionARN, FunctionName: "demo", Handler: "index.handler"})
	srv.RegisterContainerConfig("127.0.0.2", runtimeContainerConfig{FunctionARN: functionARN, FunctionName: "demo", Handler: "index.handler"})
	client1 := http.DefaultClient
	client2 := clientFromLocalIP(t, "127.0.0.2")
	extID1 := registerExtension(t, client1, addr, "extension-one")
	extID2 := registerExtension(t, client2, addr, "extension-two")

	// When: container 1 accepts an invocation.
	srv.SubmitInvocation(functionARN, []byte(`{"ok":true}`), time.Now().Add(30*time.Second))
	runtimeResp := runtimeNext(t, client1, addr)
	runtimeResp.Body.Close()
	if runtimeResp.StatusCode != http.StatusOK {
		t.Fatalf("runtime next status = %d, want 200", runtimeResp.StatusCode)
	}

	// Then: only extension 1 receives the INVOKE event.
	nextResp := extensionNext(t, client1, addr, extID1)
	nextResp.Body.Close()
	if nextResp.StatusCode != http.StatusOK {
		t.Fatalf("extension 1 next status = %d, want 200", nextResp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/2020-01-01/extension/event/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID2)
	resp, err := client2.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("extension 2 unexpectedly received status %d", resp.StatusCode)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("extension 2 poll error = %v, want deadline exceeded", err)
	}
}

func TestRuntimeAPIExtensionNext_receivesShutdown(t *testing.T) {
	// Given: an extension registered for SHUTDOWN events.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	extID := registerExtensionEvents(t, http.DefaultClient, addr, "parameters-secrets-extension", []string{"SHUTDOWN"})

	// When: the execution environment is shutting down and the extension polls /event/next.
	srv.EnqueueExtensionShutdown("127.0.0.1", "SPINDOWN", time.Now().Add(2*time.Second))
	nextResp := extensionNext(t, http.DefaultClient, addr, extID)
	defer nextResp.Body.Close()

	// Then: the extension receives an AWS-shaped SHUTDOWN lifecycle event.
	if nextResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", nextResp.StatusCode)
	}
	var event struct {
		EventType      string `json:"eventType"`
		ShutdownReason string `json:"shutdownReason"`
		DeadlineMs     int64  `json:"deadlineMs"`
	}
	if err := json.NewDecoder(nextResp.Body).Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event.EventType != "SHUTDOWN" || event.ShutdownReason != "SPINDOWN" || event.DeadlineMs == 0 {
		t.Fatalf("shutdown event = %+v", event)
	}
}

func TestRuntimeAPIExtensionInitError_acceptsReportedError(t *testing.T) {
	// Given: a registered extension.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	extID := registerExtension(t, http.DefaultClient, addr, "parameters-secrets-extension")

	// When: the extension reports an init error.
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/init/error", bytes.NewBufferString(`{"errorMessage":"init failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	req.Header.Set("Lambda-Extension-Function-Error-Type", "Extension.InitError")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the Runtime API accepts the report using the Lambda Extensions API status.
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if reason, ok := srv.ContainerError("127.0.0.1"); !ok || reason != "Extension.InitError" {
		t.Fatalf("container error = %q, %v", reason, ok)
	}
}

func TestRuntimeAPIExtensionExitError_acceptsReportedError(t *testing.T) {
	// Given: a registered extension.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	extID := registerExtension(t, http.DefaultClient, addr, "parameters-secrets-extension")

	// When: the extension reports an exit error.
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/exit/error", bytes.NewBufferString(`{"errorMessage":"exit failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	req.Header.Set("Lambda-Extension-Function-Error-Type", "Extension.ExitError")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: the Runtime API accepts the report using the Lambda Extensions API status.
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
}

func TestRuntimeAPILogsAPI_deliversFunctionLogs(t *testing.T) {
	// Given: an extension registered in a Lambda container and an HTTP log destination.
	srv, addr := newRuntimeAPITestServer(t)
	srv.RegisterContainerConfig("127.0.0.1", runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:demo",
		FunctionName: "demo",
		Handler:      "index.handler",
	})
	extID := registerExtension(t, http.DefaultClient, addr, "logs-extension")
	received := make(chan []map[string]any, 1)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var events []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&events); err != nil {
			t.Errorf("decode log events: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- events
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()

	// When: the extension subscribes to function logs and a function log is published.
	subscribeBody, err := json.Marshal(map[string]any{
		"types": []string{"function"},
		"buffering": map[string]any{
			"timeoutMs": 1000,
			"maxBytes":  262144,
			"maxItems":  1000,
		},
		"destination": map[string]string{
			"protocol": "HTTP",
			"URI":      dest.URL,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPut, "http://"+addr+"/2020-08-15/logs", bytes.NewReader(subscribeBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("subscribe status = %d, want 200", resp.StatusCode)
	}
	srv.PublishExtensionLog("127.0.0.1", "function", "hello from function")

	// Then: the destination receives a Logs API batch with the function record.
	select {
	case events := <-received:
		if len(events) != 1 {
			t.Fatalf("events = %#v", events)
		}
		if events[0]["type"] != "function" || events[0]["record"] != "hello from function" || events[0]["time"] == "" {
			t.Fatalf("event = %#v", events[0])
		}
	case <-time.After(time.Second):
		t.Fatal("log destination did not receive event")
	}
}

func TestNormalizeExtensionLogURI_rewritesLoopbackToContainerIP(t *testing.T) {
	// Given: an extension subscribes using a loopback destination from inside the container.
	rawURI := "http://127.0.0.1:4243/logs"

	// When: the Runtime API prepares the URI for host-side delivery.
	got := normalizeExtensionLogURI(rawURI, "172.18.0.9")

	// Then: delivery targets the subscribing container IP and original port/path.
	want := "http://172.18.0.9:4243/logs"
	if got != want {
		t.Fatalf("uri = %q, want %q", got, want)
	}
}

func TestRuntimeAPIExtensionQueue_isBounded(t *testing.T) {
	// Given: an extension that has stopped polling for lifecycle events.
	srv, _ := newRuntimeAPITestServer(t)
	ext := &extensionState{Events: map[string]bool{"SHUTDOWN": true}}

	// When: more lifecycle events are queued than the safety cap allows.
	srv.mu.Lock()
	for i := 0; i < maxExtensionEventQueue+10; i++ {
		srv.enqueueExtensionEventLocked(ext, extensionEvent{ID: "event", Body: []byte(`{}`)})
	}
	got := len(ext.Queue)
	srv.mu.Unlock()

	// Then: the in-memory queue is capped to avoid unbounded growth.
	if got != maxExtensionEventQueue {
		t.Fatalf("queue length = %d, want %d", got, maxExtensionEventQueue)
	}
}

func registerExtension(t *testing.T, client *http.Client, addr, name string) string {
	t.Helper()
	return registerExtensionEvents(t, client, addr, name, []string{"INVOKE"})
}

func registerExtensionEvents(t *testing.T, client *http.Client, addr, name string, events []string) string {
	t.Helper()
	payload, err := json.Marshal(map[string][]string{"events": events})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBuffer(payload)
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/2020-01-01/extension/register", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Name", name)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want 200", resp.StatusCode)
	}
	extID := resp.Header.Get("Lambda-Extension-Identifier")
	if extID == "" {
		t.Fatal("missing extension identifier")
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return extID
}

func runtimeNext(t *testing.T, client *http.Client, addr string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/2018-06-01/runtime/invocation/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func extensionNext(t *testing.T, client *http.Client, addr, extID string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/2020-01-01/extension/event/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Lambda-Extension-Identifier", extID)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func clientFromLocalIP(t *testing.T, ip string) *http.Client {
	t.Helper()
	return &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{
		LocalAddr: &net.TCPAddr{IP: net.ParseIP(ip)},
	}).DialContext}}
}

func waitUntil(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met")
}
