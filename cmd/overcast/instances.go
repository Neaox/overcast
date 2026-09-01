package main

// instances.go — the instance registry `overcast start`/`stop`/`restart`
// (cmd_start.go, cmd_stop.go) use to track background daemons this CLI has
// spawned. Each named instance gets a directory under
// ~/.overcast/instances/<name>/ holding instance.json (this file's
// instanceRecord, marshalled) and, for the native backend, daemon.log — the
// spawned process's redirected stdout+stderr.
//
// Concurrent CLI invocations racing on the same instance name are an
// accepted v1 limitation: there is no file locking here. Two `overcast start
// default` running at once could both decide the name is free and spawn two
// daemons; that is no worse than running two `overcast serve` by hand, and
// locking adds real complexity for a race nobody hits running this from a
// terminal or one CI job at a time.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

// instancesBaseDir returns the directory holding every instance's registry
// entry. A var (not a const), swappable in tests so they can redirect it
// into a t.TempDir() without touching the real ~/.overcast on the machine
// running the suite — the same seam envEnviron (cmd_env.go) uses for the
// calling shell's environment.
var instancesBaseDir = defaultInstancesBaseDir

func defaultInstancesBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".overcast", "instances"), nil
}

// instanceRecord is the persisted state of one instance this CLI manages.
// Backend is a string rather than an enum so a Docker backend (a later
// phase) can introduce a new value without a breaking schema change to
// instance.json — "native" is the only value this phase ever writes.
type instanceRecord struct {
	Name     string `json:"name"`
	Backend  string `json:"backend"` // "native" is the only value this phase produces.
	PID      int    `json:"pid"`
	Port     int    `json:"port"`
	UIPort   int    `json:"uiPort"`
	Endpoint string `json:"endpoint"` // http://127.0.0.1:<port> — 127.0.0.1, not localhost (see cmd_start.go).
	DataDir  string `json:"dataDir,omitempty"`
	State    string `json:"state,omitempty"` // OVERCAST_STATE passthrough; empty means the daemon's own default.
	LogFile  string `json:"logFile"`
	// StartedAt is RFC3339 via time.Time's default JSON marshalling.
	StartedAt time.Time `json:"startedAt"`
	Version   string    `json:"version"`
	// Env is the OVERCAST_*/AWS_* passthrough `start` was given via repeated
	// --env KEY=VALUE, persisted so `restart` reproduces the same instance
	// instead of silently falling back to flag defaults. It is written to
	// instance.json in plain text on disk — do not pass secrets through
	// --env; anything sensitive belongs in the daemon's own environment or a
	// file under --data-dir, not in a CLI flag this registry keeps a copy of.
	Env map[string]string `json:"env,omitempty"`
}

// instanceDir returns the directory holding one instance's registry entry
// under base (normally instancesBaseDir()'s result, passed in so callers
// that already resolved it once don't pay for it twice).
func instanceDir(base, name string) string {
	return filepath.Join(base, name)
}

// recordPath returns the instance.json path inside an instance's directory.
func recordPath(dir string) string {
	return filepath.Join(dir, "instance.json")
}

// saveInstance writes rec's registry entry, creating the instance's
// directory if needed. Overwrites any existing record for the same name —
// this is how a stale (dead-pid) record gets silently replaced by a fresh
// `overcast start`.
func saveInstance(rec instanceRecord) error {
	base, err := instancesBaseDir()
	if err != nil {
		return err
	}
	dir := instanceDir(base, rec.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create instance directory %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance record: %w", err)
	}
	if err := os.WriteFile(recordPath(dir), data, 0644); err != nil {
		return fmt.Errorf("write instance record: %w", err)
	}
	return nil
}

// loadInstance reads the registry entry for name. Returns an error when no
// such instance has ever been started (or its record was already removed).
func loadInstance(name string) (instanceRecord, error) {
	base, err := instancesBaseDir()
	if err != nil {
		return instanceRecord{}, err
	}
	dir := instanceDir(base, name)
	data, err := os.ReadFile(recordPath(dir))
	if err != nil {
		return instanceRecord{}, fmt.Errorf("no registry record for instance %q: %w", name, err)
	}
	var rec instanceRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return instanceRecord{}, fmt.Errorf("parse instance record %s: %w", recordPath(dir), err)
	}
	return rec, nil
}

// listInstances returns every registered instance's record, sorted by name.
// A missing base directory (nothing has ever been started) is not an error
// — it returns an empty slice, same as a directory that exists but is
// empty. An entry whose instance.json fails to parse is skipped rather than
// failing the whole listing, so one corrupt record can't hide every other
// instance from `overcast stop`'s completion or a future `overcast list`.
func listInstances() ([]instanceRecord, error) {
	base, err := instancesBaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read instances directory %s: %w", base, err)
	}
	var recs []instanceRecord
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rec, err := loadInstance(e.Name())
		if err != nil {
			continue
		}
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	return recs, nil
}

// removeInstance deletes an instance's registry directory (instance.json and
// daemon.log). Removing a name with no record is not an error — RemoveAll
// on a nonexistent path returns nil — which is what lets stopInstance
// (cmd_stop.go) call this unconditionally on both the normal and
// stale-record paths.
func removeInstance(name string) error {
	base, err := instancesBaseDir()
	if err != nil {
		return err
	}
	dir := instanceDir(base, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove instance directory %s: %w", dir, err)
	}
	return nil
}

// instanceRunning reports whether rec's process is currently alive.
// processAlive (detach_unix.go / detach_windows.go) does the actual
// platform-specific liveness probe. This does not distinguish "this pid now
// belongs to something else" from "still our daemon" — pid reuse on a
// machine that has run many processes since this instance started is
// possible but rare enough that v1 accepts it, the same way it accepts two
// concurrent `overcast start` invocations racing on one name (see this
// file's header comment).
func instanceRunning(rec instanceRecord) bool {
	return processAlive(rec.PID)
}

// completeInstanceNames is the shell-completion source for `overcast start
// --name`: every known instance, running or not, since naming an existing
// stopped instance is exactly how a caller would restart it under that name.
func completeInstanceNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	recs, err := listInstances()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(recs))
	for _, r := range recs {
		names = append(names, r.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeRunningInstanceNames is the shell-completion source for `overcast
// stop`/`overcast restart`'s positional [name]: only instances that
// currently read as running, since completing a stopped instance's name for
// `stop` would just offer to remove a record already gone.
func completeRunningInstanceNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	recs, err := listInstances()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, r := range recs {
		if instanceRunning(r) {
			names = append(names, r.Name)
		}
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
