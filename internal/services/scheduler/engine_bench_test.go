package scheduler

// Benchmarks for the engine tick.
//
// The metric is wall-clock time for one tick's worth of firings to be
// *delivered*, not for tick() to return: the useful property is how long the
// emulator takes to get every due schedule to its target, and moving delivery
// off the tick goroutine changes where that time is spent, not only how much of
// it there is.
//
// The case that matters is a slow target. A benchmark in which every target
// answers instantly measures the queue's own overhead and nothing else, so both
// shapes are here — `all_fast` is the no-regression check, `16_slow` is the
// measurement. Slowness is a real time.Sleep inside the delivery handler, so
// this reports wall time; allocation counts are not the signal and are not
// collected. Run it with -benchtime=Nx rather than a duration, because one
// iteration of the slow case takes hundreds of milliseconds:
//
//	go test -run '^$' -bench BenchmarkEngineTick -benchtime 10x -count 3 ./internal/services/scheduler/

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowTargetDelay is how long a "slow" target takes to answer one delivery.
// Long enough to dominate every other cost in the tick, short enough that a
// serial run of the slow case still finishes in well under a second.
const slowTargetDelay = 20 * time.Millisecond

// benchTargets is the sink every benchmarked firing lands in. A delivery whose
// body names a queue prefixed "slow-" is held for slowTargetDelay; the
// completion is counted *after* that wait, so the round is only reported
// finished once every delivery has actually been answered.
type benchTargets struct {
	mu   sync.Mutex
	seen int
	want int
	done chan struct{}
}

func (t *benchTargets) round(want int) <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen, t.want, t.done = 0, want, make(chan struct{})
	return t.done
}

func (t *benchTargets) delivered() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen
}

func (t *benchTargets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if strings.Contains(string(body), `"QueueUrl":"slow-`) {
		time.Sleep(slowTargetDelay)
	}
	t.mu.Lock()
	t.seen++
	if t.seen == t.want {
		close(t.done)
	}
	t.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func BenchmarkEngineTick(b *testing.B) {
	cases := []struct {
		name  string
		total int
		slow  int
	}{
		{name: "64_schedules_all_fast", total: 64, slow: 0},
		{name: "64_schedules_16_slow", total: 64, slow: 16},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			targets := &benchTargets{}

			// Each round gets its own service and store, so no firing from the
			// previous round can still be in flight when the next one ticks.
			// Setup and teardown are untimed.
			setup := func() (*Service, <-chan struct{}) {
				s, _ := newFiringService(b, targets)
				for i := 0; i < tc.total; i++ {
					name := fmt.Sprintf("fast-%03d", i)
					if i < tc.slow {
						name = fmt.Sprintf("slow-%03d", i)
					}
					seedSchedule(b, s, "us-east-1", name, sqsTarget(name))
				}
				return s, targets.round(tc.total)
			}

			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				b.StopTimer()
				s, done := setup()
				b.StartTimer()

				s.tick()
				select {
				case <-done:
				case <-time.After(60 * time.Second):
					b.Fatalf("only %d of %d schedules were delivered", targets.delivered(), tc.total)
				}

				b.StopTimer()
				stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := s.Stop(stopCtx); err != nil {
					b.Fatalf("stop scheduler: %v", err)
				}
				cancel()
				b.StartTimer()
			}
		})
	}
}
