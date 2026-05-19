package docker

import (
	"context"
	"sync"

	"github.com/Neaox/overcast/internal/events"
	"go.uber.org/zap"
)

// ServiceConfig describes a single service's Docker requirements.
// The Supervisor uses this to probe the socket, create the network, and
// wire the Docker client into the service.
type ServiceConfig struct {
	// Name is used for logging ("rds", "ecs", "lambda").
	Name string
	// Socket is the Docker daemon socket path (e.g. /var/run/docker.sock).
	Socket string
	// Network is the Docker network to create for this service.
	Network string
}

// ServiceResult is returned per-service after a successful probe.
type ServiceResult struct {
	Name      string
	Client    *Client
	NetworkID string
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

// Probe probes Docker for each ServiceConfig. Configs sharing the same socket
// path reuse a single client connection and share a single availability probe.
// Each config gets its own network created. Returns one ServiceResult per
// successful config. Configs that fail to probe are logged and skipped.
func (s *Supervisor) Probe(ctx context.Context, configs []ServiceConfig) []ServiceResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	type probeEntry struct {
		result *ProbeResult
		err    error
	}
	// probeCache deduplicates probes per socket+network pair. The Probe()
	// function both verifies connectivity *and* creates the network, so we
	// must call it once per unique (socket, network) combination.
	probeCache := make(map[string]*probeEntry) // "socket\000network" → result

	var results []ServiceResult
	for _, cfg := range configs {
		log := s.logger.With(zap.String("service", cfg.Name))
		cacheKey := cfg.Socket + "\x00" + cfg.Network

		entry, ok := probeCache[cacheKey]
		if !ok {
			pr, err := Probe(cfg.Socket, cfg.Network, log)
			entry = &probeEntry{result: pr, err: err}
			probeCache[cacheKey] = entry
		}

		if entry.err != nil {
			log.Warn("Docker not available — service will be metadata-only",
				zap.String("socket", cfg.Socket), zap.Error(entry.err))
			continue
		}

		// Register the client for watcher deduplication (by socket path).
		s.clients[cfg.Socket] = entry.result.Client

		results = append(results, ServiceResult{
			Name:      cfg.Name,
			Client:    entry.result.Client,
			NetworkID: entry.result.NetworkID,
		})
		log.Info("Docker wired",
			zap.String("socket", cfg.Socket),
			zap.String("network", cfg.Network))
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
			w := NewWatcher(dc, s.bus, s.logger.With(zap.String("socket", socket)))
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
