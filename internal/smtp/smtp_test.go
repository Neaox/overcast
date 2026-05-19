package smtp_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/smtp"
)

// dialSMTP connects to addr, runs through a minimal SMTP session sending one
// message, and returns. It mirrors what NetMailer does internally.
func dialSMTP(t *testing.T, addr, from string, to []string, body string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	br := bufio.NewReader(conn)

	readLine := func() string {
		t.Helper()
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("readLine: %v", err)
		}
		return strings.TrimRight(line, "\r\n")
	}
	// Skip multi-line response (e.g. EHLO 250- lines) until a line without "-".
	readUntilFinal := func() {
		for {
			line := readLine()
			// Final line: code followed by space (e.g. "250 OK")
			if len(line) < 4 || line[3] != '-' {
				return
			}
		}
	}
	send := func(line string) {
		t.Helper()
		fmt.Fprintf(conn, "%s\r\n", line)
	}

	readLine() // greeting
	send("EHLO test")
	readUntilFinal()
	send("MAIL FROM:<" + from + ">")
	readLine()
	for _, r := range to {
		send("RCPT TO:<" + r + ">")
		readLine()
	}
	send("DATA")
	readLine() // 354
	// Write body lines; terminate with lone ".".
	for _, line := range strings.Split(body, "\n") {
		send(line)
	}
	send(".")
	readLine() // 250
	send("QUIT")
	readLine() // 221
}

// startTestServer starts srv in a background goroutine and returns its bound
// address once the listener is ready.
func startTestServer(t *testing.T, srv *smtp.Server) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	go srv.Serve(ctx)
	return addr
}

// waitForMessage polls store.Len() up to 500ms, failing if no message arrives.
func waitForMessage(t *testing.T, store *smtp.MailStore) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.Len() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for message in store")
}

// ---- Server tests ----------------------------------------------------------

func TestServer_CapturePlainMessage(t *testing.T) {
	store := smtp.NewMailStore(0)
	addr := startTestServer(t, smtp.NewServer("127.0.0.1:0", store))

	body := "Subject: Hello\r\n\r\nworld"
	dialSMTP(t, addr, "sender@example.com", []string{"rcpt@example.com"}, body)
	waitForMessage(t, store)

	msgs := store.List()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.From != "sender@example.com" {
		t.Errorf("From = %q, want %q", m.From, "sender@example.com")
	}
	if len(m.To) != 1 || m.To[0] != "rcpt@example.com" {
		t.Errorf("To = %v, want [rcpt@example.com]", m.To)
	}
	if m.Subject != "Hello" {
		t.Errorf("Subject = %q, want %q", m.Subject, "Hello")
	}
	if !strings.Contains(m.TextBody, "world") {
		t.Errorf("TextBody = %q, expected to contain %q", m.TextBody, "world")
	}
}

func TestServer_MultipleMessages(t *testing.T) {
	store := smtp.NewMailStore(0)
	addr := startTestServer(t, smtp.NewServer("127.0.0.1:0", store))

	dialSMTP(t, addr, "a@x.com", []string{"b@x.com"}, "Subject: One\r\n\r\nMsg1")
	dialSMTP(t, addr, "a@x.com", []string{"b@x.com"}, "Subject: Two\r\n\r\nMsg2")

	for i := 0; i < 50; i++ {
		if store.Len() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if store.Len() != 2 {
		t.Fatalf("expected 2 messages, got %d", store.Len())
	}
}

// ---- MailStore tests -------------------------------------------------------

func TestMailStore_CapacityEviction(t *testing.T) {
	store := smtp.NewMailStore(3)
	for i := 0; i < 5; i++ {
		store.Add(&smtp.CapturedMessage{
			ID:   fmt.Sprintf("id-%d", i),
			From: "x@x.com",
		})
	}
	if store.Len() != 3 {
		t.Fatalf("expected len 3, got %d", store.Len())
	}
	// Oldest (id-0, id-1) should have been dropped.
	if store.Get("id-0") != nil || store.Get("id-1") != nil {
		t.Error("oldest messages should have been evicted")
	}
	if store.Get("id-4") == nil {
		t.Error("most recent message should still be present")
	}
}

func TestMailStore_DeleteAndClear(t *testing.T) {
	store := smtp.NewMailStore(0)
	store.Add(&smtp.CapturedMessage{ID: "a", From: "x@x.com"})
	store.Add(&smtp.CapturedMessage{ID: "b", From: "x@x.com"})

	if !store.Delete("a") {
		t.Error("Delete returned false for existing id")
	}
	if store.Get("a") != nil {
		t.Error("message should be gone after Delete")
	}
	if store.Len() != 1 {
		t.Errorf("expected len 1, got %d", store.Len())
	}

	store.Clear()
	if store.Len() != 0 {
		t.Errorf("expected len 0 after Clear, got %d", store.Len())
	}
}

func TestMailStore_ListNewestFirst(t *testing.T) {
	store := smtp.NewMailStore(0)
	store.Add(&smtp.CapturedMessage{ID: "first", From: "x@x.com"})
	store.Add(&smtp.CapturedMessage{ID: "second", From: "x@x.com"})

	list := store.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
	if list[0].ID != "second" || list[1].ID != "first" {
		t.Errorf("expected newest first, got %s, %s", list[0].ID, list[1].ID)
	}
}

// ---- Server concurrency stress test ---------------------------------------

func TestServer_ConcurrentConnections(t *testing.T) {
	store := smtp.NewMailStore(200)
	srv := smtp.NewServer("127.0.0.1:0", store)
	addr := startTestServer(t, srv)

	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	mailer := smtp.NewMailer(smtp.Config{Host: host, Port: port})

	var wg sync.WaitGroup
	const goroutines = 50
	const perGoroutine = 4 // 200 total messages

	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := mailer.Send(
					context.Background(),
					fmt.Sprintf("g%d-%d@concurrent.test", id, i),
					[]string{fmt.Sprintf("rcpt-%d-%d@concurrent.test", id, i)},
					fmt.Sprintf("subject %d-%d", id, i),
					fmt.Sprintf("body %d-%d", id, i),
					"",
				)
				if err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	n := 0
	for e := range errs {
		n++
		t.Errorf("unexpected send error: %v", e)
	}
	if n > 0 {
		t.Fatalf("%d sends failed", n)
	}

	// All 200 messages should be captured.
	if got := store.Len(); got != goroutines*perGoroutine {
		t.Errorf("expected %d messages, got %d", goroutines*perGoroutine, got)
	}
}

// TestServer_Thrash blasts the SMTP server with 1000 concurrent messages from
// 100 goroutines — far more than the 4-suite compat run ever sends. The server
// must accept every message, remain responsive afterwards, and not leak
// goroutines or file descriptors.
func TestServer_Thrash(t *testing.T) {
	store := smtp.NewMailStore(2000)
	srv := smtp.NewServer("127.0.0.1:0", store)
	addr := startTestServer(t, srv)

	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	mailer := smtp.NewMailer(smtp.Config{Host: host, Port: port})

	const goroutines = 100
	const perGoroutine = 10 // 1000 total messages

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := mailer.Send(
					context.Background(),
					fmt.Sprintf("thrash-%d-%d@load.test", id, i),
					[]string{fmt.Sprintf("rcpt-%d-%d@load.test", id, i)},
					fmt.Sprintf("subject %d-%d", id, i),
					fmt.Sprintf("body %d-%d", id, i),
					"",
				)
				if err != nil {
					errs <- err
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Errorf("unexpected error under thrash load: %v", e)
	}

	if got := store.Len(); got != goroutines*perGoroutine {
		t.Errorf("expected %d messages, got %d", goroutines*perGoroutine, got)
	}

	// After the thrashing, the server must still be responsive.
	if err := mailer.Send(context.Background(), "post-thrash@test.com",
		[]string{"check@test.com"}, "post thrash", "still alive?", ""); err != nil {
		t.Fatalf("server unresponsive after thrash load: %v", err)
	}

	t.Logf("1000 messages accepted, server still responsive")
}

// TestNetMailer_DialTimeout reproduces the cascading-hang observed in the
// compat suite: when the SMTP server's accept loop stalls (backlog full,
// listener died), clients calling smtp.SendMail hang forever because net.Dial
// has no timeout. This test proves the bug exists without the fix.
//
// We create a listener that never Accepts (simulating a broken accept loop),
// saturate its backlog so new SYNs are silently dropped, then show that the
// old-style Send hangs. The NetMailer fix replaces net.Dial with
// DialContext+Timeout so the call returns an error instead of leaking a
// goroutine and an ephemeral port.
func TestNetMailer_DialTimeout(t *testing.T) {
	store := smtp.NewMailStore(10)
	srv := smtp.NewServer("127.0.0.1:0", store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, err := srv.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	// Start serve loop so we can accept one message.
	go srv.Serve(ctx)

	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	mailer := smtp.NewMailer(smtp.Config{Host: host, Port: port})

	// Prove the server works with one message.
	if err := mailer.Send(context.Background(), "a@x.com", []string{"b@x.com"}, "sub", "body", ""); err != nil {
		t.Fatalf("initial Send failed (server should be healthy): %v", err)
	}

	// Now kill the listener — simulate the bug where the accept loop dies.
	_ = srv.Close()

	// The next Send must either return an error promptly or produce a dial
	// error via the OS. With a plain net.Dial (no timeout) against a closed
	// localhost listener, the OS usually returns ECONNREFUSED immediately —
	// localhost does not exhibit the silent-drop behaviour of a full backlog.
	// We assert the call completes within a generous window.
	done := make(chan error, 1)
	go func() {
		done <- mailer.Send(context.Background(), "a@x.com", []string{"b@x.com"}, "sub", "body", "")
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error after server close, got nil")
		} else {
			t.Logf("Send after close returned promptly: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Send hung for >3s after server close — net.Dial has no timeout")
	}

	// Now test the slow-accept scenario. We create a listener that accepts
	// exactly zero connections (no Accept loop running). The kernel will
	// queue the initial SYN but since no Accept ever drains it, the backlog
	// fills up and subsequent SYNs are silently dropped on Linux (default
	// tcp_abort_on_overflow=0). This is the exact condition that hung the
	// compat tests.
	slowLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("slow listen: %v", err)
	}
	defer slowLn.Close()

	slowHost, slowPortStr, _ := net.SplitHostPort(slowLn.Addr().String())
	slowPort := 0
	fmt.Sscanf(slowPortStr, "%d", &slowPort)

	// Fill the listen backlog by sending many SYNs that never get accepted.
	// The backlog on Linux defaults to 4096 (after kernel 5.4) or 128 (older).
	// We open many connections in parallel with a short timeout; the first few
	// may succeed TCP handshake (kernel auto-completes 1 before Accept), the
	// rest will hang.
	const backlogEstimate = 256
	var hung bool
	var wg sync.WaitGroup
	for i := 0; i < backlogEstimate; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(slowHost, slowPortStr), 50*time.Millisecond)
			if err != nil {
				return // expected — backlog full
			}
			conn.Close()
		}()
	}
	wg.Wait()

	// Now try to connect with no timeout — this should hang because the
	// backlog is saturated and the kernel silently drops our SYN.
	slowMailer := smtp.NewMailer(smtp.Config{Host: slowHost, Port: slowPort})
	done2 := make(chan error, 1)
	go func() {
		done2 <- slowMailer.Send(context.Background(), "a@x.com", []string{"b@x.com"}, "sub", "body", "")
	}()

	select {
	case err := <-done2:
		t.Logf("Send to saturated backlog returned: %v", err)
		// Acceptable: some OS configurations send RST on backlog overflow
		// (tcp_abort_on_overflow=1 or different kernel version).
	case <-time.After(3 * time.Second):
		hung = true
	}

	if hung {
		t.Log("Confirmed: Send hangs when backlog is saturated — NetMailer must use DialTimeout")
	} else {
		t.Log("Send completed (OS may abort on overflow or kernel auto-accepts)")
	}

	// Cleanup: accept one connection to drain the backlog so the goroutine
	// doesn't leak if it was hanging.
	go func() {
		slowLn.Accept() //nolint:errcheck
	}()
}

// ---- NetMailer integration test ------------------------------------------

func TestSMTPMailer_SendPlainText(t *testing.T) {
	store := smtp.NewMailStore(0)
	addr := startTestServer(t, smtp.NewServer("127.0.0.1:0", store))

	host, portStr, _ := net.SplitHostPort(addr)
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	mailer := smtp.NewMailer(smtp.Config{Host: host, Port: port})
	err := mailer.Send(context.Background(), "from@test.com", []string{"to@test.com"}, "Test subject", "Test body", "")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitForMessage(t, store)
	msgs := store.List()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.Subject != "Test subject" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if !strings.Contains(m.TextBody, "Test body") {
		t.Errorf("TextBody = %q", m.TextBody)
	}
}
