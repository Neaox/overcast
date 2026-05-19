package lambda

import (
	"context"
	"sync"

	"github.com/Neaox/overcast/internal/events"
)

// exitNotifier bridges Docker watcher bus events to per-container channels,
// replacing per-invocation WaitContainer goroutines. Each in-flight invocation
// registers interest in its container's exit; the bus subscription dispatches
// the event to the right channel.
//
// This eliminates one goroutine + one blocking HTTP connection per active
// invocation, replacing them with a single Docker event stream.
type exitNotifier struct {
	mu    sync.Mutex
	chans map[string]chan string // containerID → exitCode
}

func newExitNotifier() *exitNotifier {
	return &exitNotifier{chans: make(map[string]chan string)}
}

// register returns a channel that will receive the exit code string when the
// container dies. The caller must call unregister when done.
func (en *exitNotifier) register(containerID string) <-chan string {
	ch := make(chan string, 1)
	en.mu.Lock()
	en.chans[containerID] = ch
	en.mu.Unlock()
	return ch
}

// unregister removes the exit channel for a container. Safe to call multiple
// times or after the channel has already been sent to.
func (en *exitNotifier) unregister(containerID string) {
	en.mu.Lock()
	delete(en.chans, containerID)
	en.mu.Unlock()
}

// handleContainerDied is a bus handler for DockerContainerDied events.
// It routes the exit notification to the registered channel, if any.
func (en *exitNotifier) handleContainerDied(_ context.Context, e events.Event) {
	p, ok := e.Payload.(events.DockerContainerPayload)
	if !ok || p.Service != "lambda" {
		return
	}
	en.mu.Lock()
	ch, found := en.chans[p.ContainerID]
	en.mu.Unlock()
	if found {
		select {
		case ch <- p.ExitCode:
		default:
		}
	}
}
