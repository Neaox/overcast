package docker

// netspec.go answers one question on every startup: is the network Overcast is
// about to reuse in the exact state Overcast would have created it in?
//
// It exists because Docker's create-network call is not idempotent in the way
// it looks. Handed a name that already exists it returns the existing network
// and applies nothing — not the isolation, not the subnet, not the driver
// options. So a network created by an older Overcast, by a different
// configuration, or by hand keeps every setting it was born with, for the life
// of the machine, while every log line and every `docker network ls` says the
// name is present and correct. That is the shape of #1564: two engineers on one
// pinned version, different behaviour, nothing anywhere saying why.
//
// Checking only the field that happened to change last time does not fix that.
// A verification that compares one flag reports "matches" for a network that
// differs in four others, so the comparison here is field-by-field over the
// whole spec, and a network carrying no spec label at all — every network
// created before this code — is treated as mismatched rather than assumed
// innocent.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// Label keys carried by every network Overcast creates, on top of the
// ManagedLabels set. They are what makes the state of a network legible from
// `docker network inspect` alone, which is the outcome #1564 asked for.
const (
	// LabelSpecHash is the first 12 hex characters of the SHA-256 of the
	// resolved spec — driver, isolation, IPv6, IPAM and driver options. Two
	// networks with the same hash were asked for in the same state.
	//
	// It is a fast path, not the check: verification still compares every field,
	// because a hash proves what Overcast *asked* for and says nothing about
	// what somebody has done to the network since.
	LabelSpecHash = "overcast.network.spec-hash"

	// LabelSpecVersion is the Overcast version that created the network, so an
	// operator reading a mismatch can see which release's idea of the spec they
	// are looking at.
	LabelSpecVersion = "overcast.network.version"

	// LabelEgressMode records the OVERCAST_VPC_EGRESS mode in force when the
	// network was created. Isolation is derived from it, so a network whose
	// isolation surprises someone can be traced to the mode that chose it
	// without reading a log from a previous boot.
	LabelEgressMode = "overcast.network.egress"

	// LabelGatewayAttached records whether the VPC had an internet gateway when
	// the network was last created, as "true" or "false". Only VPC networks
	// carry it.
	//
	// It is the input to the isolation decision, written down beside the
	// outcome, and it exists for readers that have no state store to ask —
	// `overcast network status` and `overcast network reset` above all. Without
	// it the CLI cannot compute the same desired state the daemon does for a
	// gateway-attached VPC, and a CLI that guesses would report a mismatch that
	// is not there and then "repair" the network into one that is.
	//
	// Absent on a network created before this label existed, which the CLI reads
	// as "this fact is not knowable from here" rather than as "false".
	LabelGatewayAttached = "overcast.network.gateway"
)

// Bridge driver options Overcast sets explicitly rather than inheriting.
// Named because an option that is only ever a string literal in a create call
// cannot be verified against a live network without repeating the literal.
const (
	// OptionICC is inter-container communication on this bridge. Left at
	// Docker's default (on); named so the verification can compare it when a
	// spec pins it.
	OptionICC = "com.docker.network.bridge.enable_icc"

	// OptionIPMasquerade is source-NAT for traffic leaving this bridge. It is
	// the option that actually carries egress on a routable network, so a
	// network with it off looks routable and behaves isolated — the exact
	// failure mode this file exists to catch.
	OptionIPMasquerade = "com.docker.network.bridge.enable_ip_masquerade"
)

// NetworkSpec is the complete desired state of one network Overcast manages.
//
// "Complete" is the load-bearing word. Every field here is both written on
// create and compared on verify; a setting Overcast can ask for but cannot
// check is a setting that drifts silently, which is the bug class this type
// closes rather than a hypothetical.
type NetworkSpec struct {
	// Name is the Docker network name.
	Name string

	// Driver is the network driver. Empty means DefaultNetworkDriver.
	Driver string

	// Internal cuts the network off from the wider network — no egress, no
	// route to anything Docker did not put on it.
	//
	// Ignored when InternalMode is set.
	Internal bool

	// InternalMode decides Internal dynamically, using the Docker client Probe
	// has just dialled and verified — the seam a caller needs when the right
	// answer depends on facts about the daemon (a native Linux kernel vs.
	// Docker Desktop's VM) that are not knowable until a client exists to ask.
	// Takes precedence over Internal when set.
	//
	// It returns a reason as well as an answer, because a caller that has to
	// choose dynamically is exactly the caller whose answer nobody can predict
	// from the outside — see InternalDecision.
	//
	// Called once per spec, after the client is confirmed available and before
	// the network is created.
	InternalMode func(ctx context.Context, dc *Client) InternalDecision

	// IPv6 allocates IPv6 addresses on the network as well as IPv4.
	IPv6 bool

	// Subnet and Gateway pin IPAM. Left empty, Docker allocates from its own
	// address pools and the verification does not compare them — a spec that
	// did not ask for an address range has no opinion about the one Docker
	// picked, and comparing it would report a mismatch on every network.
	Subnet  string
	Gateway string

	// Options are driver options this spec pins. Only the keys present here are
	// compared: Docker reports its own defaults for the rest, and treating an
	// unset option as a mismatch would make every network permanently wrong.
	Options map[string]string

	// Labels are the identity labels the network carries — ManagedLabels plus
	// the instance identity. The spec labels (LabelSpecHash and friends) are
	// added by CreateOptions and must not be set here; they are derived.
	Labels map[string]string

	// Owner is the Overcast instance this network belongs to, stamped into
	// LabelInstance. A network whose owner is somebody else is never removed or
	// recreated, whatever it says in every other field — see LabelInstance:
	// absence is not permission, and presence of *another* name is a refusal.
	Owner string

	// Version is the Overcast version stamped into LabelSpecVersion.
	Version string

	// EgressMode is the OVERCAST_VPC_EGRESS value stamped into
	// LabelEgressMode. Recorded, never compared: changing the mode is a
	// deliberate act and the isolation it produces is compared on its own.
	EgressMode string
}

// InternalDecision is a resolved isolation choice and the reason for it.
//
// The reason is the entire point. The isolation of a network changes whether a
// function reaches the internet, and before #1564 nothing — not `docker network
// inspect`, not the logs — said which way it had gone or why.
type InternalDecision struct {
	// Internal is the answer applied to the network.
	Internal bool

	// Reason is a short phrase naming what decided it, safe to print:
	// "OVERCAST_VPC_EGRESS=none", "OVERCAST_CONTROL_PLANE_INTERNAL=true".
	Reason string

	// Warnings are consequences of this decision the operator has to be told
	// without having to ask — logged at WARN, once, at startup.
	//
	// Isolating a plane is the kind of choice whose cost lands a long way from
	// its cause: a function that cannot reach an external API fails minutes
	// later, inside somebody's application code, as ENETUNREACH. The moment of
	// decision is the only place that knows it is coming.
	Warnings []string
}

// ResolvedNetworkSpec is a NetworkSpec with its dynamic isolation settled.
// Everything downstream of Probe's one call to InternalMode takes this, so the
// probe cannot be re-run and quietly produce a second answer.
type ResolvedNetworkSpec struct {
	NetworkSpec

	// Internal shadows NetworkSpec.Internal with the resolved value.
	Internal bool

	// Reason and Warnings come from InternalDecision, or are empty for a spec
	// whose isolation is a constant.
	Reason   string
	Warnings []string

	// hash memoizes SpecHash for the duration of one EnsureNetwork call. Unset
	// is not a valid hash, so an unmemoized copy simply computes it.
	hash string
}

// Resolve settles the spec's isolation, calling InternalMode exactly once.
// A nil client is fine: InternalMode implementations must tolerate it, and a
// spec without one resolves to its static Internal.
func (s NetworkSpec) Resolve(ctx context.Context, dc *Client) ResolvedNetworkSpec {
	r := ResolvedNetworkSpec{NetworkSpec: s, Internal: s.Internal}
	if s.InternalMode != nil {
		d := s.InternalMode(ctx, dc)
		r.Internal, r.Reason, r.Warnings = d.Internal, d.Reason, d.Warnings
	}
	return r
}

// driver returns the driver this spec asks for, defaulted.
func (r ResolvedNetworkSpec) driver() string {
	if r.Driver == "" {
		return DefaultNetworkDriver
	}
	return r.Driver
}

// SpecHash is the identity of the desired state: the first 12 hex characters of
// the SHA-256 over every field that describes how the network behaves.
//
// Labels are deliberately excluded. They carry this hash, so including them
// would not terminate, and they say who created the network rather than what it
// is — two instances asking for the same network state should agree on the
// hash. So should two Overcast versions, which is why the version label is out
// too: a release that did not change the spec must not invalidate every network
// on the machine.
func (r ResolvedNetworkSpec) SpecHash() string {
	if r.hash != "" {
		return r.hash
	}
	keys := make([]string, 0, len(r.Options))
	for k := range r.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("driver=" + r.driver() + "\n")
	b.WriteString("internal=" + strconv.FormatBool(r.Internal) + "\n")
	b.WriteString("ipv6=" + strconv.FormatBool(r.IPv6) + "\n")
	b.WriteString("subnet=" + r.Subnet + "\n")
	b.WriteString("gateway=" + r.Gateway + "\n")
	for _, k := range keys {
		b.WriteString("option:" + k + "=" + r.Options[k] + "\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:12]
}

// CreateOptions is the spec as a create call, with the derived spec labels
// folded into the identity labels. Anything that creates one of these networks
// goes through here, so a network can never exist without the labels its own
// verification needs.
func (r ResolvedNetworkSpec) CreateOptions() CreateNetworkOptions {
	labels := make(map[string]string, len(r.Labels)+4)
	for k, v := range r.Labels {
		labels[k] = v
	}
	labels[LabelSpecHash] = r.SpecHash()
	if r.Version != "" {
		labels[LabelSpecVersion] = r.Version
	}
	if r.EgressMode != "" {
		labels[LabelEgressMode] = r.EgressMode
	}
	if r.Owner != "" {
		labels[LabelInstance] = r.Owner
	}
	return CreateNetworkOptions{
		Name:     r.Name,
		Driver:   r.driver(),
		Labels:   labels,
		Subnet:   r.Subnet,
		Gateway:  r.Gateway,
		Internal: r.Internal,
		IPv6:     r.IPv6,
		Options:  r.Options,
	}
}

// NetworkFieldDiff is one field on which a live network disagrees with the spec.
type NetworkFieldDiff struct {
	Field string `json:"field"`
	Want  string `json:"want"`
	Got   string `json:"got"`
}

// String renders one diff for a log line or the CLI: `internal: want true, got false`.
func (d NetworkFieldDiff) String() string {
	return fmt.Sprintf("%s: want %s, got %s", d.Field, d.Want, d.Got)
}

// DiffStrings renders a diff set for a message, in field order.
func DiffStrings(diffs []NetworkFieldDiff) []string {
	out := make([]string, 0, len(diffs))
	for _, d := range diffs {
		out = append(out, d.String())
	}
	return out
}

// Diff compares a live network against the spec, field by field, and returns
// every disagreement.
//
// An absent spec-hash label is a disagreement. Every network created before
// this code carries none, and that is exactly the population #1564 was about:
// networks whose settings nobody can account for, on machines where everything
// looked fine. Treating "no label" as "probably correct" would exempt the only
// networks that have ever actually been wrong.
//
// IPAM and driver options are compared only where the spec pins them. Docker
// reports its own allocation and its own option defaults for everything else,
// and calling those a mismatch would mark every network permanently broken.
func (r ResolvedNetworkSpec) Diff(info *NetworkInspect) []NetworkFieldDiff {
	if info == nil {
		return nil
	}
	var diffs []NetworkFieldDiff
	add := func(field, want, got string) {
		if want != got {
			diffs = append(diffs, NetworkFieldDiff{Field: field, Want: want, Got: got})
		}
	}

	add("driver", r.driver(), info.Driver)
	add("internal", strconv.FormatBool(r.Internal), strconv.FormatBool(info.Internal))
	add("ipv6", strconv.FormatBool(r.IPv6), strconv.FormatBool(info.EnableIPv6))
	if r.Subnet != "" {
		add("ipam.subnet", r.Subnet, info.Subnet())
	}
	if r.Gateway != "" {
		add("ipam.gateway", r.Gateway, info.Gateway())
	}
	optionKeys := make([]string, 0, len(r.Options))
	for k := range r.Options {
		optionKeys = append(optionKeys, k)
	}
	sort.Strings(optionKeys)
	for _, k := range optionKeys {
		add("option:"+k, r.Options[k], info.Options[k])
	}

	want := r.SpecHash()
	if got := info.Labels[LabelSpecHash]; got == "" {
		diffs = append(diffs, NetworkFieldDiff{
			Field: LabelSpecHash,
			Want:  want,
			Got:   "(absent — created before Overcast labelled its networks, or not by Overcast)",
		})
	} else {
		add(LabelSpecHash, want, got)
	}

	return diffs
}

// NetworkStatus is one network's state as it actually stands, as reported by
// /_overcast/health under `docker.networks`.
type NetworkStatus struct {
	Name string `json:"name"`

	// Internal is what the network *is*, not what this run asked for. Those
	// differ only in the drift case below, and reporting the ask there would
	// repeat #1564's original lie in a new place: an engineer reading
	// `internal: false` while their function gets ENETUNREACH is exactly the
	// confusion this field exists to end.
	Internal bool `json:"internal"`

	// Reason names what this run decided and why — "OVERCAST_VPC_EGRESS=none",
	// "OVERCAST_CONTROL_PLANE_INTERNAL=false". Empty for a network whose
	// isolation is a constant of the model rather than a decision.
	Reason string `json:"reason,omitempty"`

	// SpecHash is the desired state's identity — what the network would carry
	// in LabelSpecHash if it matched.
	SpecHash string `json:"specHash,omitempty"`

	// Mismatch lists every field on which the live network disagrees with the
	// spec, and is empty for a network in the state it should be in. A network
	// that was repaired reports none: the repair is what makes it true.
	Mismatch []NetworkFieldDiff `json:"mismatch,omitempty"`

	// Attached names the containers holding the network in its current state,
	// set only when they are the reason a mismatch could not be repaired.
	Attached []string `json:"attached,omitempty"`

	// Owner is the instance label on the live network when it names somebody
	// other than this instance. Set only in that case, because that is the one
	// case where the answer changes what Overcast may do.
	Owner string `json:"owner,omitempty"`

	// Drift is a one-line summary of an unrepaired mismatch, safe to print.
	// Empty is the normal case.
	Drift string `json:"drift,omitempty"`

	// Fix is the command that resolves the drift, when one exists.
	Fix string `json:"fix,omitempty"`
}

// OK reports whether the network is in the exact state the spec asked for.
//
// Drift counts, not only the field list. A network Overcast could not read
// carries a Drift and no Mismatch — nothing was compared, so there is nothing
// to list — and calling that OK would report "I did not look" as "I looked and
// it was right", which is the confusion this whole type exists to end.
func (s NetworkStatus) OK() bool { return len(s.Mismatch) == 0 && s.Drift == "" }

// EnsureNetwork creates the network if it is absent and verifies it against the
// spec if it is not, repairing it where repair is free and refusing to proceed
// quietly where it is not.
//
// The three outcomes, and why each is what it is:
//
//   - **Absent.** Created to spec. Nothing to reconcile.
//   - **Present and matching.** Left alone.
//   - **Present and differing.** Removed and recreated when nothing is attached
//     and the network is this instance's, because there is by definition no
//     connection to sever and no neighbour to disturb. Otherwise left exactly as
//     it is and reported at WARN naming every differing field, what is attached,
//     and the command that fixes it. Recreating under running containers would
//     drop every one of them off the network mid-run, which is a worse startup
//     than a setting one command away from correct; and recreating somebody
//     else's network is not Overcast's to do at all.
//
// Never returns an error for a network it declined to repair. A wrong-but-usable
// network is not a reason to refuse to start — it is a reason to say so loudly,
// which is what the returned NetworkStatus carries into `/_overcast/health`.
func EnsureNetwork(ctx context.Context, dc *Client, spec ResolvedNetworkSpec, logger *zap.Logger) (NetworkStatus, error) {
	// Hashed once and reused: Diff and CreateOptions both want it, and this is
	// the function whose selling point is that it is cheap enough to run on
	// every startup for every network.
	spec.hash = spec.SpecHash()
	status := NetworkStatus{
		Name:     spec.Name,
		Internal: spec.Internal,
		Reason:   spec.Reason,
		SpecHash: spec.hash,
	}
	if dc == nil || spec.Name == "" {
		return status, nil
	}

	// Everything below either reads a network in order to decide whether to
	// rebuild it, or rebuilds it. Both have to be inside the lock, and the read
	// has to happen *after* acquiring it: another path may have rebuilt the
	// network while this call was waiting, and acting on what was true before
	// the wait is how two rebuilds interleave into one broken network.
	defer LockNetwork(spec.Name)()

	info, err := inspectForVerify(ctx, dc, spec.Name)
	if err != nil || info == nil {
		// Absent, or unreadable — and the two are not the same thing, which is
		// the whole of #1582. A 404 means there is nothing here and the create
		// settles it. Any other error means the network may well exist, and
		// Docker's create call returns an existing network *unchanged*
		// (CreateNetworkWithOptions resolves "already exists" by looking the
		// network up and handing it back), so a create issued on the strength
		// of an unreadable inspect can leave a drifted network in place and
		// report it as freshly built to spec. That is #1564 exactly, on the
		// error path of the code written to close it.
		//
		// The create still happens either way: no network at all is a daemon
		// that starts and then fails every container create with an error
		// naming nothing about networks. What changes is that a blind create is
		// never trusted — it is verified below, or reported as unverified.
		unreadable := err != nil && !IsNotFound(err)
		if _, createErr := dc.CreateNetworkWithOptions(ctx, spec.CreateOptions()); createErr != nil {
			// Fatal, and deliberately not downgraded to a warning like a
			// declined repair is. A wrong-but-usable network is something to
			// start alongside and say loudly; *no* network is a daemon that
			// starts and then fails every container create with an error that
			// says nothing about networks. Address-pool exhaustion is the
			// common way to get here.
			status.Drift = "could not create: " + createErr.Error()
			return status, fmt.Errorf("create network %s: %w", spec.Name, createErr)
		}
		if !unreadable {
			return status, nil
		}
		// The network was there to be read and could not be, so the create may
		// have handed back somebody else's state. Read it again and fall into
		// the ordinary comparison; a network that is now readable gets the same
		// verification, repair and reporting as any other.
		info, err = dc.InspectNetwork(ctx, spec.Name)
		if err != nil || info == nil {
			return unverifiedStatus(status, spec.Name, err, logger), nil
		}
	}

	diffs := spec.Diff(info)
	if len(diffs) == 0 {
		status.Internal = info.Internal
		return status, nil
	}

	// Ownership before anything destructive. A network labelled for another
	// Overcast instance is that instance's to manage, however wrong it looks
	// from here — the alternative is one instance deleting a network another is
	// actively using, which is how a running VPC loses its network.
	if owner := info.Instance(); owner != "" && spec.Owner != "" && owner != spec.Owner {
		status.Internal = info.Internal
		status.Mismatch = diffs
		status.Owner = owner
		status.Drift = fmt.Sprintf(
			"network belongs to another Overcast instance (%s) and was left alone; %s",
			owner, strings.Join(DiffStrings(diffs), "; "))
		status.Fix = "run this instance with a different OVERCAST_NETWORK, or stop the other instance"
		logger.Warn("Docker network name is shared with another Overcast instance and does not match this "+
			"configuration — it was left alone, because removing it would break the instance that owns it. "+
			"Give this instance its own OVERCAST_NETWORK",
			zap.String("network", spec.Name),
			zap.String("owner", owner),
			zap.String("this_instance", spec.Owner),
			zap.Strings("differs", DiffStrings(diffs)))
		return status, nil
	}

	// A network somebody else's tooling created is never destroyed, even empty
	// and even under one of our names. `docker compose` stamps its own
	// ownership labels, and a compose network that happens to be called
	// `overcast` is byte-for-byte indistinguishable from a plane an older
	// Overcast made if the only thing checked is the absence of our label. The
	// VPC sweep already takes this line ("absence is not permission",
	// ec2.removeOrphanedNetworks); this aligns with it.
	if tool := foreignTool(info.Labels); tool != "" {
		status.Internal = info.Internal
		status.Mismatch = diffs
		status.Owner = tool
		status.Drift = fmt.Sprintf("a network created by %s already has this name and does not match "+
			"this configuration; it was left alone (%s)", tool, strings.Join(DiffStrings(diffs), "; "))
		status.Fix = "rename that network, or set OVERCAST_NETWORK to a name it does not use"
		logger.Warn("a Docker network created by another tool already has a name Overcast manages, and "+
			"does not match this configuration — it was left alone rather than removed. Rename it, or "+
			"give Overcast a different OVERCAST_NETWORK",
			zap.String("network", spec.Name),
			zap.String("created_by", tool),
			zap.Strings("differs", DiffStrings(diffs)))
		return status, nil
	}

	attached := info.AttachedNames()
	if len(attached) == 0 && !strings.EqualFold(info.Scope, "swarm") {
		if repaired := recreateToSpec(ctx, dc, spec, info, logger); repaired {
			// Warn, not Info: this destroyed and rebuilt a network, and on the
			// first start after an upgrade it destroys one nobody labelled. The
			// operator should be able to find out afterwards what went.
			logger.Warn("removed and recreated a Docker network to match this configuration — it "+
				"differed and had nothing attached",
				zap.String("network", spec.Name),
				zap.String("removed_id", info.ID),
				zap.Strings("differed", DiffStrings(diffs)))
			return status, nil
		}
		// Fall through to the warning: the rebuild did not happen, and saying
		// so beats reporting a state that was never applied.
	}

	status.Internal = info.Internal
	status.Mismatch = diffs
	status.Attached = attached
	status.Drift = fmt.Sprintf("network is not in the configured state (%s)",
		strings.Join(DiffStrings(diffs), "; "))
	status.Fix = "overcast network reset " + spec.Name
	if len(attached) > 0 {
		status.Drift += fmt.Sprintf("; %d container(s) attached", len(attached))
	}
	logger.Warn("Docker network is not in the state this configuration asks for, and could not be "+
		"recreated — Docker cannot change these settings in place. Run `overcast network reset "+
		spec.Name+"` to stop what is attached and rebuild it, or `overcast network reset --dry-run` "+
		"to see what that would do first",
		zap.String("network", spec.Name),
		zap.Strings("differs", DiffStrings(diffs)),
		zap.Strings("attached_containers", attached),
		zap.Int("attached", len(attached)))
	return status, nil
}

// recreateToSpec removes the network described by info and creates it again to
// spec. Callers must already hold LockNetwork(spec.Name).
//
// Two things here are deliberate and neither is obvious.
//
// **It removes by id, not by name.** The name is what the lock is keyed on and
// what the next create will claim; the id is what this call decided to destroy.
// Removing by name would remove whatever now holds the name — which, if
// something outside this process rebuilt it in the meantime, is not the network
// this call inspected.
//
// **A remove that reports success is not evidence the network is gone.**
// RemoveNetwork treats a missing network as success, because a missing network
// is normally the outcome wanted — so a 404 from a network somebody else
// removed and recreated reads exactly like a removal this call performed.
// Creating on the strength of that would overwrite their network with this
// spec. So the network is re-inspected, and the create only happens when it is
// genuinely absent.
func recreateToSpec(ctx context.Context, dc *Client, spec ResolvedNetworkSpec, info *NetworkInspect,
	logger *zap.Logger) bool {
	target := info.ID
	if target == "" {
		target = spec.Name
	}
	if rmErr := dc.RemoveNetwork(ctx, target); rmErr != nil {
		logger.Debug("could not remove the network to rebuild it",
			zap.String("network", spec.Name), zap.Error(rmErr))
		return false
	}

	// Confirm the removal rather than infer it. See above.
	if still, err := dc.InspectNetwork(ctx, spec.Name); err == nil && still != nil {
		logger.Warn("a Docker network was rebuilt by something else while this one was being "+
			"repaired — leaving it alone rather than overwriting it",
			zap.String("network", spec.Name),
			zap.String("was", info.ID),
			zap.String("now", still.ID))
		return false
	}

	if _, createErr := dc.CreateNetworkWithOptions(ctx, spec.CreateOptions()); createErr != nil {
		logger.Warn("could not recreate the network after removing it",
			zap.String("network", spec.Name), zap.Error(createErr))
		return false
	}
	return true
}

// foreignToolLabels are ownership marks other tools stamp on the networks they
// create. A network carrying one is that tool's, whatever its name.
//
// The list is deliberately short and specific. It is not trying to identify
// every possible creator — it cannot — but to catch the collision that actually
// happens: a Compose project with a service or network called `overcast`,
// sitting on the name Overcast's data plane wants.
var foreignToolLabels = map[string]string{
	"com.docker.compose.network": "docker compose",
	"com.docker.compose.project": "docker compose",
	"com.docker.stack.namespace": "docker stack",
	"io.podman.compose.project":  "podman compose",
}

// foreignTool names the tool that created a network, or "" when nothing on it
// claims one. A network Overcast labelled as its own is never foreign, whatever
// else it carries — an Overcast network attached to a Compose project is still
// Overcast's.
func foreignTool(labels map[string]string) string {
	if labels[LabelManaged] == "true" {
		return ""
	}
	for key, tool := range foreignToolLabels {
		if _, ok := labels[key]; ok {
			return tool
		}
	}
	return ""
}

// inspectForVerify reads a network, retrying once when the read fails for any
// reason other than "no such network".
//
// The retry is there because the two failures the caller has to tell apart are
// a 404, which is a fact, and everything else, which is usually a moment: a
// daemon busy behind a burst of container creates, a socket that dropped as
// Docker Desktop restarted, a request that outran the startup context. One
// immediate retry settles most of those, and settling them is worth a round
// trip — the alternative branch reports a network as unverified, which degrades
// health, and a health endpoint that cries wolf on a hiccup is one nobody reads.
//
// A 404 is never retried: it is the answer, and asking again only slows down
// the create that follows it.
func inspectForVerify(ctx context.Context, dc *Client, name string) (*NetworkInspect, error) {
	info, err := dc.InspectNetwork(ctx, name)
	if err == nil || IsNotFound(err) {
		return info, err
	}
	return dc.InspectNetwork(ctx, name)
}

// unverifiedStatus is what a network reports when Overcast could not read it
// well enough to say whether it matches.
//
// It is deliberately not a clean status. "I did not look" and "I looked and it
// was right" are different facts, and reporting the second for the first is the
// failure #1564 was filed about — an operator reading `internal: false` in
// /_overcast/health while their function gets ENETUNREACH. So this sets Drift,
// which NetworkStatus.OK reads as not-OK, so health degrades and the
// network-state-mismatch advisory fires with the reason quoted.
//
// There is no field list, because no comparison happened. The Fix is the
// command that will do one on demand.
func unverifiedStatus(status NetworkStatus, name string, err error, logger *zap.Logger) NetworkStatus {
	reason := "the Docker daemon did not answer"
	if err != nil {
		reason = err.Error()
	}
	status.Drift = "could not be verified against this configuration: " + reason
	status.Fix = "overcast network status"
	logger.Warn("could not read a Docker network to check it against this configuration — it is in use "+
		"but its settings were never compared, so what a container can reach may not be what this "+
		"configuration says. Run `overcast network status` to compare it once the daemon is answering",
		zap.String("network", name),
		zap.Error(err))
	return status
}
