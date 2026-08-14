package rds

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/lifecycle"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// handler_lifecycle_test.go — stop/start of a DB instance across the container
// boundary.
//
// RDS containers were created with HostConfig.AutoRemove, which tells Docker to
// delete the container the moment it exits. StopDBInstance stops the container
// deliberately and keeps its ID, so AutoRemove deleted the very container
// StartDBInstance was going to restart: a stopped instance could never be
// started again, and the emulator-only logs endpoint answered a stopped
// instance with the daemon's "No such container" error.

const lifecycleContainerID = "cafecafecafe"

// enginePortsPerHandler is how many host ports a test handler reserves. The
// allocator hands its instances consecutive ports from RDSPortBase, and the
// most any test here stands up against one handler is two — an Aurora cluster's
// writer and reader — so this is that with room to spare. serveEngine says so
// if a test ever outgrows it rather than quietly going back to binding on
// demand.
const enginePortsPerHandler = 8

// The fake engines, and the ports being kept for engines that have not started
// answering yet.
//
// engineListeners keys the fake engines by the address they answer on, so two
// instances sharing a dial target share one listener. engineHeldPorts keys the
// reserved ports the same way, and engineBlocks records which bases those
// reservations were made from.
//
// Package-level because serveEngine is called from several files; every entry
// is removed by the t.Cleanup that created it, and these tests do not run in
// parallel.
var (
	engineListenersMu sync.Mutex
	engineListeners   = map[string]net.Listener{} // addr → the engine answering there
	engineHeldPorts   = map[string]heldPort{}     // addr → a port held for an engine
	engineBlocks      = map[int]int{}             // reserved base → ports reserved from it
)

// heldPort is a port kept for a fake engine that has not started yet: bound, so
// nothing else on the machine can take it, but not listening, so a connection
// to it is refused exactly as if nothing were there. listen promotes that same
// socket when the engine is meant to answer. Implemented per platform in
// held_port_unix_test.go and held_port_other_test.go.
type heldPort interface {
	port() int
	listen() (net.Listener, error)
	close()
}

// lifecycleDaemon is an httptest server speaking enough of the Docker Engine
// API for the RDS container lifecycle, and — crucially — modelling AutoRemove
// the way the real daemon does: a container created with it is gone once it
// stops, and every later request for it is a 404. It also serves the exec
// endpoints a master-password change runs through, including the daemon's
// refusal to exec into a container that is not running.
type lifecycleDaemon struct {
	srv *httptest.Server

	mu           sync.Mutex
	autoRemove   bool // what the create request asked for
	exists       bool // whether the daemon still has the container
	running      bool
	starts       int  // successful start requests
	creates      int  // create-container requests
	failCreate   bool // reject creates, so a rebuild cannot succeed
	failStart    bool // reject starts, as when the daemon is going away
	createLabels map[string]string
	createEnv    []string
	archive      []byte
	requests     []string
	// byName is what a lookup of overcast-rds-<name> answers with, for the
	// tests where another Overcast on this daemon already holds the name.
	// Nil means no such container, which is every other test.
	byName []byte
	// foreign records requests aimed at containers this daemon does not model
	// — another Overcast's, in the instance-scope tests. They are recorded
	// rather than waved through by the default case, because the point of
	// those tests is that the request is never made at all.
	foreign []string

	execCmds     [][]string // Cmd of every exec create, in order
	execEnvs     [][]string // Env of every exec create, in order
	execExitCode int        // exit status reported for every exec
	execOutput   string     // what every exec writes to its stream
}

// execCreateRequest is the subset of the exec-create body these tests read.
type execCreateRequest struct {
	Cmd []string `json:"Cmd"`
	Env []string `json:"Env"`
}

// recordedPasswordExecs returns password-change commands, excluding lifecycle
// readiness probes that are part of instance setup.
func (d *lifecycleDaemon) recordedPasswordExecs() (cmds, envs [][]string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, cmd := range d.execCmds {
		if len(cmd) >= 2 && (cmd[len(cmd)-2] == "-e" || cmd[len(cmd)-2] == "-c") && cmd[len(cmd)-1] == "SELECT 1" {
			continue
		}
		if len(cmd) == 3 && cmd[0] == "sh" && cmd[1] == "-c" && strings.Contains(cmd[2], credentialBootstrapMarker) {
			continue
		}
		cmds = append(cmds, append([]string(nil), cmd...))
		envs = append(envs, append([]string(nil), d.execEnvs[i]...))
	}
	return cmds, envs
}

// failExecs makes every subsequent exec exit non-zero with output — what an
// engine that refuses the statement looks like from here.
func (d *lifecycleDaemon) failExecs(code int, output string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execExitCode = code
	d.execOutput = output
}

// serveEngine answers on whatever address Overcast is going to dial for this
// instance, standing in for a database that has finished booting.
//
// It reads the dial target off the record rather than assuming a port: which
// address that is depends on whether Overcast is running beside the engine or
// on the host, and the test suite itself runs inside a container, so hardcoding
// either one makes the test pass for the wrong reason.
func serveEngine(t *testing.T, h *Handler, id string) net.Listener {
	t.Helper()
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	host, port := dialTarget(inst)
	if host == "" || port == 0 {
		t.Fatalf("instance %s has no dial target — the health check has nothing to dial", id)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	engineListenersMu.Lock()
	defer engineListenersMu.Unlock()
	// lifecycleDaemon models a single container, so instances created against
	// one daemon can share a dial target — an Aurora cluster's members when the
	// suite itself runs in a container, where the target is the container's own
	// address rather than a host port. One engine answering for all of them is
	// what that fake shape means; listening twice on the same address is just a
	// bind error.
	if ln, ok := engineListeners[addr]; ok {
		return ln
	}

	var ln net.Listener
	if held, ok := engineHeldPorts[addr]; ok {
		// This port has been held since the handler was built, for exactly
		// this. Promoting the socket that holds it is what keeps the port from
		// having to be free a second time.
		delete(engineHeldPorts, addr)
		var err error
		if ln, err = held.listen(); err != nil {
			t.Fatalf("fake engine could not answer on the port held for %s (%s): %v", id, addr, err)
		}
	} else {
		if base, count, ok := reservedBlockFor(port); ok {
			t.Fatalf("instance %s was allocated %s, past the %d ports reserved from %d — "+
				"raise enginePortsPerHandler to cover what this test stands up", id, addr, count, base)
		}
		// Not a reserved host port: the suite is running inside a container, so
		// the dial target is the engine container's own address and port.
		var err error
		if ln, err = net.Listen("tcp", addr); err != nil {
			t.Fatalf("fake engine could not listen on the instance's dial target %s: %v", addr, err)
		}
	}
	engineListeners[addr] = ln
	t.Cleanup(func() {
		engineListenersMu.Lock()
		delete(engineListeners, addr)
		engineListenersMu.Unlock()
		_ = ln.Close()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln
}

// foreignRequest names a request by the container it addressed and what it
// asked of it, e.g. "POST d0d0d0d0d0d0/stop".
func foreignRequest(method, path string) string {
	_, target, _ := strings.Cut(path, "/containers/")
	return method + " " + target
}

// foreignRequests returns every request the daemon received for a container it
// does not model.
func (d *lifecycleDaemon) foreignRequests() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.foreign...)
}

// holdsNameForAnotherOvercast makes a lookup of this instance's container name
// answer with a container another Overcast on the same daemon created for a DB
// instance of the same name — labelled RDS, labelled with that identifier, and
// labelled as somebody else's.
func (d *lifecycleDaemon) holdsNameForAnotherOvercast(t *testing.T, id string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"Id":   neighbourContainerID,
		"Name": "/overcast-rds-" + id,
		"Config": map[string]any{"Labels": map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    "rds",
			docker.LabelResourceID: id,
			docker.LabelInstance:   neighbourInstance,
		}},
		"State":           map[string]any{"Status": "running", "Running": true},
		"NetworkSettings": map[string]any{"Networks": map[string]any{}, "Ports": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("encode the neighbouring container: %v", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byName = body
}

// The container another Overcast on the same daemon created for a DB instance
// of the same name, and the sweep identity it stamped on it. Both differ from
// anything this handler ever writes — that is the whole of what makes it
// somebody else's.
const (
	neighbourContainerID = "d0d0d0d0d0d0"
	neighbourInstance    = "another-overcast-instance"
)

// neighbourContainer is that container as it appears in a container listing:
// same resource ID as one of this instance's, since the identifier is the
// user's choice of name and nothing stops two Overcasts from being given the
// same one.
func neighbourContainer(id, state string) docker.ContainerSummary {
	return docker.ContainerSummary{
		ID:    neighbourContainerID,
		Names: []string{"/overcast-rds-" + id},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    "rds",
			docker.LabelResourceID: id,
			docker.LabelInstance:   neighbourInstance,
		},
	}
}

// ownContainer is the summary Docker returns for the container this handler
// created for id, as reconciliation sees it.
func ownContainer(t *testing.T, h *Handler, id, state string) docker.ContainerSummary {
	t.Helper()
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DockerContainerID == "" {
		t.Fatalf("instance %s never recorded a container", id)
	}
	return docker.ContainerSummary{
		ID:    inst.DockerContainerID,
		Names: []string{"/overcast-rds-" + id},
		State: state,
		Labels: map[string]string{
			docker.LabelManaged:    "true",
			docker.LabelService:    "rds",
			docker.LabelResourceID: id,
			docker.LabelInstance:   h.instances.Resolve(context.Background()),
		},
	}
}

// drop makes the daemon forget the container, as a startup sweep, a
// `docker prune`, or a user tidying up by hand would.
func (d *lifecycleDaemon) drop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exists = false
	d.running = false
}

// refuseCreates makes rebuilding the container impossible.
func (d *lifecycleDaemon) refuseCreates() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failCreate = true
}

func (d *lifecycleDaemon) setStartFailure(fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failStart = fail
}

func newLifecycleDaemon(t *testing.T) *lifecycleDaemon {
	t.Helper()
	d := &lifecycleDaemon{}

	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		const cid = lifecycleContainerID

		// Once the daemon has discarded the container, everything addressed at
		// it answers 404 with the body Docker actually sends.
		if strings.Contains(p, "/containers/"+cid) {
			d.mu.Lock()
			gone := !d.exists
			d.mu.Unlock()
			if gone {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"No such container: ` + cid + `"}`))
				return
			}
		}

		switch {
		case strings.HasSuffix(p, "/images/create"), strings.HasSuffix(p, "/images/prune"):
			w.WriteHeader(http.StatusOK)

		case strings.HasSuffix(p, "/containers/json"):
			d.mu.Lock()
			exists := d.exists
			running := d.running
			labels := maps.Clone(d.createLabels)
			d.mu.Unlock()
			if !exists {
				_, _ = w.Write([]byte("[]"))
				return
			}
			state := "exited"
			if running {
				state = "running"
			}
			// The labels the create request actually carried, including the
			// sweep identity: a listing that invented its own would let
			// reconciliation match on a label RDS never stamps.
			_ = json.NewEncoder(w).Encode([]docker.ContainerSummary{{
				ID:     cid,
				State:  state,
				Labels: labels,
			}})

		// Exec create. Docker refuses to exec into a container that is not
		// running, and so does this.
		case strings.HasSuffix(p, "/containers/"+cid+"/exec"):
			var req execCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode exec create request: %v", err)
			}
			d.mu.Lock()
			running := d.running
			if running {
				d.execCmds = append(d.execCmds, req.Cmd)
				d.execEnvs = append(d.execEnvs, req.Env)
			}
			d.mu.Unlock()
			if !running {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"Container ` + cid + ` is not running"}`))
				return
			}
			_, _ = w.Write([]byte(`{"Id":"exec-1"}`))

		case strings.HasSuffix(p, "/exec/exec-1/start"):
			d.mu.Lock()
			out := d.execOutput
			d.mu.Unlock()
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			_, _ = w.Write([]byte(out))

		case strings.HasSuffix(p, "/exec/exec-1/json"):
			d.mu.Lock()
			code := d.execExitCode
			d.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"Running":false,"ExitCode":%d}`, code)

		// GetContainerByName lookup before create. No existing container unless
		// a test put one there — the name is derived from the DB instance
		// identifier, so it is where two Overcasts sharing a daemon collide.
		case strings.Contains(p, "/containers/overcast-rds-") && strings.HasSuffix(p, "/json"):
			d.mu.Lock()
			body := d.byName
			d.mu.Unlock()
			if body == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)

		case strings.HasSuffix(p, "/networks/create"):
			_, _ = w.Write([]byte(`{"Id":"net-1"}`))

		case strings.HasSuffix(p, "/connect"):
			w.WriteHeader(http.StatusOK)

		case strings.HasSuffix(p, "/containers/create"):
			var req docker.CreateContainerRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode create container request: %v", err)
			}
			d.mu.Lock()
			d.creates++
			refuse := d.failCreate
			d.createLabels = maps.Clone(req.Labels)
			d.createEnv = append([]string(nil), req.Env...)
			d.requests = append(d.requests, "create")
			if req.HostConfig != nil {
				d.autoRemove = req.HostConfig.AutoRemove
			}
			if !refuse {
				d.exists = true
			}
			d.mu.Unlock()
			if refuse {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"no space left on device"}`))
				return
			}
			_, _ = w.Write([]byte(`{"Id":"` + cid + `"}`))

		case strings.HasSuffix(p, "/containers/"+cid+"/start"):
			d.mu.Lock()
			d.requests = append(d.requests, "start")
			fail := d.failStart
			if fail {
				d.mu.Unlock()
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if !d.running {
				d.starts++
			}
			d.running = true
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPut && strings.HasSuffix(p, "/containers/"+cid+"/archive"):
			var body bytes.Buffer
			if _, err := body.ReadFrom(r.Body); err != nil {
				t.Errorf("read copied archive: %v", err)
			}
			d.mu.Lock()
			d.archive = append([]byte(nil), body.Bytes()...)
			d.requests = append(d.requests, "archive")
			d.mu.Unlock()
			w.WriteHeader(http.StatusOK)

		case strings.HasSuffix(p, "/containers/"+cid+"/stop"):
			d.mu.Lock()
			d.running = false
			// The behaviour under test: AutoRemove turns a stop into a delete.
			if d.autoRemove {
				d.exists = false
			}
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Container remove (DELETE /containers/{id}).
		case r.Method == http.MethodDelete && strings.HasSuffix(p, "/containers/"+cid):
			d.mu.Lock()
			d.exists = false
			d.running = false
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		// Inspect by ID (setContainerEndpoint when running inside Docker).
		case strings.HasSuffix(p, "/containers/"+cid+"/json"):
			_, _ = w.Write([]byte(`{"Id":"` + cid + `",` +
				`"State":{"Status":"running","Running":true},` +
				`"NetworkSettings":{"Networks":{"overcast_rds":{"IPAddress":"127.0.0.1"}},"Ports":{}}}`))

		// Anything aimed at a container other than the one this daemon models:
		// another Overcast's, in the instance-scope tests. Recorded so a test
		// can say the request was never made. The cid exclusion keeps requests
		// this daemon has no case for — a log read, say — from being reported
		// as somebody else's container.
		case strings.Contains(p, "/containers/") && !strings.Contains(p, "/containers/"+cid):
			d.mu.Lock()
			d.foreign = append(d.foreign, foreignRequest(r.Method, p))
			d.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func TestCreateAuroraMySQL3Container_bootstrapsAWSMasterCredentialsBeforeStart(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	const (
		id       = "mysql-special-password"
		password = `valid\pass'word!`
	)

	createRunningInstanceWith(t, h, id, "aurora-mysql", "admin", password)

	d.mu.Lock()
	env := append([]string(nil), d.createEnv...)
	archiveBytes := append([]byte(nil), d.archive...)
	requests := append([]string(nil), d.requests...)
	d.mu.Unlock()

	if slices.Contains(env, "MYSQL_ROOT_PASSWORD="+password) {
		t.Fatal("requested password was handed to the image entrypoint, whose SQL interpolation changes backslashes")
	}
	for _, value := range env {
		if strings.HasPrefix(value, "MYSQL_PASSWORD=") || strings.HasPrefix(value, "MYSQL_USER=") {
			t.Fatalf("custom user was delegated to the image entrypoint: %q", value)
		}
	}
	if len(requests) < 3 || !slices.Equal(requests[:3], []string{"create", "archive", "start"}) {
		t.Fatalf("container requests = %v, want create, archive, start", requests)
	}

	tr := tar.NewReader(bytes.NewReader(archiveBytes))
	header, err := tr.Next()
	if err != nil {
		t.Fatalf("read credential init archive: %v", err)
	}
	if header.Name != "10-overcast-credentials.sh" {
		t.Fatalf("init archive entry = %q", header.Name)
	}
	if header.Mode != 0o644 {
		t.Fatalf("init archive mode = %#o, want readable but non-executable 0644", header.Mode)
	}
	var script bytes.Buffer
	if _, err := script.ReadFrom(tr); err != nil {
		t.Fatalf("read credential init script: %v", err)
	}
	for _, want := range []string{
		"IDENTIFIED WITH mysql_native_password",
		`BY 'valid\\pass''word!'`,
		`export MYSQL_ROOT_PASSWORD='valid\pass'\''word!'`,
		"CREATE USER IF NOT EXISTS 'admin'@'%'",
		"CREATE ROLE IF NOT EXISTS 'rds_superuser_role'@'%'",
		" ON *.* TO 'rds_superuser_role'@'%' WITH GRANT OPTION",
		"GRANT 'rds_superuser_role'@'%' TO 'admin'@'%'",
		"SET DEFAULT ROLE 'rds_superuser_role'@'%' TO 'admin'@'%'",
		"CREATE ROLE, DROP ROLE, APPLICATION_PASSWORD_ADMIN, ROLE_ADMIN, SET_USER_ID, XA_RECOVER_ADMIN, CONNECTION_ADMIN, SHOW_ROUTINE",
		"DROP USER IF EXISTS 'root'@'%'",
	} {
		if !strings.Contains(script.String(), want) {
			t.Errorf("credential init script does not contain %q:\n%s", want, script.String())
		}
	}
	if strings.Contains(script.String(), "CREATE USER IF NOT EXISTS 'root'@'%'") {
		t.Errorf("credential initializer exposes root@%% as a remote administrator:\n%s", script.String())
	}
	if strings.Contains(script.String(), " ON `test`.* TO 'admin'@'%'") {
		t.Errorf("master privileges are scoped to the bootstrap database:\n%s", script.String())
	}
}

func TestCreateMySQLContainer_withoutDBNameDoesNotCreateTestDatabase(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)

	createRunningInstanceWith(t, h, "mysql-no-db", "mysql", "admin", "password123")

	d.mu.Lock()
	env := append([]string(nil), d.createEnv...)
	d.mu.Unlock()
	for _, value := range env {
		if strings.HasPrefix(value, "MYSQL_DATABASE=") {
			t.Fatalf("container env contains %q, want no application database when DBName is omitted", value)
		}
	}
}

func TestCredentialInitializersGiveEveryRequestedMasterAWSBootstrapPrivileges(t *testing.T) {
	tests := []struct {
		name      string
		instance  DBInstance
		image     string
		want      []string
		forbidden []string
	}{
		{
			name: "MySQL 5.7",
			instance: DBInstance{
				Engine: "mysql", EngineVersion: "5.7", MasterUsername: "admin", MasterUserPassword: "password123",
			},
			image: "mysql:5.7",
			want: []string{
				"CREATE USER IF NOT EXISTS 'admin'@'%' IDENTIFIED BY 'password123'",
				" ON *.* TO 'admin'@'%' WITH GRANT OPTION",
				"DROP USER IF EXISTS 'root'@'%'",
			},
			forbidden: []string{" ON `test`.*", "GRANT ALL ON *.* TO 'root'@'%'"},
		},
		{
			name: "MariaDB",
			instance: DBInstance{
				Engine: "mariadb", EngineVersion: "11.4", MasterUsername: "admin", MasterUserPassword: "password123",
			},
			image: "mariadb:11",
			want: []string{
				"CREATE USER IF NOT EXISTS 'admin'@'%' IDENTIFIED BY 'password123'",
				" ON *.* TO 'admin'@'%' WITH GRANT OPTION",
				"DROP USER IF EXISTS 'root'@'%'",
			},
			forbidden: []string{" ON `test`.*", "GRANT ALL ON *.* TO 'root'@'%'"},
		},
		{
			name: "PostgreSQL",
			instance: DBInstance{
				Engine: "postgres", MasterUsername: "admin", MasterUserPassword: "password123", DBName: "application",
			},
			image: "postgres:16",
			want: []string{
				`CREATE ROLE "admin" WITH LOGIN NOSUPERUSER CREATEDB CREATEROLE`,
				`GRANT "rds_superuser" TO "admin" WITH ADMIN OPTION`,
				`ARRAY['pg_monitor', 'pg_signal_backend', 'pg_checkpoint', 'pg_use_reserved_connections']`,
				`GRANT %I TO rds_superuser WITH ADMIN OPTION`,
				`ALTER DATABASE "postgres" OWNER TO "admin"`,
				`CREATE DATABASE "application" OWNER "admin"`,
			},
			forbidden: []string{`CREATE ROLE "admin" WITH LOGIN SUPERUSER`, `CREATE DATABASE "test"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive, err := credentialArchiveForInstance(&tc.instance, tc.image)
			if err != nil {
				t.Fatalf("credentialArchiveForInstance: %v", err)
			}
			contents := readCredentialArchive(t, archive)
			for _, want := range tc.want {
				if !strings.Contains(contents, want) {
					t.Errorf("credential initializer does not contain %q:\n%s", want, contents)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(contents, forbidden) {
					t.Errorf("credential initializer contains forbidden %q:\n%s", forbidden, contents)
				}
			}
		})
	}
}

func readCredentialArchive(t *testing.T, archive *bytes.Reader) string {
	t.Helper()
	tr := tar.NewReader(archive)
	if _, err := tr.Next(); err != nil {
		t.Fatalf("read credential init archive: %v", err)
	}
	var contents bytes.Buffer
	if _, err := contents.ReadFrom(tr); err != nil {
		t.Fatalf("read credential init contents: %v", err)
	}
	return contents.String()
}

func (d *lifecycleDaemon) state() (autoRemove, exists bool, starts int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.autoRemove, d.exists, d.starts
}

// containerGone reports whether the daemon has discarded the container, polling
// for up to 10s so an async GC removal has a chance to land.
func (d *lifecycleDaemon) containerGone(within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if _, exists, _ := d.state(); !exists {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reserveEnginePorts gives a test handler a port range nothing else is using,
// and keeps it that way until the test is over.
//
// The allocator hands out ports from cfg.RDSPortBase, and a test then has to
// listen on whichever one it picked — so the range has to actually be free.
// Left at the default every test allocated the same port and the second to run
// could not bind; a fixed alternative range only moves that assumption
// somewhere less obvious, since it still asserts nothing else on the machine
// wants those ports. Asking the OS for ports it is willing to give removes the
// assumption instead of relocating it.
//
// Asking is not enough on its own, though, and that is what this used to do:
// probe one port with :0, close it, and bind it again once the instance was
// up. `go test ./...` runs the package binaries in parallel and half of them
// stand up httptest servers on ephemeral ports, so between the probe and the
// bind — seconds, across a container create and start — something else could
// take it, and TestModifyDBCluster_masterPasswordReachesEveryMember failed on
// CI with "address already in use". The second member's port was worse than
// racy: the allocator hands out consecutive ports and only the first had ever
// been established to be free at all.
//
// So the base and the enginePortsPerHandler ports above it — every port a test
// handler's instances actually reach — are held from here until serveEngine
// takes one over or the test ends. A held port is bound but not listening,
// which is both halves of what the tests need: nothing else can bind it, and
// connecting to it is refused until an engine is meant to answer.
func reserveEnginePorts(t *testing.T) int {
	t.Helper()
	if !heldPortsSupported {
		return probeFreePort(t)
	}

	base, held, err := holdEnginePortBlock(enginePortsPerHandler)
	if err != nil {
		t.Fatalf("could not reserve %d consecutive ports for the test's RDS port base: %v",
			enginePortsPerHandler, err)
	}

	engineListenersMu.Lock()
	engineBlocks[base] = enginePortsPerHandler
	for port, hp := range held {
		engineHeldPorts[engineAddr(port)] = hp
	}
	engineListenersMu.Unlock()

	t.Cleanup(func() {
		engineListenersMu.Lock()
		defer engineListenersMu.Unlock()
		delete(engineBlocks, base)
		for port := base; port < base+enginePortsPerHandler; port++ {
			addr := engineAddr(port)
			if hp, ok := engineHeldPorts[addr]; ok {
				hp.close()
				delete(engineHeldPorts, addr)
			}
		}
	})
	return base
}

// portBlockAttempts bounds the search for a run of free ports. Each attempt is
// a handful of syscalls, and only a machine with almost no ephemeral ports left
// needs more than the first.
const portBlockAttempts = 100

// holdEnginePortBlock holds count consecutive ports, starting wherever the OS
// is willing to put the first one. A neighbour that is already taken means this
// run is not available: the block is dropped and another asked for.
func holdEnginePortBlock(count int) (int, map[int]heldPort, error) {
	var lastErr error
	for attempt := 0; attempt < portBlockAttempts; attempt++ {
		first, err := holdPort(0)
		if err != nil {
			return 0, nil, err
		}
		base := first.port()

		held := map[int]heldPort{base: first}
		for port := base + 1; port < base+count; port++ {
			hp, err := holdPort(port)
			if err != nil {
				lastErr = err
				break
			}
			held[port] = hp
		}
		if len(held) == count {
			return base, held, nil
		}
		for _, hp := range held {
			hp.close()
		}
	}
	return 0, nil, fmt.Errorf("no run of %d free ports in %d attempts; last refusal: %w",
		count, portBlockAttempts, lastErr)
}

// reservedBlockFor reports the reservation an allocated port came from, if any.
// A base can produce any port in [base, base+portAllocationSpan), so one in
// that range past the reserved run is a test that has stood up more instances
// than its handler reserved ports for. Callers hold engineListenersMu.
func reservedBlockFor(port int) (base, count int, ok bool) {
	for b, c := range engineBlocks {
		if port >= b && port < b+portAllocationSpan {
			return b, c, true
		}
	}
	return 0, 0, false
}

func engineAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// probeFreePort asks the OS for a port and hands it back released. It is the
// fallback for platforms that cannot hold one (held_port_other_test.go), and
// carries the window reserveEnginePorts exists to close.
func probeFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find a free port for the test's RDS port base: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("closing the probe listener: %v", err)
	}
	return port
}

func newLifecycleHandler(t *testing.T, d *lifecycleDaemon) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012", RDSPortBase: reserveEnginePorts(t)}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	h := s.handler
	h.docker = docker.NewClient("tcp://"+d.srv.Listener.Addr().String(), zap.NewNop())
	h.puller = docker.NewImagePuller(h.docker)
	h.dockerReady.Store(true)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.scheduler.Stop(ctx)
		h.dockerWg.Wait()
	})
	return h
}

// The wiring the docker package's sweep tests cannot see. Those prove that a
// sweep scoped to an identity spares another instance's containers; this proves
// RDS actually stamps that identity when it creates one. Without the stamp the
// scoping still holds, but in the other direction: the GC would recognise none
// of its own containers and clean up nothing it ever created.
func TestCreateDBInstance_stampsTheSweepIdentityOnItsContainer(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)

	createRunningInstance(t, h, "lifecycle-identity")

	d.mu.Lock()
	labels := maps.Clone(d.createLabels)
	d.mu.Unlock()

	want := h.instances.Resolve(context.Background())
	if want == "" {
		t.Fatal("the handler resolved no sweep identity, so its containers can never be swept")
	}
	if got := labels[docker.LabelInstance]; got != want {
		t.Errorf("container labelled %s=%q, want %q — the GC sweeps on this value",
			docker.LabelInstance, got, want)
	}
}

func useMockLifecycleClock(t *testing.T, h *Handler) *clock.Mock {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.scheduler.Stop(ctx)
	mock := clock.NewMock()
	h.scheduler = lifecycle.NewScheduler(mock)
	h.clk = mock
	return mock
}

// createRunningInstance creates a DB instance and waits for its container start
// to finish, so the test acts on a settled record.
func createRunningInstance(t *testing.T, h *Handler, id string) {
	t.Helper()
	createRunningInstanceWith(t, h, id, "mysql", "admin", "password123")
}

// createRunningInstanceWith is createRunningInstance for a test that cares
// which engine and credentials the instance was built with.
func createRunningInstanceWith(t *testing.T, h *Handler, id, engine, user, password string) {
	t.Helper()
	if _, aerr := h.createDBInstanceTyped(context.Background(), &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               engine,
		MasterUsername:       user,
		MasterUserPassword:   password,
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		inst, aerr := h.store.getDBInstance(context.Background(), id)
		if aerr == nil && inst.DockerContainerID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("container ID was never recorded on the instance")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.dockerWg.Wait()
	// A create no longer declares itself available; the health check does,
	// once the engine answers. Standing one up and waiting exercises the real
	// promotion path rather than writing the status in behind its back.
	serveEngine(t, h, id)
	waitForStatus(t, h, id, "available")
}

// A stopped instance must still have a container to start again. AWS's
// stop/start cycle preserves the instance; AutoRemove made the stop
// destructive, so StartDBInstance had nothing to restart and the instance was
// stranded in "stopped" for good.
func TestStopStartDBInstance_containerSurvivesTheStop(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "lifecycle-mysql"

	// Given: a running instance whose container was not created with AutoRemove.
	createRunningInstance(t, h, id)
	if autoRemove, _, _ := d.state(); autoRemove {
		t.Error("RDS container created with HostConfig.AutoRemove — Docker deletes it on stop, " +
			"so StopDBInstance destroys the container StartDBInstance needs")
	}

	// When: the instance is stopped and started again.
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	if _, exists, _ := d.state(); !exists {
		t.Fatal("the stop removed the container — nothing is left for StartDBInstance to start")
	}
	waitForStatus(t, h, id, "stopped")
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	// StartDBInstance answers before the container is up — AWS returns
	// "starting" and settles asynchronously — so wait for that work to land.
	h.dockerWg.Wait()

	// Then: the daemon really did restart the same container.
	_, exists, starts := d.state()
	if !exists {
		t.Fatal("container no longer exists after the stop/start cycle")
	}
	if starts < 2 {
		t.Errorf("container was started %d time(s); want a second start from StartDBInstance", starts)
	}
}

// waitForStatus polls until the instance reaches want, and reports what it saw
// instead if it does not.
func waitForStatus(t *testing.T, h *Handler, id, want string) *DBInstance {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last string
	for {
		inst, aerr := h.store.getDBInstance(context.Background(), id)
		if aerr == nil {
			if inst.DBInstanceStatus == want {
				return inst
			}
			last = inst.DBInstanceStatus
		}
		if time.Now().After(deadline) {
			t.Fatalf("instance %s never reached %q; last status %q", id, want, last)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Containers go missing — a startup sweep, a `docker prune`, a user tidying up.
// The instance record is what says the database should exist, so a start
// rebuilds it rather than reporting a start that started nothing.
func TestStartDBInstance_rebuildsAContainerDockerNoLongerHas(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "lifecycle-rebuild"

	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")

	// Given: the container is gone behind Overcast's back.
	d.drop()
	createsBefore := func() int { _, _, _ = d.state(); d.mu.Lock(); defer d.mu.Unlock(); return d.creates }()

	// When: the instance is started.
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.dockerWg.Wait()

	// Then: a container was built to replace it, and the instance is not left
	// claiming a container that does not exist.
	d.mu.Lock()
	createsAfter := d.creates
	exists := d.exists
	d.mu.Unlock()
	if createsAfter <= createsBefore {
		t.Error("no container was created to replace the missing one")
	}
	if !exists {
		t.Error("instance still has no container after being started")
	}
	inst, _ := h.store.getDBInstance(ctx, id)
	if inst.DBInstanceStatus == "failed" {
		t.Errorf("instance failed despite the container being rebuildable: %s", inst.StatusReason)
	}
}

// Stopping the engine container through Docker is an out-of-band runtime
// mutation, not an RDS StopDBInstance request. The persisted RDS control-plane
// state remains authoritative, so Overcast must immediately put the instance
// back on its startup path instead of adopting Docker's stopped state.
func TestHandleContainerEvent_recoversDockerLevelStop(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-level-stop"

	// Given: an available instance whose engine is deliberately stopped through
	// Docker Desktop rather than through StopDBInstance.
	createRunningInstance(t, h, id)
	d.mu.Lock()
	d.running = false
	startsBefore := d.starts
	d.mu.Unlock()

	// When: Docker reports that the container process has died.
	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})
	h.dockerWg.Wait()

	// Then: Overcast restarts the engine and health-checks it as a starting
	// instance. Only StopDBInstance is allowed to establish a durable stop.
	d.mu.Lock()
	startsAfter := d.starts
	running := d.running
	d.mu.Unlock()
	if startsAfter <= startsBefore {
		t.Error("Docker-level stop did not restart the engine container")
	}
	if !running {
		t.Error("engine container remains stopped after Docker-level recovery")
	}
	if got := instanceStatus(t, h, ctx, id); got != "starting" {
		t.Errorf("status = %q, want %q while the recovered engine is health-checked", got, "starting")
	}
}

func TestHandleContainerEvent_coalescesDuplicateRecoveryEvents(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-duplicate-die"

	createRunningInstance(t, h, id)
	mock := useMockLifecycleClock(t, h)

	d.mu.Lock()
	d.running = false
	startsBefore := d.starts
	d.mu.Unlock()
	event := events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	}

	h.handleContainerEvent(ctx, event)
	h.handleContainerEvent(ctx, event)

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DockerRecoveryAttempts != 1 {
		t.Fatalf("recovery attempts = %d, want one coalesced attempt", inst.DockerRecoveryAttempts)
	}
	if got := h.scheduler.PendingCount(); got != 1 {
		t.Fatalf("pending recovery callbacks = %d, want 1", got)
	}

	h.scheduler.AdvanceAndSettle(mock, 0)
	h.dockerWg.Wait()
	d.mu.Lock()
	startsAfter := d.starts
	d.mu.Unlock()
	if startsAfter != startsBefore+1 {
		t.Fatalf("container starts = %d after duplicate events, want %d", startsAfter, startsBefore+1)
	}
}

func TestHandleContainerEvent_stopsRestartingAfterRecoveryBudget(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-crash-loop"

	createRunningInstance(t, h, id)
	mock := useMockLifecycleClock(t, h)
	event := events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	}

	for attempt := 1; attempt <= maxDockerRecoveryAttempts; attempt++ {
		d.mu.Lock()
		d.running = false
		d.mu.Unlock()
		h.handleContainerEvent(ctx, event)

		inst, aerr := h.store.getDBInstance(ctx, id)
		if aerr != nil {
			t.Fatalf("attempt %d getDBInstance: %s", attempt, aerr.Message)
		}
		if inst.DockerRecoveryAttempts != attempt {
			t.Fatalf("attempt counter = %d, want %d", inst.DockerRecoveryAttempts, attempt)
		}

		h.scheduler.AdvanceAndSettle(mock, dockerRecoveryBackoff(attempt))
		h.dockerWg.Wait()
		h.scheduler.AdvanceAndSettle(mock, time.Second)
		if got := instanceStatus(t, h, ctx, id); got != "available" {
			t.Fatalf("attempt %d status = %q, want available before the next crash", attempt, got)
		}
	}

	d.mu.Lock()
	d.running = false
	startsBeforeExhaustion := d.starts
	d.mu.Unlock()
	h.handleContainerEvent(ctx, event)

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance after exhaustion: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "failed" {
		t.Fatalf("status after recovery budget = %q, want failed", inst.DBInstanceStatus)
	}
	if inst.DockerRecoveryPending {
		t.Error("exhausted recovery remains pending and can be retried on Docker reconnect")
	}
	if !strings.Contains(inst.StatusReason, "repeatedly exited") {
		t.Errorf("StatusReason = %q, want repeated-exit explanation", inst.StatusReason)
	}
	if got := h.scheduler.PendingCount(); got != 0 {
		t.Errorf("pending callbacks after exhaustion = %d, want 0", got)
	}
	d.mu.Lock()
	startsAfterExhaustion := d.starts
	d.mu.Unlock()
	if startsAfterExhaustion != startsBeforeExhaustion {
		t.Errorf("container restarted after exhaustion: starts %d -> %d", startsBeforeExhaustion, startsAfterExhaustion)
	}
}

func TestDockerRecoveryBackoff(t *testing.T) {
	wants := []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	for i, want := range wants {
		attempt := i + 1
		if got := dockerRecoveryBackoff(attempt); got != want {
			t.Errorf("attempt %d backoff = %s, want %s", attempt, got, want)
		}
	}
	if got := dockerRecoveryBackoff(100); got != dockerRecoveryMaxBackoff {
		t.Errorf("large-attempt backoff = %s, want cap %s", got, dockerRecoveryMaxBackoff)
	}
}

func TestDockerRecoveryBudget_resetsOnlyAfterStableAvailability(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-stable-recovery"

	createRunningInstance(t, h, id)
	mock := useMockLifecycleClock(t, h)
	d.mu.Lock()
	d.running = false
	d.mu.Unlock()
	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})
	h.scheduler.AdvanceAndSettle(mock, 0)
	h.dockerWg.Wait()
	h.scheduler.AdvanceAndSettle(mock, time.Second)

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance after recovery: %s", aerr.Message)
	}
	if inst.DockerRecoveryAttempts != 1 {
		t.Fatalf("attempts immediately after availability = %d, want 1", inst.DockerRecoveryAttempts)
	}

	h.scheduler.AdvanceAndSettle(mock, dockerRecoveryStableFor)
	inst, aerr = h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance after stability period: %s", aerr.Message)
	}
	if inst.DockerRecoveryAttempts != 0 {
		t.Errorf("attempts after %s stable = %d, want reset", dockerRecoveryStableFor, inst.DockerRecoveryAttempts)
	}
}

func TestDockerRecoveryBudget_recognizesPersistedStabilityAfterRestart(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-persisted-stability"

	createRunningInstance(t, h, id)
	mock := useMockLifecycleClock(t, h)
	if _, aerr := h.mutateInstance(ctx, id, func(inst *DBInstance) *protocol.AWSError {
		inst.DockerRecoveryAttempts = maxDockerRecoveryAttempts
		inst.DockerRecoveryAvailableAt = mock.Now().Add(-dockerRecoveryStableFor)
		return nil
	}); aerr != nil {
		t.Fatalf("seed persisted recovery state: %s", aerr.Message)
	}
	d.mu.Lock()
	d.running = false
	d.mu.Unlock()

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})

	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "starting" {
		t.Fatalf("status = %q, want a fresh recovery instead of failed", inst.DBInstanceStatus)
	}
	if inst.DockerRecoveryAttempts != 1 {
		t.Errorf("attempts after persisted stability = %d, want fresh attempt 1", inst.DockerRecoveryAttempts)
	}
}

func TestReconcileContainers_reconcilesMissedStop(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconnect-stop"

	// Given: Docker went away before Overcast received the container's die
	// event, leaving the control-plane record available while the retained
	// container is actually exited.
	createRunningInstance(t, h, id)
	d.mu.Lock()
	d.running = false
	startsBefore := d.starts
	d.mu.Unlock()

	// When: central Docker reconciliation supplies the daemon snapshot after
	// startup or watcher reconnect.
	containers, err := h.docker.ListContainers(ctx, serviceName)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	h.reconcileContainers(ctx, containers)
	h.dockerWg.Wait()

	// Then: RDS lists the daemon's current state and restarts the missed exit.
	d.mu.Lock()
	startsAfter := d.starts
	running := d.running
	d.mu.Unlock()
	if startsAfter <= startsBefore {
		t.Error("Docker reconnect did not reconcile and restart the exited container")
	}
	if !running {
		t.Error("engine remains stopped after Docker reconnect reconciliation")
	}
}

func TestReconcileContainers_retriesRecoveryInterruptedByDaemonShutdown(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconnect-interrupted-recovery"

	// Given: Docker emitted the engine's die event, then became unavailable
	// before Overcast could restart it.
	createRunningInstance(t, h, id)
	d.mu.Lock()
	d.running = false
	d.mu.Unlock()
	d.setStartFailure(true)
	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})
	h.dockerWg.Wait()
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "failed" || !inst.DockerRecoveryPending {
		t.Fatalf("interrupted recovery = status %q, pending %v; want failed, true",
			inst.DBInstanceStatus, inst.DockerRecoveryPending)
	}

	// When: Docker returns and central reconciliation supplies a fresh snapshot.
	d.setStartFailure(false)
	containers, err := h.docker.ListContainers(ctx, serviceName)
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	h.reconcileContainers(ctx, containers)
	// The second automatic attempt is deliberately delayed by the recovery
	// backoff. Wait for that keyed callback to launch the async Docker start
	// before waiting on the Docker work itself.
	h.scheduler.Settle()
	h.dockerWg.Wait()

	// Then: only that infrastructure-recovery failure is retried.
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if !running {
		t.Error("daemon reconnect did not retry the interrupted engine recovery")
	}
	inst, aerr = h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance after reconnect: %s", aerr.Message)
	}
	if inst.DBInstanceStatus != "starting" || inst.DockerRecoveryPending {
		t.Errorf("recovered instance = status %q, pending %v; want starting, false",
			inst.DBInstanceStatus, inst.DockerRecoveryPending)
	}
}

func TestHandleContainerStarted_preservesExplicitStop(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "docker-start-after-api-stop"

	// Given: the instance was explicitly stopped through RDS, then its engine
	// was started directly through Docker Desktop.
	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")
	d.mu.Lock()
	d.running = true
	d.mu.Unlock()

	// When: Docker reports the out-of-band start.
	h.handleContainerStarted(ctx, events.Event{
		Type: events.DockerContainerStarted,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "start",
			Service:     "rds",
			ResourceID:  id,
		},
	})

	// Then: Overcast stops the engine again and preserves StopDBInstance.
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if running {
		t.Error("Docker start overrode an explicit StopDBInstance")
	}
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Errorf("status = %q, want %q", got, "stopped")
	}
}

func TestHandleContainerEvent_ignoresOvercastShutdown(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "overcast-shutdown"

	// Given: Overcast is shutting down and its cleanup stopped the engine.
	createRunningInstance(t, h, id)
	d.mu.Lock()
	d.running = false
	startsBefore := d.starts
	d.mu.Unlock()
	h.shuttingDown.Store(true)

	// When: Docker emits the resulting die event.
	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: lifecycleContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
		},
	})

	// Then: shutdown cleanup is not fought by a new recovery goroutine. The
	// persisted desired state remains available for startup reconciliation.
	d.mu.Lock()
	startsAfter := d.starts
	d.mu.Unlock()
	if startsAfter != startsBefore {
		t.Errorf("shutdown event restarted the container: starts %d -> %d", startsBefore, startsAfter)
	}
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Errorf("status = %q, want %q for next-start reconciliation", got, "available")
	}
}

// A persisted instance that was live when Overcast stopped is still meant to
// be live when Overcast starts again. If its container disappeared during the
// restart, startup reconciliation must rebuild it instead of silently turning
// the instance into an explicitly stopped one.
func TestReconcileContainers_rebuildsMissingAvailableInstance(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconcile-missing"

	// Given: an available instance whose container disappeared while Overcast
	// was not running.
	createRunningInstance(t, h, id)
	d.drop()
	d.mu.Lock()
	createsBefore := d.creates
	d.mu.Unlock()

	// When: startup reconciliation sees no managed container for the instance.
	h.reconcileContainers(ctx, nil)
	h.dockerWg.Wait()

	// Then: it rebuilds the missing container and keeps the instance on its
	// path back to available rather than declaring that the user stopped it.
	d.mu.Lock()
	createsAfter := d.creates
	exists := d.exists
	d.mu.Unlock()
	if createsAfter <= createsBefore {
		t.Error("startup reconciliation did not rebuild the missing container")
	}
	if !exists {
		t.Error("instance still has no container after startup reconciliation")
	}
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceStatus == "stopped" {
		t.Error("startup reconciliation marked a previously available instance as explicitly stopped")
	}
}

// A Docker-driven stop is not the same as StopDBInstance. The former records
// runtime drift; the latter records user intent. Reconciliation must recover
// the former even when the old process persisted "stopped" before exiting.
func TestReconcileContainers_rebuildsMissingExternallyStoppedInstance(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconcile-external-stop"

	// Given: a previously available instance was marked stopped because its
	// container went away, not because StopDBInstance was called.
	createRunningInstance(t, h, id)
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	inst.DBInstanceStatus = "stopped"
	inst.StoppedByUser = false
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}
	d.drop()
	d.mu.Lock()
	createsBefore := d.creates
	d.mu.Unlock()

	// When: startup reconciliation sees the stopped record and missing runtime.
	h.reconcileContainers(ctx, nil)
	h.dockerWg.Wait()

	// Then: it rebuilds the runtime because no user requested the stop.
	d.mu.Lock()
	createsAfter := d.creates
	exists := d.exists
	d.mu.Unlock()
	if createsAfter <= createsBefore {
		t.Error("startup reconciliation did not rebuild the externally stopped instance")
	}
	if !exists {
		t.Error("externally stopped instance still has no container after reconciliation")
	}
}

func TestReconcileContainers_restartsExitedContainerForAvailableInstance(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconcile-exited"

	// Given: the DB instance record remained available across an Overcast
	// restart, but Docker reports that its retained container is exited.
	createRunningInstance(t, h, id)
	d.mu.Lock()
	d.running = false
	startsBefore := d.starts
	d.mu.Unlock()

	// When: startup reconciliation observes the stopped runtime.
	h.reconcileContainers(ctx, []docker.ContainerSummary{{
		ID:    lifecycleContainerID,
		State: "exited",
		Labels: map[string]string{
			docker.LabelService:    "rds",
			docker.LabelResourceID: id,
		},
	}})
	h.dockerWg.Wait()

	// Then: the existing container is restarted, not replaced or translated
	// into an explicit StopDBInstance state.
	d.mu.Lock()
	startsAfter := d.starts
	exists := d.exists
	d.mu.Unlock()
	if startsAfter <= startsBefore {
		t.Error("startup reconciliation did not restart the exited container")
	}
	if !exists {
		t.Error("reconciliation lost the retained container")
	}
	if got := instanceStatus(t, h, ctx, id); got == "stopped" {
		t.Error("reconciliation marked the available instance as explicitly stopped")
	}
}

func TestReconcileContainers_leavesExplicitlyStoppedInstanceStopped(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconcile-user-stop"

	// Given: the user explicitly stopped the instance and its container later
	// disappeared while Overcast was not running.
	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")
	d.drop()
	d.mu.Lock()
	createsBefore := d.creates
	d.mu.Unlock()

	// When: startup reconciliation runs without the container.
	h.reconcileContainers(ctx, nil)
	h.dockerWg.Wait()

	// Then: user intent wins; no engine is recreated or started.
	d.mu.Lock()
	createsAfter := d.creates
	d.mu.Unlock()
	if createsAfter != createsBefore {
		t.Errorf("reconciliation rebuilt an explicitly stopped instance: creates %d -> %d", createsBefore, createsAfter)
	}
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Errorf("status = %q, want %q", got, "stopped")
	}
}

func TestReconcileContainers_stopsDockerStartedExplicitlyStoppedInstance(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "reconcile-user-started-container"

	// Given: StopDBInstance established a durable stopped state, but someone
	// subsequently started its engine directly through Docker Desktop.
	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")
	d.mu.Lock()
	d.running = true
	d.mu.Unlock()

	// When: reconciliation observes a running container for that instance.
	h.reconcileContainers(ctx, []docker.ContainerSummary{{
		ID:    lifecycleContainerID,
		State: "running",
		Labels: map[string]string{
			docker.LabelService:    "rds",
			docker.LabelResourceID: id,
		},
	}})

	// Then: the RDS control-plane stop wins and the engine is stopped again.
	d.mu.Lock()
	running := d.running
	d.mu.Unlock()
	if running {
		t.Error("reconciliation left a Docker-started container running for an explicitly stopped instance")
	}
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Errorf("status = %q, want %q", got, "stopped")
	}
}

// ── Two Overcasts, one Docker daemon ─────────────────────────────────────────
//
// A DB instance identifier is the user's choice of name, and nothing stops two
// Overcasts from being given the same one. Both containers then carry
// overcast.resource-id=mydb, so every match made on that label alone reaches
// across the boundary: this instance's record finds the other's container.
// What it does with it is the damage — an RDS container holds the database in
// its writable layer, so stopping it takes an unrelated database offline and
// hands it to the next sweep to delete.

// The destructive shape: this instance's record says the user stopped its
// database, the neighbour's is running, and reconciliation stopped it on the
// strength of a StopDBInstance call that was never made against it.
func TestReconcileContainers_leavesAnotherOvercastsDatabaseRunning(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "shared-name-stopped"

	// Given: this Overcast's instance of that name was explicitly stopped.
	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")

	// When: reconciliation lists both containers of that name — this one's,
	// stopped as the record says, and the neighbouring Overcast's, running.
	h.reconcileContainers(ctx, []docker.ContainerSummary{
		ownContainer(t, h, id, "exited"),
		neighbourContainer(id, "running"),
	})
	h.scheduler.Settle()
	h.dockerWg.Wait()

	// Then: nothing was asked of the neighbour's container.
	if got := d.foreignRequests(); len(got) > 0 {
		t.Errorf("reconciliation acted on another Overcast's container: %v", got)
	}
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Errorf("status = %q, want %q", got, "stopped")
	}
}

// The same collision from the other side: this instance's own container is the
// one that needs restarting, and it must be found among the containers of that
// name rather than shadowed by the neighbour's.
func TestReconcileContainers_restartsItsOwnContainerNotTheNeighbourOfTheSameName(t *testing.T) {
	for _, tc := range []struct {
		name      string
		neighbour func(id string) docker.ContainerSummary
	}{
		{"labelled as another Overcast's", func(id string) docker.ContainerSummary {
			return neighbourContainer(id, "running")
		}},
		{"created before the instance label existed", func(id string) docker.ContainerSummary {
			// Unlabelled is not permission either. The record names the
			// container this instance created, and it does not name this one.
			c := neighbourContainer(id, "running")
			delete(c.Labels, docker.LabelInstance)
			return c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newLifecycleDaemon(t)
			h := newLifecycleHandler(t, d)
			ctx := context.Background()
			const id = "shared-name-available"

			// Given: this instance is meant to be live, and its own container
			// exited while Overcast was away.
			createRunningInstance(t, h, id)
			d.mu.Lock()
			d.running = false
			startsBefore := d.starts
			d.mu.Unlock()

			// When: reconciliation lists the neighbour's running container of
			// the same name alongside it.
			h.reconcileContainers(ctx, []docker.ContainerSummary{
				ownContainer(t, h, id, "exited"),
				tc.neighbour(id),
			})
			h.scheduler.Settle()
			h.dockerWg.Wait()

			// Then: this instance's own engine is the one brought back, and
			// the neighbour's is left to the Overcast that owns it.
			d.mu.Lock()
			startsAfter := d.starts
			d.mu.Unlock()
			if startsAfter <= startsBefore {
				t.Error("reconciliation read the neighbour's container as this instance's runtime and left its own engine down")
			}
			if got := d.foreignRequests(); len(got) > 0 {
				t.Errorf("reconciliation acted on another Overcast's container: %v", got)
			}
		})
	}
}

// The event stream carries the same resource ID and the same ambiguity, and
// this branch stops the container the event names. Docker reporting that the
// neighbour started its database is not a reason to stop it.
func TestHandleContainerStarted_leavesAnotherOvercastsContainerRunning(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "shared-name-started"

	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")

	h.handleContainerStarted(ctx, events.Event{
		Type: events.DockerContainerStarted,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "start",
			Service:     "rds",
			ResourceID:  id,
			Instance:    neighbourInstance,
		},
	})

	if got := d.foreignRequests(); len(got) > 0 {
		t.Errorf("a start event for another Overcast's container was acted on: %v", got)
	}
	if got := instanceStatus(t, h, ctx, id); got != "stopped" {
		t.Errorf("status = %q, want %q", got, "stopped")
	}
}

// A die event for the neighbour's container is not this instance's engine
// crashing: recovering from it restarts a container that never stopped and
// spends this instance's recovery budget doing it.
func TestHandleContainerEvent_ignoresAnotherOvercastsContainerDying(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "shared-name-died"

	createRunningInstance(t, h, id)
	d.mu.Lock()
	startsBefore := d.starts
	d.mu.Unlock()

	h.handleContainerEvent(ctx, events.Event{
		Type: events.DockerContainerDied,
		Payload: events.DockerContainerPayload{
			ContainerID: neighbourContainerID,
			Action:      "die",
			Service:     "rds",
			ResourceID:  id,
			Instance:    neighbourInstance,
		},
	})
	h.scheduler.Settle()
	h.dockerWg.Wait()

	d.mu.Lock()
	startsAfter := d.starts
	d.mu.Unlock()
	if startsAfter != startsBefore {
		t.Errorf("another Overcast's die event restarted this instance's engine: starts %d -> %d", startsBefore, startsAfter)
	}
	if got := instanceStatus(t, h, ctx, id); got != "available" {
		t.Errorf("status = %q, want %q — the instance's own engine never stopped", got, "available")
	}
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DockerRecoveryAttempts != 0 {
		t.Errorf("recovery attempts = %d, want 0 — the budget was spent on somebody else's crash", inst.DockerRecoveryAttempts)
	}
}

// Container names are unique per daemon and derived from the identifier, so a
// rebuild finds the neighbour's container sitting on the name it wants. Reusing
// it hands both Overcasts the same database, and this one goes on to stop and
// delete it; the collision has to be reported instead.
func TestStartDBInstance_refusesToReuseAnotherOvercastsContainerOfTheSameName(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "shared-name-rebuild"

	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")

	// Given: this instance's container is gone, and the neighbouring Overcast
	// holds the name a rebuild would use.
	d.drop()
	d.holdsNameForAnotherOvercast(t, id)

	// When: the instance is started.
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// Then: the start fails with the collision named, and the record does not
	// walk away claiming a database it does not own.
	inst := waitForStatus(t, h, id, "failed")
	if !strings.Contains(inst.StatusReason, "another Overcast") {
		t.Errorf("StatusReason = %q, want it to name the Overcast that holds the container", inst.StatusReason)
	}
	if inst.DockerContainerID == neighbourContainerID {
		t.Error("the instance adopted another Overcast's container")
	}
	if got := d.foreignRequests(); len(got) > 0 {
		t.Errorf("another Overcast's container was acted on: %v", got)
	}
}

// When the container genuinely cannot be started, the instance must say so.
// Reporting "available" for a database with nothing behind it is the specific
// dishonesty this replaces: the only way to discover it was to try to connect.
func TestStartDBInstance_reportsFailedWhenTheContainerCannotBeStarted(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)
	ctx := context.Background()
	const id = "lifecycle-unstartable"

	createRunningInstance(t, h, id)
	if _, aerr := h.stopDBInstanceTyped(ctx, &stopDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StopDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	waitForStatus(t, h, id, "stopped")

	// Given: the container is gone and cannot be rebuilt.
	d.drop()
	d.refuseCreates()

	// When: the instance is started.
	if _, aerr := h.startDBInstanceTyped(ctx, &startDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("StartDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	// Then: it reports AWS's "failed", with a reason worth reading — not
	// "available", and not stuck in "starting" forever either.
	inst := waitForStatus(t, h, id, "failed")
	if inst.StatusReason == "" {
		t.Error("instance failed with no reason recorded")
	}
	if !strings.Contains(inst.StatusReason, "could not be started") {
		t.Errorf("StatusReason = %q, want it to say the container could not be started", inst.StatusReason)
	}
}

// Dropping AutoRemove puts container removal entirely on the delete path, so
// prove the delete path still removes it — otherwise the fix trades a stranded
// instance for a leaked container.
func TestDeleteDBInstance_removesItsContainer(t *testing.T) {
	d := newLifecycleDaemon(t)
	h := newLifecycleHandler(t, d)

	gcCtx, cancelGC := context.WithCancel(context.Background())
	t.Cleanup(cancelGC)
	h.gc = docker.NewGC(h.docker, zap.NewNop(), false, h.instances.Resolve)
	h.gc.StartRemoveLoop(gcCtx)

	const id = "lifecycle-delete"
	createRunningInstance(t, h, id)

	if _, aerr := h.deleteDBInstanceTyped(context.Background(), &deleteDBInstanceReq{DBInstanceIdentifier: id}); aerr != nil {
		t.Fatalf("DeleteDBInstance: %s: %s", aerr.Code, aerr.Message)
	}

	if !d.containerGone(10 * time.Second) {
		t.Fatal("DeleteDBInstance left the container behind")
	}
}
