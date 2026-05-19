package main

// sse_source.go — a hostbridge.Source implementation that tails overcast's
// /_internal/domains/watch SSE endpoint. It reconnects on error with a
// short backoff; the bridge itself is stateless with respect to gaps so the
// reconnect simply re-snapshots.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/hostbridge"
	"github.com/Neaox/overcast/internal/hostbridge/mdns"
)

// sseSource streams domain events from overcastd's SSE endpoint. It is a
// hostbridge.Source.
type sseSource struct {
	endpoint string // e.g. "http://localhost:4566"
	bindIP   net.IP // IP to advertise for every received hostname
	log      *zap.Logger
}

func newSSESource(endpoint string, bindIP net.IP, log *zap.Logger) *sseSource {
	return &sseSource{endpoint: endpoint, bindIP: bindIP, log: log}
}

// domainEnvelope mirrors the shape emitted by internal/router/domains.go.
type domainEnvelope struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Watch opens the SSE connection and returns a channel of events. The
// channel is closed when ctx is cancelled. On transient connection errors
// the goroutine reconnects with a 1-second backoff.
func (s *sseSource) Watch(ctx context.Context) (<-chan hostbridge.Event, error) {
	out := make(chan hostbridge.Event, 64)
	go func() {
		defer close(out)
		backoff := time.Second
		for {
			if err := s.stream(ctx, out); err != nil {
				if ctx.Err() != nil {
					return
				}
				s.log.Warn("bridge: sse stream error, reconnecting", zap.Error(err))
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}()
	return out, nil
}

// stream runs one SSE connection until it errors or ctx is cancelled.
func (s *sseSource) stream(ctx context.Context, out chan<- hostbridge.Event) error {
	url := strings.TrimRight(s.endpoint, "/") + "/_internal/domains/watch"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var env domainEnvelope
		if err := json.Unmarshal([]byte(line[len("data: "):]), &env); err != nil {
			s.log.Warn("bridge: bad sse payload", zap.Error(err), zap.String("line", line))
			continue
		}
		ev := hostbridge.Event{
			Record: mdns.Record{Hostname: env.Name, IP: s.bindIP},
		}
		switch env.Type {
		case "added":
			ev.Type = hostbridge.EventAdded
		case "removed":
			ev.Type = hostbridge.EventRemoved
		default:
			continue
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("sse stream closed")
}
