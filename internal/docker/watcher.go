package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/overcast-sh/overcast/internal/events"
	"go.uber.org/zap"
)

// Watcher listens to the Docker Engine events stream and publishes typed
// events on an events.Bus. Only Overcast-managed resources are tracked — both
// containers and networks — but the two are filtered in different places, and
// that is not a style choice (see stream).
//
// Usage:
//
//	w := docker.NewWatcher(client, bus, logger)
//	go w.Run(ctx) // blocks until ctx is cancelled
type Watcher struct {
	client    *Client
	bus       *events.Bus
	logger    *zap.Logger
	tracker   *Tracker
	attempted bool

	// verifyNetwork re-reads one network against the spec this process
	// resolved for it and records the result. Supplied by the Supervisor,
	// which is the only thing that holds the specs; nil for a watcher built
	// without one, which then reports nothing on a create.
	verifyNetwork func(ctx context.Context, name string)
}

// networkVerifyTimeout bounds the one inspect a network `create` costs. It runs
// on the event-stream goroutine, so it has to be short: an unbounded read
// against a wedged daemon would stall every container event behind it, and a
// re-verification that has not answered in this long has been overtaken by the
// next probe anyway.
const networkVerifyTimeout = 5 * time.Second

// DaemonConnectedPayload identifies the Docker client whose event stream has
// connected. A process can watch multiple daemon sockets, so services compare
// this pointer with the client they were wired to before reconciling.
type DaemonConnectedPayload struct {
	Client      *Client `json:"-"`
	Reconnected bool    `json:"reconnected"`
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

func NewWatcherWithTracker(client *Client, bus *events.Bus, logger *zap.Logger, tracker *Tracker) *Watcher {
	w := NewWatcher(client, bus, logger)
	w.tracker = tracker
	return w
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
		reconnected := w.attempted
		w.attempted = true
		err := w.stream(ctx, reconnected)
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
func (w *Watcher) stream(ctx context.Context, reconnected bool) error {
	filters := url.Values{}
	// Docker filters JSON: the two resource types Overcast watches, limited to
	// the actions it acts on.
	//
	// **No label filter, and that is the fix rather than an oversight.** The
	// Engine reports an event's actor attributes, and a `label=` filter is
	// matched against them — but a *network* event's attributes are only `name`
	// and `type`. No labels are sent for a network on any Engine this has been
	// checked against (29.x reproduces it exactly), so a stream filtered on
	// `overcast.managed=true` delivers container events and **not one network
	// event of any kind**. Everything built on the network half was therefore
	// inert on a real daemon: the destroy that stops health reporting a network
	// that is gone (#1583) never arrived, and neither would the create this
	// re-verifies on (#1599).
	//
	// Container events do carry their labels, so the container filter moves
	// into dispatch and matches exactly what the daemon used to apply. Networks
	// are gated on what this process knows about the name instead — a record to
	// forget, a spec to verify against — which is a stronger gate than the
	// label anyway: it is scoped to the networks this configuration manages
	// rather than to every network Overcast has ever labelled.
	filtersJSON := `{"type":["container","network"],` +
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
	w.bus.Publish(ctx, events.Event{
		Type:    events.DockerDaemonConnected,
		Payload: DaemonConnectedPayload{Client: w.client, Reconnected: reconnected},
	})

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
		// The filter the daemon used to apply, applied here instead — see
		// stream. A container Overcast did not create is not its business:
		// publishing one would have services reconciling against somebody
		// else's containers.
		if de.Actor.Attributes[LabelManaged] != "true" {
			return
		}
		w.dispatchContainer(ctx, de)
	case "network":
		w.dispatchNetwork(ctx, de)
	default:
		return
	}
	w.recordTrackerEvent(ctx, de)
}

// recordTrackerEvent records the event type and time in the Docker status
// tracker so the health endpoint can report the last observed Docker daemon
// event — a useful signal that the daemon is alive and communicating.
func (w *Watcher) recordTrackerEvent(ctx context.Context, de *dockerEvent) {
	if w.tracker == nil {
		return
	}
	w.tracker.RecordDockerEvent(de.Type+":"+de.Action, time.Unix(de.Time, 0))

	// A network that has been destroyed has no state left to report, so the
	// last thing the startup probe knew about it stops being a fact and starts
	// being a memory. `overcast network reset` runs in another process and
	// removes the network before rebuilding it, so without this the drift it
	// was run to fix keeps its advisory standing for the life of this daemon —
	// the operator does the thing they were told to do, watches it succeed, and
	// nothing they can see changes (#1583).
	//
	// The create is verified too, since the Supervisor now holds the resolved
	// specs (#1599). Verified, never repaired: `overcast network reset` is
	// usually the reason both of these events are arriving, and a repair from
	// here would land between its remove and its create — see VerifyNetwork.
	//
	// The pair arrives in order on one stream and is handled in order on this
	// goroutine, which is what makes the forget safe. A destroy delivered after
	// the probe recorded the rebuilt network erases the entry (#1601); the
	// create behind it puts back one that was read a moment ago, so the window
	// closes itself in the time it takes to deliver one event rather than
	// lasting until the next probe.
	//
	// Connect and disconnect are untouched: a container joining a network says
	// nothing about whether the network matches its spec.
	//
	// Both calls are gated on the name rather than on a label the daemon does
	// not send (see stream): ForgetNetwork drops only a name the report holds,
	// and VerifyNetwork acts only on a name this process resolved a spec for.
	// A network nothing here manages reaches neither.
	if de.Type != "network" {
		return
	}
	name := de.Actor.Attributes["name"]
	switch de.Action {
	case "destroy":
		w.tracker.ForgetNetwork(name)
	case "create":
		w.verifyOnCreate(ctx, name)
	}
}

// verifyOnCreate re-reads a newly created network and records what it is, so a
// network that was just repaired reports `ok` rather than dropping out of
// /_overcast/health until the next probe (#1599).
//
// Inline on the event goroutine, under a short timeout. One inspect is the
// budget the whole verification is built around — cheap enough to run for every
// network on every startup — and running it here rather than in a goroutine is
// what keeps it ordered behind the destroy it usually follows.
func (w *Watcher) verifyOnCreate(ctx context.Context, name string) {
	if w.verifyNetwork == nil || name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, networkVerifyTimeout)
	defer cancel()
	w.verifyNetwork(ctx, name)
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
		Instance:    attrs[LabelInstance],
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

	e := events.Event{
		Type:    eventType,
		Payload: payload,
	}
	if de.Time > 0 {
		e.Time = time.Unix(de.Time, 0)
	}
	w.bus.Publish(ctx, e)
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
	return info.ExitReason()
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
