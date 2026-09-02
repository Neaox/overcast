package dockertest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
)

// The names below are verbatim from a developer machine that had accumulated
// twenty-two of them; the shared ones are what that machine's live instance
// was running on at the time, and what a sweep must never touch.
func TestIsTestNetwork(t *testing.T) {
	for _, name := range []string{
		"overcast_ecs_test_1787472886987880100",
		"overcast_ecs_test_1787472886987880100_control",
		"overcast_rds_master_test_00362be8e11f",
		"overcast_rds_master_test_00362be8e11f_control",
	} {
		if !IsTestNetwork(name) {
			t.Errorf("IsTestNetwork(%q) = false, want true", name)
		}
	}
	for _, name := range []string{
		"overcast",
		"overcast_control",
		"overcast_ecs",
		"overcast_ecs_control",
		// EFS's fixed-name planes: shared across runs, recreated by the next one.
		"overcast_efs_test",
		"overcast_efs_test_control",
		// No suite segment between the prefix and the marker.
		"overcast_test_1787472886987880100",
		// An id that is not a nanotime or a hex suffix — the shape an instance
		// name could take by accident.
		"overcast_agent_test_run",
		"overcast_ecs_test_abc",
		"overcast_ecs_test_",
		// Not anchored at either end.
		"myovercast_ecs_test_1787472886987880100",
		"overcast_ecs_test_1787472886987880100_extra",
		"Overcast_ecs_test_1787472886987880100",
	} {
		if IsTestNetwork(name) {
			t.Errorf("IsTestNetwork(%q) = true, want false", name)
		}
	}
}

func TestRemoveOwnedEvictsWhatIsLeftThenRemoves(t *testing.T) {
	fd, dc := newFakeDaemon(t)
	old := time.Now().Add(-time.Hour)
	fd.nets["overcast_ecs_test_1787472886987880100"] = &fakeNet{created: old, containers: map[string]fakeContainer{
		"c1": {name: "overcast-ecs-task-abc", managed: true},
		"c2": {name: "devcontainer", managed: false},
	}}
	// Empty, but the daemon says "active endpoints" twice before agreeing —
	// the asynchronous-removal window RemoveOwned waits out.
	fd.nets["overcast_ecs_test_1787472886987880100_control"] = &fakeNet{created: old, refusals: 2}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var log logLines
	RemoveOwned(ctx, dc, []string{
		"overcast_ecs_test_1787472886987880100",
		"overcast_ecs_test_1787472886987880100_control",
		"overcast_ecs_test_1787472886987880100_never_created",
		"",
	}, log.f)

	if got, want := fd.removedContainers, []string{"c1"}; !slices.Equal(got, want) {
		t.Errorf("removed containers = %v, want %v (only the managed one)", got, want)
	}
	if got, want := fd.disconnected, []string{"c2@overcast_ecs_test_1787472886987880100"}; !slices.Equal(got, want) {
		t.Errorf("disconnected = %v, want %v (the unmanaged one is detached, not removed)", got, want)
	}
	if got, want := fd.removedNets, []string{
		"overcast_ecs_test_1787472886987880100",
		"overcast_ecs_test_1787472886987880100_control",
	}; !slices.Equal(got, want) {
		t.Errorf("removed networks = %v, want %v", got, want)
	}
	log.mustContain(t, "still held container overcast-ecs-task-abc; removed it")
	log.mustContain(t, "still held devcontainer, which is not Overcast's; disconnected it")
	log.mustNotContain(t, "never_created")
	log.mustNotContain(t, "was not removed")
}

func TestRemoveOwnedReportsWhatItCouldNotRemove(t *testing.T) {
	fd, dc := newFakeDaemon(t)
	fd.nets["overcast_rds_master_test_00362be8e11f_control"] = &fakeNet{created: time.Now(), refusals: 1 << 20}

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	var log logLines
	RemoveOwned(ctx, dc, []string{"overcast_rds_master_test_00362be8e11f_control"}, log.f)

	if len(fd.removedNets) != 0 {
		t.Fatalf("removed %v, want nothing", fd.removedNets)
	}
	log.mustContain(t, "overcast_rds_master_test_00362be8e11f_control was not removed")
	log.mustContain(t, "has active endpoints")
	log.mustContain(t, "make docker-clean-test-networks")
}

func TestSweepRemovesOnlyEmptyOldPerTestNetworks(t *testing.T) {
	fd, dc := newFakeDaemon(t)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	live := map[string]fakeContainer{"c0": {name: "overcast-rds-mydb", managed: true}}
	fd.nets["overcast"] = &fakeNet{created: old, containers: live}
	fd.nets["overcast_control"] = &fakeNet{created: old}
	fd.nets["overcast_efs_test_control"] = &fakeNet{created: old}
	fd.nets["overcast_ecs_test_1787472886987880100_control"] = &fakeNet{created: old}
	fd.nets["overcast_ecs_test_1787614952211031700"] = &fakeNet{created: old}
	fd.nets["overcast_ecs_test_1787614952211031700_control"] = &fakeNet{created: old, containers: live}
	fd.nets["overcast_rds_master_test_00362be8e11f"] = &fakeNet{created: now.Add(-time.Minute)}

	opts := SweepOptions{MinAge: 15 * time.Minute, Now: func() time.Time { return now }}

	var dry logLines
	res, err := Sweep(context.Background(), dc, SweepOptions{MinAge: opts.MinAge, Now: opts.Now, DryRun: true}, dry.f)
	if err != nil {
		t.Fatalf("dry-run sweep: %v", err)
	}
	if len(fd.removedNets) != 0 {
		t.Fatalf("dry run removed %v", fd.removedNets)
	}
	wantRemoved := []string{
		"overcast_ecs_test_1787472886987880100_control",
		"overcast_ecs_test_1787614952211031700",
	}
	wantRetained := []string{
		"overcast_ecs_test_1787614952211031700_control",
		"overcast_rds_master_test_00362be8e11f",
	}
	if !slices.Equal(res.Removed, wantRemoved) || !slices.Equal(res.Retained, wantRetained) {
		t.Fatalf("dry run: removed %v retained %v, want %v / %v", res.Removed, res.Retained, wantRemoved, wantRetained)
	}
	dry.mustContain(t, "would remove overcast_ecs_test_1787614952211031700 (empty, created 48h0m0s ago)")

	var log logLines
	res, err = Sweep(context.Background(), dc, opts, log.f)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !slices.Equal(res.Removed, wantRemoved) {
		t.Errorf("removed = %v, want %v", res.Removed, wantRemoved)
	}
	if !slices.Equal(res.Retained, wantRetained) {
		t.Errorf("retained = %v, want %v", res.Retained, wantRetained)
	}
	if !slices.Equal(fd.removedNets, wantRemoved) {
		t.Errorf("daemon removed %v, want %v", fd.removedNets, wantRemoved)
	}
	for _, shared := range []string{"overcast", "overcast_control", "overcast_efs_test_control"} {
		if _, ok := fd.nets[shared]; !ok {
			t.Errorf("%s was removed; it is not a per-test network", shared)
		}
		log.mustNotContain(t, shared+" ")
		log.mustNotContain(t, shared+"\n")
	}
	log.mustContain(t, "retained overcast_ecs_test_1787614952211031700_control: 1 container(s) attached: overcast-rds-mydb")
	log.mustContain(t, "retained overcast_rds_master_test_00362be8e11f: created 1m0s ago, younger than 15m0s")
}

// ─── fake daemon ─────────────────────────────────────────────────────────────

type fakeContainer struct {
	name    string
	managed bool
}

type fakeNet struct {
	created    time.Time
	containers map[string]fakeContainer
	// refusals is how many more removals of an empty network the daemon answers
	// with "has active endpoints" before it agrees.
	refusals int
}

type fakeDaemon struct {
	mu                sync.Mutex
	nets              map[string]*fakeNet
	removedNets       []string
	removedContainers []string
	disconnected      []string
}

func newFakeDaemon(t *testing.T) (*fakeDaemon, *docker.Client) {
	t.Helper()
	fd := &fakeDaemon{nets: map[string]*fakeNet{}}
	srv := httptest.NewServer(http.HandlerFunc(fd.handle))
	t.Cleanup(srv.Close)
	return fd, docker.NewClient("tcp://"+strings.TrimPrefix(srv.URL, "http://"), zap.NewNop())
}

func (fd *fakeDaemon) handle(w http.ResponseWriter, r *http.Request) {
	fd.mu.Lock()
	defer fd.mu.Unlock()
	const netPrefix, ctrPrefix = "/v1.45/networks/", "/v1.45/containers/"
	p := r.URL.Path
	switch {
	case r.Method == http.MethodGet && p == "/v1.45/networks":
		out := []docker.NetworkSummary{}
		for name, n := range fd.nets {
			out = append(out, docker.NetworkSummary{ID: "id-" + name, Name: name, Created: n.created})
		}
		_ = json.NewEncoder(w).Encode(out)

	case r.Method == http.MethodPost && strings.HasPrefix(p, netPrefix) && strings.HasSuffix(p, "/disconnect"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, netPrefix), "/disconnect")
		var body struct{ Container string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if n, ok := fd.nets[name]; ok {
			delete(n.containers, body.Container)
		}
		fd.disconnected = append(fd.disconnected, body.Container+"@"+name)
		w.WriteHeader(http.StatusOK)

	case r.Method == http.MethodGet && strings.HasPrefix(p, netPrefix):
		name := strings.TrimPrefix(p, netPrefix)
		n, ok := fd.nets[name]
		if !ok {
			http.Error(w, `{"message":"network `+name+` not found"}`, http.StatusNotFound)
			return
		}
		info := docker.NetworkInspect{ID: "id-" + name, Name: name, Created: n.created,
			Containers: map[string]docker.NetworkEndpoint{}}
		for id, c := range n.containers {
			info.Containers[id] = docker.NetworkEndpoint{Name: c.name}
		}
		_ = json.NewEncoder(w).Encode(info)

	case r.Method == http.MethodDelete && strings.HasPrefix(p, netPrefix):
		name := strings.TrimPrefix(p, netPrefix)
		n, ok := fd.nets[name]
		if !ok {
			http.Error(w, `{"message":"network `+name+` not found"}`, http.StatusNotFound)
			return
		}
		if len(n.containers) > 0 || n.refusals > 0 {
			if n.refusals > 0 {
				n.refusals--
			}
			http.Error(w, `{"message":"error while removing network: network `+name+
				` id id-`+name+` has active endpoints"}`, http.StatusForbidden)
			return
		}
		delete(fd.nets, name)
		fd.removedNets = append(fd.removedNets, name)
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodGet && strings.HasPrefix(p, ctrPrefix) && strings.HasSuffix(p, "/json"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, ctrPrefix), "/json")
		for _, n := range fd.nets {
			if c, ok := n.containers[id]; ok {
				labels := map[string]string{}
				if c.managed {
					labels[docker.LabelManaged] = "true"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Id": id, "Name": "/" + c.name,
					"Config": map[string]any{"Labels": labels},
				})
				return
			}
		}
		http.Error(w, `{"message":"No such container: `+id+`"}`, http.StatusNotFound)

	case r.Method == http.MethodDelete && strings.HasPrefix(p, ctrPrefix):
		id := strings.TrimPrefix(p, ctrPrefix)
		for _, n := range fd.nets {
			delete(n.containers, id)
		}
		fd.removedContainers = append(fd.removedContainers, id)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"message":"unexpected `+r.Method+` `+p+`"}`, http.StatusNotImplemented)
	}
}

// ─── log capture ─────────────────────────────────────────────────────────────

type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) f(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) mustContain(t *testing.T, want string) {
	t.Helper()
	if !l.contains(want) {
		t.Errorf("log lacks %q; got:\n  %s", want, strings.Join(l.lines, "\n  "))
	}
}

func (l *logLines) mustNotContain(t *testing.T, unwanted string) {
	t.Helper()
	if l.contains(unwanted) {
		t.Errorf("log mentions %q; got:\n  %s", unwanted, strings.Join(l.lines, "\n  "))
	}
}

func (l *logLines) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line+"\n", s) {
			return true
		}
	}
	return false
}
