package main

// cmd_network.go — `overcast network status|reset`. The host-side half of the
// exact-state network verification the daemon runs at startup.
//
// The daemon can inspect a network and tell you it is wrong; what it cannot do
// is fix one that has containers on it. Docker will not change a network's
// isolation, driver, subnet or options in place, and recreating a network under
// running containers drops every one of them off it mid-run — so a daemon that
// did that at startup would trade a wrong setting for a broken stack. That
// leaves the repair to a moment somebody chose, which is this command.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/dataplane"
	"github.com/overcast-sh/overcast/internal/docker"
)

// networkStdinIsTerminal reports whether the real process stdin is an
// interactive terminal. A package-level seam, as in cmd_reset.go, so tests can
// simulate both an interactive and a piped stdin without needing a terminal.
var networkStdinIsTerminal = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

// errDriftFound is what `overcast network status` exits non-zero with when a
// network is not in its configured state.
//
// Prose on stdout is for a person; an exit status is for the pipeline that
// runs this in CI to catch a machine drifting between releases. The message is
// deliberately the summary and not a repeat of the per-network detail, which
// has already been printed above it.
var errDriftFound = errors.New("one or more Docker networks are not in the state this configuration " +
	"asks for; see above, and `overcast network reset --dry-run`")

func newNetworkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Inspect and rebuild the Docker networks Overcast manages",
	}
	cmd.AddCommand(newNetworkStatusCmd(), newNetworkResetCmd())
	return cmd
}

func newNetworkStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Compare every Overcast-managed Docker network against the state this configuration asks for",
		Long: "Inspects the two planes and every per-VPC network, and reports each field on which the live " +
			"network differs from what this configuration would create. Reads only; `overcast network " +
			"reset` is what changes anything.",
		Args: cobra.NoArgs,
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runNetwork(cmd, func(nc *networkContext) error {
				targets, err := nc.targets(cmd.Context(), nil)
				if err != nil {
					return err
				}
				if !nc.report(cmd, targets) {
					// A documented non-zero exit, so `overcast network status`
					// is usable as a CI gate rather than only as prose.
					return errDriftFound
				}
				return nil
			})
		},
	}
	return cmd
}

func newNetworkResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset [network...]",
		Short: "Rebuild Overcast's Docker networks in the exact state this configuration asks for",
		Long: "Removes and recreates each named network so that its isolation, driver, addressing and " +
			"driver options are exactly what this configuration would have created. Docker cannot change " +
			"those in place, which is why a rebuild is the only repair.\n\n" +
			"Containers Overcast manages that sit on the network are stopped first; containers it does not " +
			"manage are disconnected and left running, because stopping somebody else's container is not " +
			"this command's business. With no arguments every network this configuration names is " +
			"considered, and any already in the right state is left alone.\n\n" +
			"Run it with --dry-run first: that prints exactly what would be stopped, disconnected and " +
			"rebuilt, and changes nothing.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			force, _ := cmd.Flags().GetBool("force")
			yes, _ := cmd.Flags().GetBool("yes")
			return runNetwork(cmd, func(nc *networkContext) error {
				targets, err := nc.targets(cmd.Context(), args)
				if err != nil {
					return err
				}
				work := selectWork(targets, force)
				if len(work) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(),
						"Every network is already in the state this configuration asks for. Nothing to do.")
					return nil
				}
				nc.printPlan(cmd, work)
				if dryRun {
					fmt.Fprintln(cmd.OutOrStdout(), "\n--dry-run: nothing was changed.")
					return nil
				}
				if !yes && networkStdinIsTerminal() {
					ok, err := confirmNetworkReset(cmd, work)
					if err != nil {
						return err
					}
					if !ok {
						fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
						return nil
					}
				}
				return nc.apply(cmd, work, force)
			})
		},
	}
	cmd.Flags().Bool("dry-run", false, "print what would be stopped, disconnected and rebuilt, and change nothing")
	cmd.Flags().Bool("force", false, "rebuild even a network that already matches this configuration")
	cmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// networkContext is the shared setup both subcommands need: the configuration
// the daemon would read, and a client for the daemon's own Docker socket.
type networkContext struct {
	cfg *config.Config
	dc  *docker.Client
	log *zap.Logger

	// managedOnce memoizes the one listing of Overcast-managed containers.
	// classifyAttached needs it per network and runs twice per network (once to
	// plan, once under the lock to act); the answer is the same every time.
	managedOnce sync.Once
	managed     map[string]docker.ContainerSummary
}

// managedContainers is every container Overcast created, by id, listed once.
//
// An error is not fatal here: a caller that cannot list them treats every
// attached container as somebody else's, which is the conservative direction —
// it disconnects rather than stops.
func (nc *networkContext) managedContainers(ctx context.Context) map[string]docker.ContainerSummary {
	nc.managedOnce.Do(func() {
		nc.managed = map[string]docker.ContainerSummary{}
		all, err := nc.dc.ListContainers(ctx, "")
		if err != nil {
			return
		}
		for _, c := range all {
			nc.managed[c.ID] = c
		}
	})
	return nc.managed
}

// runNetwork resolves the configuration and Docker client once, and hands them
// to fn. The configuration is loaded exactly as `overcast serve` loads it, so
// the state this command rebuilds towards is the state the daemon would.
func runNetwork(_ *cobra.Command, fn func(*networkContext) error) error {
	log := zap.NewNop()
	cfg, err := loadDataDir()
	if err != nil {
		return err
	}
	dc := docker.NewClient(cfg.LambdaDockerSocket, log)
	if !dc.Available(5 * time.Second) {
		return fmt.Errorf("Docker is not reachable at %s — the networks this command manages "+
			"live on that daemon", cfg.LambdaDockerSocket)
	}
	return fn(&networkContext{cfg: cfg, dc: dc, log: log})
}

// networkTarget is one network, the state this configuration wants it in, and
// what it is in now.
type networkTarget struct {
	spec     docker.ResolvedNetworkSpec
	info     *docker.NetworkInspect
	diffs    []docker.NetworkFieldDiff
	absent   bool
	managed  []docker.ContainerSummary // Overcast's own, to stop
	foreign  []string                  // not Overcast's, to disconnect
	otherOwn string                    // instance label naming somebody else

	// skipped marks a network a bare invocation will not act on because it
	// belongs to somebody else. Reported, never worked on.
	skipped bool

	// isolationKnown is false for a VPC network whose recorded gateway fact
	// could not be read, where `internal` and the spec hash are not this
	// command's to judge — see networkSpecTarget.
	isolationKnown bool
}

// targets resolves the networks to act on and inspects each.
//
// With no names it takes every network this configuration names: the two
// planes, and every per-VPC network under this configuration's prefix — minus
// any that belong to another Overcast instance, which a bare invocation never
// touches. Named networks are taken literally, and one that is not Overcast's
// to manage is an error rather than a silent skip: being told "no such managed
// network" beats a command that reports success having done nothing.
//
// explicit says the caller named the networks, which is what allows a
// foreign-owned one to be acted on at all — and only after it has been refused
// once with the owner named.
func (nc *networkContext) targets(ctx context.Context, names []string) ([]networkTarget, error) {
	specs, err := nc.specs(ctx)
	if err != nil {
		return nil, err
	}
	mine := nc.instanceIdentity(ctx)

	if len(names) > 0 {
		byName := make(map[string]networkSpecTarget, len(specs))
		for _, s := range specs {
			byName[s.spec.Name] = s
		}
		filtered := make([]networkSpecTarget, 0, len(names))
		for _, name := range names {
			s, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("%q is not a network this configuration manages. Managed networks "+
					"are %s, and %s* for each VPC; run `overcast network status` to list them",
					name, nc.cfg.Network+" and "+nc.cfg.ControlNetwork(), nc.cfg.VPCNetworkPrefix())
			}
			if foreign, owner := s.foreignTo(mine); foreign {
				return nil, fmt.Errorf("%q belongs to another Overcast instance (%s). Rebuilding it to "+
					"this configuration would break the instance that owns it: stop that instance first, "+
					"or give this one its own OVERCAST_NETWORK", name, owner)
			}
			filtered = append(filtered, s)
		}
		specs = filtered
	}

	targets := make([]networkTarget, 0, len(specs))
	for _, s := range specs {
		if foreign, owner := s.foreignTo(mine); foreign {
			// A bare invocation skips it entirely rather than listing it as
			// work: naming it is how somebody says they meant it, and even then
			// it is refused above.
			targets = append(targets, networkTarget{spec: s.spec, otherOwn: owner, skipped: true})
			continue
		}
		t := networkTarget{spec: s.spec, isolationKnown: s.isolationKnown}
		info, err := nc.dc.InspectNetwork(ctx, s.spec.Name)
		if err != nil || info == nil {
			t.absent = true
			targets = append(targets, t)
			continue
		}
		t.info = info
		t.diffs = s.spec.Diff(info)
		if !s.isolationKnown {
			t.diffs = unknowableFields(t.diffs)
		}
		t.managed, t.foreign = nc.classifyAttached(ctx, info)
		targets = append(targets, t)
	}
	return targets, nil
}

// foreignTo reports whether this network belongs to an Overcast instance other
// than the one running the command, and names it.
//
// A network with no instance label is not foreign — it predates the label, and
// the planes carry none by design (they are scoped by name instead). A network
// whose owner cannot be compared, because this command could not establish its
// own identity, *is* treated as foreign: absence of proof is not proof of
// ownership, and the cost of being wrong here is rebuilding a network another
// instance is serving.
func (s networkSpecTarget) foreignTo(mine string) (foreign bool, owner string) {
	if s.owner == "" {
		return false, ""
	}
	if mine == "" || s.owner != mine {
		return true, s.owner
	}
	return false, ""
}

// specs is the desired state of every network this configuration names.
//
// The planes come straight from dataplane.PlaneSpecs, so this command and the
// daemon cannot disagree about them.
//
// Per-VPC networks are discovered from the daemon rather than from EC2 state —
// the CLI has no store to read, and a network that exists is the thing being
// repaired anyway. Everything the spec needs is read off the live network: the
// VPC id and subnet, the owning instance, and **the internet-gateway fact the
// daemon recorded when it last created it** (docker.LabelGatewayAttached).
//
// That last one is the whole reason the label exists. Under `open` a VPC's
// isolation follows its gateway, and a CLI that assumed a gateway-less VPC
// would want `internal=true` for every network on the machine — reporting a
// mismatch on every gateway-attached VPC and then "repairing" it into a state
// that contradicts the template, which the daemon's next reconcile would flip
// straight back. Reading the fact is the only honest option available here;
// inventing it is worse than declining to compare.
func (nc *networkContext) specs(ctx context.Context) ([]networkSpecTarget, error) {
	var specs []networkSpecTarget
	for _, spec := range dataplane.PlaneSpecs(nc.cfg) {
		specs = append(specs, networkSpecTarget{spec: spec.Resolve(ctx, nc.dc), isolationKnown: true})
	}

	networks, err := nc.dc.ListNetworks(ctx, "ec2")
	if err != nil {
		return nil, fmt.Errorf("list Overcast networks: %w", err)
	}
	prefix := nc.cfg.VPCNetworkPrefix()
	sort.Slice(networks, func(i, j int) bool { return networks[i].Name < networks[j].Name })
	for i := range networks {
		n := networks[i]
		if !strings.HasPrefix(n.Name, prefix) {
			continue
		}
		gateway, known := gatewayAttached(n.Labels)
		specs = append(specs, networkSpecTarget{
			spec: dataplane.VPCNetworkSpec(nc.cfg, dataplane.VPCNetwork{
				VPCID:              strings.TrimPrefix(n.Name, prefix),
				Subnet:             n.Subnet(),
				Owner:              n.Instance(),
				Internal:           dataplane.VPCNetworkInternal(nc.cfg, gateway),
				HasInternetGateway: gateway,
			}).Resolve(ctx, nc.dc),
			isolationKnown: known,
			owner:          n.Instance(),
		})
	}
	return specs, nil
}

// networkSpecTarget is a desired state plus what this command is entitled to
// conclude from it.
type networkSpecTarget struct {
	spec docker.ResolvedNetworkSpec

	// isolationKnown reports whether the internet-gateway fact behind the
	// isolation could be established. False for a VPC network created before
	// Overcast recorded it, where the isolation and the spec hash that covers it
	// are not this command's to judge.
	isolationKnown bool

	// owner is the instance label on the live network, when it has one.
	owner string
}

// gatewayAttached reads the recorded internet-gateway fact off a network's
// labels. The second result is false when the network predates the label, which
// is "not knowable from here" and never "no gateway".
func gatewayAttached(labels map[string]string) (attached, known bool) {
	v, ok := labels[docker.LabelGatewayAttached]
	if !ok {
		return false, false
	}
	return v == "true", true
}

// unknowableFields are the comparisons that depend on the internet-gateway
// fact. When that fact could not be read, these are dropped from the diff
// rather than guessed at — and the drop is printed, so a clean report never
// silently means "I did not look".
func unknowableFields(diffs []docker.NetworkFieldDiff) []docker.NetworkFieldDiff {
	kept := make([]docker.NetworkFieldDiff, 0, len(diffs))
	for _, d := range diffs {
		if d.Field == "internal" || d.Field == docker.LabelSpecHash {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// classifyAttached splits the containers on a network into the ones Overcast
// manages and the ones it does not.
//
// The split is by label, not by guesswork: ListContainers filters on
// docker.LabelManaged, so "ours" means a container Overcast created and
// everything else is somebody's compose service, test harness or editor.
//
// The asymmetry is the point. A container Overcast started is Overcast's to
// stop; one it did not start is only ever taken off the network and left
// running. Stopping those would be this command reaching well outside what it
// was asked to repair — and they are named in the plan before anything happens,
// so nobody finds out afterwards.
func (nc *networkContext) classifyAttached(ctx context.Context, info *docker.NetworkInspect) ([]docker.ContainerSummary, []string) {
	if len(info.Containers) == 0 {
		return nil, nil
	}
	ours := nc.managedContainers(ctx)
	var managed []docker.ContainerSummary
	var foreign []string
	for id, ep := range info.Containers {
		if c, ok := ours[id]; ok {
			managed = append(managed, c)
			continue
		}
		name := ep.Name
		if name == "" {
			name = id
		}
		foreign = append(foreign, name)
	}
	sort.Slice(managed, func(i, j int) bool { return managed[i].ID < managed[j].ID })
	sort.Strings(foreign)
	return managed, foreign
}

// selectWork keeps the targets that need rebuilding. A network that already
// matches is skipped unless --force, and an absent one is nothing to reset:
// the daemon creates it correctly on its next start.
func selectWork(targets []networkTarget, force bool) []networkTarget {
	work := make([]networkTarget, 0, len(targets))
	for _, t := range targets {
		if t.absent || t.skipped {
			continue
		}
		if len(t.diffs) == 0 && !force {
			continue
		}
		work = append(work, t)
	}
	return work
}

// report prints the verification for every target — the `status` subcommand.
//
// Returns true when everything is in its configured state, which is what the
// command's exit status is built from: a CI caller needs something to assert on
// besides parsing this text.
func (nc *networkContext) report(cmd *cobra.Command, targets []networkTarget) bool {
	out := cmd.OutOrStdout()
	ok := true
	for _, t := range targets {
		switch {
		case t.skipped:
			fmt.Fprintf(out, "%s: skipped — belongs to another Overcast instance (%s)\n",
				t.spec.Name, t.otherOwn)
		case t.absent:
			fmt.Fprintf(out, "%s: absent — the daemon creates it on its next start\n", t.spec.Name)
		case len(t.diffs) == 0:
			fmt.Fprintf(out, "%s: ok (internal=%t, spec %s)%s\n",
				t.spec.Name, t.info.Internal, t.spec.SpecHash(), nc.egressNote(t))
		default:
			ok = false
			fmt.Fprintf(out, "%s: NOT in the configured state%s\n", t.spec.Name, nc.egressNote(t))
			for _, d := range t.diffs {
				fmt.Fprintf(out, "    %s\n", d)
			}
			nc.printAttachments(out, t)
		}
		if !t.skipped && !t.absent && !t.isolationKnown {
			fmt.Fprintf(out, "    (isolation and spec hash not compared: this network predates the "+
				"recorded gateway state, so only the daemon can say what it should be)\n")
		}
	}
	return ok
}

// egressNote says where an isolated network's containers actually get their
// egress, which is the one thing `internal=true` does not tell you.
//
// Under `open` a VPC with no internet gateway keeps an `--internal` bridge —
// honest about the template — while its containers reach the internet through
// the routable control plane they are also on. That combination is exactly the
// confusion #1564 was about, and `docker network inspect` will not explain it,
// so the place it gets read is the place it gets said.
func (nc *networkContext) egressNote(t networkTarget) string {
	if t.info == nil || !t.info.Internal || !strings.HasPrefix(t.spec.Name, nc.cfg.VPCNetworkPrefix()) {
		return ""
	}
	if egressModeName(nc.cfg) == config.VPCEgressNone {
		return " — no egress (OVERCAST_VPC_EGRESS=none)"
	}
	return " — egress via " + nc.cfg.ControlNetwork()
}

// printPlan prints what a reset would do, which is also what --dry-run prints:
// one rendering, so the preview cannot describe a different act from the one
// that runs.
func (nc *networkContext) printPlan(cmd *cobra.Command, work []networkTarget) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Rebuilding %d network(s) to match this configuration "+
		"(OVERCAST_VPC_EGRESS=%s):\n", len(work), egressModeName(nc.cfg))
	for _, t := range work {
		fmt.Fprintf(out, "\n  %s\n", t.spec.Name)
		if len(t.diffs) == 0 {
			fmt.Fprintln(out, "    already matches; rebuilding because --force was given")
		}
		for _, d := range t.diffs {
			fmt.Fprintf(out, "    %s\n", d)
		}
		nc.printAttachments(out, t)
	}
}

// printAttachments prints what is on a network and what would happen to it.
func (nc *networkContext) printAttachments(out interface{ Write([]byte) (int, error) }, t networkTarget) {
	if t.otherOwn != "" {
		fmt.Fprintf(out, "    ! owned by another Overcast instance (%s) — it will be rebuilt to THIS "+
			"configuration, which may not be what that instance wants\n", t.otherOwn)
	}
	for _, c := range t.managed {
		fmt.Fprintf(out, "    stop       %s (%s)\n", containerName(c), c.State)
	}
	for _, name := range t.foreign {
		fmt.Fprintf(out, "    disconnect %s (not Overcast's — left running)\n", name)
	}
}

// apply performs the rebuild.
//
// Ordering is fixed and load-bearing: stop what Overcast owns, disconnect what
// it does not, then remove and recreate. Docker refuses to remove a network
// with any endpoint on it, so a rebuild that skipped either step would fail
// with a 403 naming nothing useful.
func (nc *networkContext) apply(cmd *cobra.Command, work []networkTarget, force bool) error {
	out := cmd.OutOrStdout()
	ctx := cmd.Context()
	var failures []string

	for _, t := range work {
		if err := nc.rebuild(ctx, out, t, force); err != nil {
			failures = append(failures, err.Error())
		}
	}

	if len(failures) > 0 {
		return fmt.Errorf("some networks could not be rebuilt:\n  %s", strings.Join(failures, "\n  "))
	}
	fmt.Fprintln(out, "\nRestart Overcast and anything that was stopped; containers rejoin on their next start.")
	return nil
}

// confirmNetworkReset asks before doing something that stops containers.
func confirmNetworkReset(cmd *cobra.Command, work []networkTarget) (bool, error) {
	stopping := 0
	for _, t := range work {
		stopping += len(t.managed)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"\nThis rebuilds %d network(s) and stops %d Overcast container(s). Continue? [y/N] ",
		len(work), stopping)
	var answer string
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &answer); err != nil {
		// An empty line reads as an error from Fscanln, and an empty line is a
		// "no" — which is the safe reading of every other error here too.
		return false, nil
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

// containerName renders a container for a message: its first Docker name
// without the leading slash, or its id when it has none.
func containerName(c docker.ContainerSummary) string {
	if len(c.Names) > 0 && c.Names[0] != "" {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID
}

// egressModeName is the configured mode, defaulted, for a message.
func egressModeName(cfg *config.Config) config.VPCEgressMode {
	if cfg.VPCEgress == "" {
		return config.VPCEgressOpen
	}
	return cfg.VPCEgress
}

// rebuild stops, disconnects, removes and recreates one network.
//
// Ordering is fixed and load-bearing: stop what Overcast owns, disconnect
// everything that is left, then remove and recreate. Docker refuses to remove a
// network with any endpoint on it, so skipping either step fails with a 403
// naming nothing useful.
//
// The stop/disconnect asymmetry is the ownership rule. A container Overcast
// started is Overcast's to stop; one it did not start is somebody's compose
// service or test harness. Those come off the network — they have to, or the
// removal is refused — and they keep running, and the plan said so before any
// of this happened.
func (nc *networkContext) rebuild(ctx context.Context, out io.Writer, t networkTarget, force bool) error {
	// The same lock the daemon's own repair paths take, so a rebuild started
	// here cannot interleave with one an Overcast in this process is doing. A
	// separate Overcast process is kept off this network by the ownership
	// label instead — see docker.LockNetwork.
	defer docker.LockNetwork(t.spec.Name)()

	// Re-read under the lock. What was true when the plan was printed need not
	// be true now: a container may have started on the network, or the running
	// daemon may have rebuilt it already.
	info, err := nc.dc.InspectNetwork(ctx, t.spec.Name)
	if err != nil || info == nil {
		return fmt.Errorf("%s: vanished before it could be rebuilt", t.spec.Name)
	}
	// Re-diffing under the lock is what stops two resets, or a reset racing the
	// running daemon, from rebuilding the same network twice. --force is the
	// operator saying they want the rebuild regardless, so it skips the check
	// rather than being printed in the plan and then silently dropped here.
	if !force && len(t.spec.Diff(info)) == 0 {
		fmt.Fprintf(out, "%s: already in the configured state; left alone\n", t.spec.Name)
		return nil
	}
	managed, foreign := nc.classifyAttached(ctx, info)

	for _, c := range managed {
		if err := nc.dc.StopContainer(ctx, c.ID, 10); err != nil {
			return fmt.Errorf("stop %s: %w", containerName(c), err)
		}
	}
	if len(foreign) > 0 {
		fmt.Fprintf(out, "%s: disconnecting %d container(s) Overcast does not manage; "+
			"they keep running: %s\n", t.spec.Name, len(foreign), strings.Join(foreign, ", "))
	}
	for id := range info.Containers {
		if err := nc.dc.DisconnectNetwork(ctx, t.spec.Name, id); err != nil {
			// A container that stopped between the plan and now is already off
			// the network, which is the outcome wanted.
			nc.log.Debug("disconnect", zap.Error(err))
		}
	}

	// Remove by id and confirm it, for the reason docker.recreateToSpec gives:
	// RemoveNetwork reports a missing network as success, so a 404 from a
	// network somebody else rebuilt is indistinguishable from a removal this
	// call performed — and creating on the strength of it would overwrite
	// theirs with this spec.
	if err := nc.dc.RemoveNetwork(ctx, info.ID); err != nil {
		return fmt.Errorf("remove %s: %w", t.spec.Name, err)
	}
	if still, inspectErr := nc.dc.InspectNetwork(ctx, t.spec.Name); inspectErr == nil && still != nil {
		return fmt.Errorf("%s: still present after removal — something else is rebuilding it, so it "+
			"was left alone rather than overwritten", t.spec.Name)
	}
	if _, err := nc.dc.CreateNetworkWithOptions(ctx, t.spec.CreateOptions()); err != nil {
		return fmt.Errorf("recreate %s: %w", t.spec.Name, err)
	}
	fmt.Fprintf(out, "rebuilt %s (internal=%t, spec %s)\n", t.spec.Name, t.spec.Internal, t.spec.SpecHash())
	return nil
}

// instanceIdentity is the sweep-domain identity of the daemon this command is
// pointed at — the value stamped into `overcast.instance` on the networks it
// created.
//
// It comes from the running daemon's `/_overcast/health`, because it lives in
// the state store and this process has none. When the daemon is not running it
// is unknowable, and the caller must treat every labelled network as one it
// cannot prove is its own — see networkSpecTarget.foreignTo. That is the
// conservative direction: the cost of guessing wrong is rebuilding a network
// another instance is serving.
func (nc *networkContext) instanceIdentity(ctx context.Context) string {
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/_overcast/health", nc.cfg.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var health struct {
		Docker struct {
			Instance string `json:"instance"`
		} `json:"docker"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return ""
	}
	return health.Docker.Instance
}
