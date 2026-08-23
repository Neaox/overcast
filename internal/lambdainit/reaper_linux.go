//go:build linux

package lambdainit

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
)

// reaper is the init's PID 1 duty: every process that dies anywhere under it is
// waited for, including grandchildren orphaned by a child that did not wait for
// its own. Nothing else in the init calls wait4, so there is exactly one
// consumer of exit statuses and no race over who collects them.
//
// os/exec's own Wait is deliberately not used. wait4(-1) reaps whichever child
// died, so a second waiter would steal statuses from this one; children are
// started with Start and their statuses arrive here.
type reaper struct {
	diag *diagLog
	sig  chan os.Signal

	mu      sync.Mutex
	watched map[int]chan syscall.WaitStatus
	orphans uint64

	stop chan struct{}
	done chan struct{}
}

func newReaper(diag *diagLog) *reaper {
	return &reaper{
		diag:    diag,
		sig:     make(chan os.Signal, 1),
		watched: make(map[int]chan syscall.WaitStatus),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (r *reaper) start() {
	signal.Notify(r.sig, syscall.SIGCHLD)
	go func() {
		defer close(r.done)
		for {
			select {
			case <-r.sig:
				r.reapAll()
			case <-r.stop:
				r.reapAll()
				return
			}
		}
	}()
}

func (r *reaper) shutdown() {
	signal.Stop(r.sig)
	close(r.stop)
	<-r.done
}

// spawn starts cmd and returns the channel its exit status will arrive on.
// Start and the registration happen under the same lock the reaper holds while
// it calls wait4, so a process that dies before this returns cannot have its
// status thrown away as an orphan.
func (r *reaper) spawn(cmd *exec.Cmd) (<-chan syscall.WaitStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan syscall.WaitStatus, 1)
	r.watched[cmd.Process.Pid] = ch
	return ch, nil
}

// reapAll collects every process that has exited, which is what makes one
// SIGCHLD enough: signals coalesce, exit statuses do not.
func (r *reaper) reapAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return
		}
		if ch, ok := r.watched[pid]; ok {
			delete(r.watched, pid)
			ch <- ws
			continue
		}
		r.orphans++
		r.diag.printf("reaped orphan pid=%d %s", pid, describeStatus(ws))
	}
}

// orphanCount reports how many processes the init reaped that it did not start
// itself.
func (r *reaper) orphanCount() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.orphans
}

// exitCodeFor turns a wait status into the code the init exits with: the
// child's own status, or 128+signal for a killed child, exactly as a shell
// reports it. The host's container-exit handling then sees what it would have
// seen had the runtime been PID 1 itself.
func exitCodeFor(ws syscall.WaitStatus) int {
	if ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ws.ExitStatus()
}

func describeStatus(ws syscall.WaitStatus) string {
	if ws.Signaled() {
		return "signal=" + ws.Signal().String()
	}
	return "code=" + strconv.Itoa(ws.ExitStatus())
}
