package smtp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// maxConcurrentConnections limits how many SMTP sessions the mock server
// handles at once. When the limit is reached, Accept blocks until a slot
// frees up, applying natural TCP-backlog backpressure. This prevents the
// mock server from exhausting OS resources (file descriptors, goroutines)
// and guarantees the main Overcast HTTP server is never starved.
const maxConcurrentConnections = 64

// Server is a minimal RFC 5321 SMTP server that captures all inbound messages
// into a MailStore. It implements just enough of the SMTP protocol to accept
// messages from standard smtp clients (including Go's net/smtp package):
// EHLO/HELO, MAIL FROM, RCPT TO, DATA, RSET, NOOP, QUIT.
//
// It does not perform any authentication or TLS. It is intended for local
// development use only — never expose on a public network.
//
// Usage:
//
//	srv := smtp.NewServer("127.0.0.1:1025", store)
//	addr, err := srv.Listen()   // bind the TCP socket (synchronous)
//	go srv.Serve(ctx)            // start accepting (runs until ctx is done)
type Server struct {
	addr  string
	store *MailStore

	// OnMessage is an optional callback invoked after every successfully
	// captured message has been stored. It runs in the connection goroutine
	// and must not block. Set before calling Serve.
	OnMessage func(*CapturedMessage)

	mu       sync.Mutex
	listener net.Listener
	wg       sync.WaitGroup

	// sema bounds concurrent connection handlers so the mock server can never
	// exhaust file descriptors or goroutines regardless of inbound load.
	sema chan struct{}
}

// NewServer creates a Server that will bind to addr (e.g. "127.0.0.1:1025")
// and store captured messages in store. Use ":0" for a random OS-assigned port.
func NewServer(addr string, store *MailStore) *Server {
	return &Server{
		addr:  addr,
		store: store,
		sema:  make(chan struct{}, maxConcurrentConnections),
	}
}

// Listen binds the TCP socket and returns the actual address (useful when the
// configured port is 0). It must be called before Serve.
func (s *Server) Listen() (string, error) {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return "", fmt.Errorf("smtp server: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()
	return ln.Addr().String(), nil
}

// Serve begins accepting connections. It blocks until ctx is done and all
// in-flight connections are closed. Listen must be called first.
func (s *Server) Serve(ctx context.Context) {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln == nil {
		return
	}

	// Close listener when context is cancelled.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	acceptBackoff := 10 * time.Millisecond
	const maxAcceptBackoff = 500 * time.Millisecond

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			// Transient accept error (EMFILE, ENFILE, etc.) —
			// back off and retry. Exponential backoff prevents
			// a busy-loop from starving the HTTP server.
			time.Sleep(acceptBackoff)
			acceptBackoff *= 2
			if acceptBackoff > maxAcceptBackoff {
				acceptBackoff = maxAcceptBackoff
			}
			continue
		}
		// Reset backoff on successful accept.
		acceptBackoff = 10 * time.Millisecond

		// Acquire a concurrency slot. This blocks when the server is at
		// capacity, which prevents Accept from returning more connections
		// than we can handle — the TCP listen backlog provides natural
		// backpressure.
		select {
		case s.sema <- struct{}{}:
		case <-ctx.Done():
			_ = conn.Close()
			break
		}

		s.wg.Add(1)
		go func() {
			defer func() {
				_ = recover() // A connection handler must never crash the server.
			}()
			defer s.wg.Done()
			defer func() { <-s.sema }() // release concurrency slot

			connCtx, connCancel := context.WithCancel(ctx)
			defer connCancel()

			// Deadline-setter goroutine: when the server is shutting
			// down or the connection handler returns, force-close the
			// raw conn so any stuck Read/Write unblocks promptly.
			done := make(chan struct{})
			go func() {
				select {
				case <-connCtx.Done():
				case <-done:
				}
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			}()
			s.handleConn(conn)
			close(done)
		}()
	}

	s.wg.Wait()
}

// Close closes the listener, causing Serve to stop accepting new connections.
// Any in-flight connections are allowed to finish.
func (s *Server) Close() error {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

// Start is a convenience method that calls Listen then Serve. It is provided
// for use in tests where the caller does not need the bound address.
func (s *Server) Start(ctx context.Context) error {
	if _, err := s.Listen(); err != nil {
		return err
	}
	s.Serve(ctx)
	return nil
}

// handleConn processes a single SMTP client connection.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	s.writeLine(rw, "220 overcast SMTP mock ready")

	var (
		from string
		to   []string
	)

	for {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			s.writeLine(rw, "250-overcast")
			s.writeLine(rw, "250-SIZE 10485760")
			s.writeLine(rw, "250 OK")

		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from = extractAngle(line[len("MAIL FROM:"):])
			s.writeLine(rw, "250 OK")

		case strings.HasPrefix(cmd, "RCPT TO:"):
			addr := extractAngle(line[len("RCPT TO:"):])
			to = append(to, addr)
			s.writeLine(rw, "250 OK")

		case cmd == "DATA":
			s.writeLine(rw, "354 Start input, end with <CRLF>.<CRLF>")
			raw, dataErr := s.readData(rw)
			if dataErr != nil {
				return
			}
			msg := newCapturedMessage(from, to, raw)
			s.store.Add(msg)
			if s.OnMessage != nil {
				s.OnMessage(msg)
			}
			s.writeLine(rw, "250 OK: message "+msg.ID)
			// Reset envelope for next message.
			from = ""
			to = nil

		case cmd == "RSET":
			from = ""
			to = nil
			s.writeLine(rw, "250 OK")

		case cmd == "NOOP":
			s.writeLine(rw, "250 OK")

		case cmd == "QUIT":
			s.writeLine(rw, "221 Bye")
			return

		default:
			s.writeLine(rw, "500 Unrecognized command")
		}
	}
}

// readData reads lines until a lone "." is received, per RFC 5321 §4.5.2.
func (s *Server) readData(rw *bufio.ReadWriter) (string, error) {
	var sb strings.Builder
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return "", err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			break
		}
		// Transparency: leading "." on a line is escaped as ".." by client.
		if strings.HasPrefix(trimmed, "..") {
			trimmed = trimmed[1:]
		}
		sb.WriteString(trimmed)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func (s *Server) writeLine(rw *bufio.ReadWriter, line string) {
	_, _ = rw.WriteString(line + "\r\n")
	_ = rw.Flush()
}

// extractAngle extracts the address from "<addr>" or returns the trimmed string.
func extractAngle(s string) string {
	s = strings.TrimSpace(s)
	// Strip SIZE parameter if present (e.g. "<foo@bar> SIZE=1234")
	if i := strings.Index(s, " "); i != -1 {
		s = s[:i]
	}
	s = strings.Trim(s, "<>")
	return strings.TrimSpace(s)
}
