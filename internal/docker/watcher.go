package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"go.uber.org/zap"
)

// Watcher listens to the Docker Engine events stream and publishes typed
// events on an events.Bus. Only Overcast-managed resources (those with the
// overcast.managed label) are tracked — both containers and networks.
//
// Usage:
//
//	w := docker.NewWatcher(client, bus, logger)
//	go w.Run(ctx) // blocks until ctx is cancelled
type Watcher struct {
	client *Client
	bus    *events.Bus
	logger *zap.Logger
}

// NewWatcher creates a Watcher that translates Docker container and network
// events into bus events. Call Run to start watching.
func NewWatcher(client *Client, bus *events.Bus, logger *zap.Logger) *Watcher {
	return &Watcher{
		client: client,
		bus:    bus,
		logger: logger.Named("docker.watcher"),
	}
}

// dockerEvent mirrors the subset of the Docker Engine /events JSON we care about.
type dockerEvent struct {
	Type   string `json:"Type"`   // "container", "network"
	Action string `json:"Action"` // container: "start","stop",… / network: "create","destroy","connect","disconnect"
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// Run connects to the Docker events stream and publishes bus events for
// managed containers. It reconnects automatically with exponential backoff
// when the stream drops. Run blocks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	const (
		minBackoff = 500 * time.Millisecond
		maxBackoff = 30 * time.Second
	)
	backoff := minBackoff

	for {
		start := time.Now()
		err := w.stream(ctx)
		if ctx.Err() != nil {
			return // clean shutdown
		}
		if time.Since(start) > maxBackoff {
			backoff = minBackoff
		}
		w.logger.Warn("docker event stream disconnected, reconnecting",
			zap.Error(err), zap.Duration("backoff", backoff))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// stream opens the Docker events endpoint with label filters and reads
// newline-delimited JSON until the stream ends or ctx is cancelled.
func (w *Watcher) stream(ctx context.Context) error {
	filters := url.Values{}
	// Docker filters JSON: only Overcast-managed resources, both container and
	// network event types, limited to the actions we care about.
	filtersJSON := `{"label":["` + LabelManaged + `=true"],"type":["container","network"],` +
		`"event":["start","stop","die","kill","oom","health_status","create","destroy","connect","disconnect"]}`
	filters.Set("filters", filtersJSON)

	path := "/v1.45/events?" + filters.Encode()
	resp, err := w.client.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("docker events: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker events: status %d", resp.StatusCode)
	}

	w.logger.Info("docker event stream connected")

	scanner := bufio.NewScanner(resp.Body)
	// Docker events are small JSON lines; 64 KB is more than enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var de dockerEvent
		if err := json.Unmarshal(line, &de); err != nil {
			w.logger.Warn("docker event: unmarshal", zap.Error(err))
			continue
		}
		w.dispatch(ctx, &de)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("docker events: scan: %w", err)
	}
	return fmt.Errorf("docker events: stream ended")
}

// dispatch translates a raw Docker event into a typed bus event and publishes it.
func (w *Watcher) dispatch(ctx context.Context, de *dockerEvent) {
	switch de.Type {
	case "container":
		w.dispatchContainer(ctx, de)
	case "network":
		w.dispatchNetwork(ctx, de)
	}
}

// dispatchContainer handles container lifecycle events.
func (w *Watcher) dispatchContainer(ctx context.Context, de *dockerEvent) {
	attrs := de.Actor.Attributes
	payload := events.DockerContainerPayload{
		ContainerID: de.Actor.ID,
		Action:      de.Action,
		ExitCode:    attrs["exitCode"],
		Service:     attrs[LabelService],
		ResourceID:  attrs[LabelResourceID],
		Image:       attrs["image"],
	}

	var eventType events.Type
	switch de.Action {
	case "start":
		eventType = events.DockerContainerStarted
	case "die":
		eventType = events.DockerContainerDied
		payload.Reason = w.inspectDieReason(ctx, de.Actor.ID)
	case "stop":
		eventType = events.DockerContainerStopped
	case "kill":
		// "kill" with OOM reason is handled via "oom" action; a plain kill
		// maps to the stopped event.
		eventType = events.DockerContainerStopped
	case "oom":
		eventType = events.DockerContainerOOM
	case "health_status":
		eventType = events.DockerContainerHealthStatus
	default:
		return // filtered out by Docker, but ignore just in case
	}

	w.logger.Debug("docker event",
		zap.String("type", "container"),
		zap.String("action", de.Action),
		zap.String("container", de.Actor.ID[:min(12, len(de.Actor.ID))]),
		zap.String("service", payload.Service),
		zap.String("resource", payload.ResourceID),
	)

	w.bus.Publish(ctx, events.Event{
		Type:    eventType,
		Payload: payload,
	})
}

// inspectDieReason fetches State from the Docker daemon after a "die" event and
// derives a human-readable reason. Returns empty string for clean exits (exit
// code 0, no error, not OOM-killed) and also if the inspect call fails — the
// die event is still published either way, just without an enriched reason.
func (w *Watcher) inspectDieReason(ctx context.Context, containerID string) string {
	inspectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	info, err := w.client.InspectContainer(inspectCtx, containerID)
	if err != nil {
		// Container may already be removed (e.g. --rm), or the daemon may be
		// slow. Neither should block event processing.
		w.logger.Debug("docker event: inspect on die failed",
			zap.String("container", containerID[:min(12, len(containerID))]),
			zap.Error(err))
		return ""
	}
	switch {
	case info.State.OOMKilled:
		return "oom"
	case info.State.Error != "":
		return info.State.Error
	case info.State.ExitCode != 0:
		return fmt.Sprintf("exit %d", info.State.ExitCode)
	default:
		return ""
	}
}

// dispatchNetwork handles network lifecycle events.
func (w *Watcher) dispatchNetwork(ctx context.Context, de *dockerEvent) {
	attrs := de.Actor.Attributes
	payload := events.DockerNetworkPayload{
		NetworkID:   de.Actor.ID,
		Action:      de.Action,
		Service:     attrs[LabelService],
		ResourceID:  attrs[LabelResourceID],
		ContainerID: attrs["container"],
	}

	var eventType events.Type
	switch de.Action {
	case "create":
		eventType = events.DockerNetworkCreated
	case "destroy":
		eventType = events.DockerNetworkDestroyed
	case "connect":
		eventType = events.DockerNetworkConnect
	case "disconnect":
		eventType = events.DockerNetworkDisconnect
	default:
		return
	}

	w.logger.Debug("docker event",
		zap.String("type", "network"),
		zap.String("action", de.Action),
		zap.String("network", de.Actor.ID[:min(12, len(de.Actor.ID))]),
		zap.String("service", payload.Service),
		zap.String("resource", payload.ResourceID),
	)

	w.bus.Publish(ctx, events.Event{
		Type:    eventType,
		Payload: payload,
	})
}
