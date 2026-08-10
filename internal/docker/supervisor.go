package docker

import (
	"context"
	"sync"

	"github.com/Neaox/overcast/internal/events"
	"go.uber.org/zap"
)

// ServiceConfig describes a single service's Docker requirements.
// The Supervisor uses this to probe the socket and wire the Docker client
// into the service.
//
// Note the absence of a network: they are a property of the deployment, not of
// a service. Every container Overcast starts shares the same two planes (see
// docs/dev/container-networking.md), so the Supervisor ensures them once per
// daemon rather than once per service.
type ServiceConfig struct {
	// Name is used for logging ("rds", "ecs", "lambda").
	Name string
	// Socket is the Docker daemon socket path (e.g. /var/run/docker.sock).
	Socket string
}

// ServiceResult is returned per-service after a successful probe.
type ServiceResult struct {
	Name   string
	Client *Client
}

// Supervisor centralises Docker lifecycle management for the entire process.
// It deduplicates probes (one per unique socket path), creates per-service
// networks, runs a single event watcher per Docker daemon, and provides
// startup reconciliation.
//
// Usage:
//
//	sup := docker.NewSupervisor(bus, logger)
//	results := sup.Probe(ctx, []ServiceConfig{...})
//	// wire results into services
//	sup.Run(ctx)   // blocks — starts watchers; returns when ctx is done
//	sup.Close()    // called during shutdown
type Supervisor struct {
	bus    *events.Bus
	logger *zap.Logger

	tracker *Tracker

	mu      sync.Mutex
	clients map[string]*Client // socket path → client (deduplicated)
	done    chan struct{}      // closed by Close to signal shutdown
}

// NewSupervisor creates a Supervisor that will publish Docker container events
// on the provided bus.
func NewSupervisor(bus *events.Bus, logger *zap.Logger) *Supervisor {
	return &Supervisor{
		bus:     bus,
		logger:  logger.Named("docker"),
		clients: make(map[string]*Client),
		done:    make(chan struct{}),
	}
}

// NewSupervisorWithTracker creates a Supervisor that also records
// per-service Docker connectivity in the given Tracker for health reporting.
func NewSupervisorWithTracker(bus *events.Bus, logger *zap.Logger, tracker *Tracker) *Supervisor {
	s := NewSupervisor(bus, logger)
	s.tracker = tracker
	return s
}

// Probe probes Docker for each ServiceConfig, ensuring networks exist on every
// daemon reached. Configs sharing the same socket path reuse a single client
// connection and share a single availability probe. Returns one ServiceResult
// per successful config; configs that fail to probe are logged and skipped.
func (s *Supervisor) Probe(ctx context.Context, configs []ServiceConfig, networks []string) []ServiceResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	type probeEntry struct {
		result *ProbeResult
		err    error
	}
	// One probe per daemon. Probe verifies connectivity *and* ensures the
	// planes, both of which are per-daemon properties, so services sharing a
	// socket share the result.
	probeCache := make(map[string]*probeEntry) // socket → result

	var results []ServiceResult
	for _, cfg := range configs {
		log := s.logger.With(zap.String("service", cfg.Name))

		entry, ok := probeCache[cfg.Socket]
		if !ok {
			pr, err := Probe(cfg.Socket, networks, log)
			entry = &probeEntry{result: pr, err: err}
			probeCache[cfg.Socket] = entry
			if err == nil {
				RemoveLegacyNetworks(ctx, pr.Client, log)
			}
		}

		if entry.err != nil {
			log.Warn("Docker not available — service will be metadata-only",
				zap.String("socket", cfg.Socket), zap.Error(entry.err))
			continue
		}

		// Register the client for watcher deduplication (by socket path).
		s.clients[cfg.Socket] = entry.result.Client

		results = append(results, ServiceResult{
			Name:   cfg.Name,
			Client: entry.result.Client,
		})
		log.Info("Docker wired", zap.String("socket", cfg.Socket))
	}

	if s.tracker != nil {
		s.tracker.RecordProbeResult(configs, results)
	}

	return results
}

// Run starts one Watcher goroutine per unique Docker client. It blocks until
// ctx is cancelled or Close is called. Call this from a goroutine after Probe.
func (s *Supervisor) Run(ctx context.Context) {
	// Merge the caller's context with our done channel so either can stop us.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	s.mu.Lock()
	unique := make(map[string]*Client, len(s.clients))
	for k, v := range s.clients {
		unique[k] = v
	}
	s.mu.Unlock()

	if len(unique) == 0 {
		<-ctx.Done()
		return
	}

	var wg sync.WaitGroup
	for socket, dc := range unique {
		wg.Add(1)
		go func(socket string, dc *Client) {
			defer wg.Done()
			var w *Watcher
			if s.tracker != nil {
				w = NewWatcherWithTracker(dc, s.bus, s.logger.With(zap.String("socket", socket)), s.tracker)
			} else {
				w = NewWatcher(dc, s.bus, s.logger.With(zap.String("socket", socket)))
			}
			w.Run(ctx)
		}(socket, dc)
	}
	wg.Wait()
}

// Close signals all background goroutines (including Probe blockers and
// watchers) to stop. Safe to call before, during, or after Run.
func (s *Supervisor) Close() {
	select {
	case <-s.done:
		// already closed
	default:
		close(s.done)
	}
}

// StatusSnapshot returns the aggregated Docker connectivity state recorded by
// the tracker, or nil when no tracker was configured.
func (s *Supervisor) StatusSnapshot() *Status {
	if s.tracker == nil {
		return nil
	}
	snap := s.tracker.Snapshot()
	return &snap
}
