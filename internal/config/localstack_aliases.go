package config

// LocalStack-compatibility alias mechanism (#1190, drop-in-replacement audit
// against https://docs.localstack.cloud/aws/capabilities/config/configuration/).
//
// Overcast is meant to be a drop-in replacement for LocalStack, so its
// documented environment variables are accepted as aliases of the matching
// Overcast setting wherever the semantics genuinely match. This is a
// deliberate exception to Overcast's alpha "no aliases" policy: that policy
// governs Overcast's own past names (e.g. the removed OVERCAST_HOST, see
// resolveListen), not another project's documented configuration. Aliasing
// LocalStack's variables is product value toward the maintainer-decided
// drop-in-replacement goal.
//
// Every alias in the table below shares one conflict rule, uniform with
// resolveListen/OVERCAST_HOST and resolveHostnameAlias/LOCALSTACK_HOST: both
// the LocalStack variable and the Overcast variable present and disagreeing
// fails startup naming both. Never silently prefer one. The same effective
// value in both is fine -- that is the natural shape of a compose file
// migrated line by line from LocalStack rather than cleaned up. And exactly
// one startup Info line is logged per alias actually recognised (see
// logLocalStackAliases in cmd/overcast/cmd_serve.go), so an operator sees
// which of their LocalStack-style settings took effect.
//
// One carve-out: an Overcast variable whose value is the Docker image's own
// baked-in ENV default -- today only OVERCAST_DATA_DIR, marked by
// OVERCAST_DATA_DIR_SOURCE=image -- is a default, not user intent, so the
// alias overrides it instead of conflicting with it (see Load's
// data-directory resolution). The images deliberately bake nothing else
// (see the ENV block in Dockerfile, and TestDockerfileBakesNoConfigDefaults),
// so every other conflict really is two user-set values disagreeing.
//
// Where LocalStack's semantics differ fundamentally from anything Overcast
// has, or the "equivalent" would be a false-friend trap rather than a true
// match, the variable is deliberately *not* aliased -- see
// docs/migration-from-localstack.md's "Not aliased" section for the
// reasoning on each.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// stringAlias is one entry in the LocalStack-compatibility alias table: a
// LocalStack environment variable whose value, when present, supplies or
// confirms the value of an Overcast variable that means the same thing.
type stringAlias struct {
	// localstackVar is the LocalStack-documented environment variable name.
	localstackVar string
	// overcastVar is the Overcast environment variable name it aliases, used
	// only in error/log messages -- resolveStringAlias takes the Overcast
	// variable's current raw value as a parameter rather than reading it
	// itself, so callers can chain several aliases for the same Overcast
	// variable (see HOSTNAME_EXTERNAL chaining after LOCALSTACK_HOST).
	overcastVar string
	// transform adapts a raw LocalStack value into the form Overcast expects
	// (e.g. DEBUG's "1" -> OVERCAST_LOG_LEVEL's "debug"). nil means the value
	// passes through unchanged (e.g. EDGE_PORT -> OVERCAST_PORT, same
	// format). transform may report the alias does not apply for this raw
	// value (ok=false) -- used for boolean-shaped LocalStack variables whose
	// "off" value (e.g. DEBUG=0) must behave as if the variable were unset
	// entirely, not as an alias supplying an empty string.
	transform func(raw string) (value string, ok bool, err error)
}

// resolveStringAlias applies one stringAlias entry against the environment,
// given the value Overcast's own variable currently holds (current, ""
// meaning unset) and currentLabel, the variable name to blame current on in
// a conflict message. For a simple, unchained alias currentLabel is just
// a.overcastVar; for a chain of several LocalStack names feeding the same
// Overcast variable (e.g. HOSTNAME_EXTERNAL applied after LOCALSTACK_HOST
// already resolved Hostname, in resolveHostnameAliasChain), the caller
// passes whichever earlier alias actually produced current, so a
// three-way disagreement names the two LocalStack variables that actually
// disagree rather than blaming the Overcast variable neither of them is.
//
// Returns the effective value and, when the LocalStack variable was present
// and accepted, its name as source -- empty when it was unset, empty, or
// transformed to not-applicable (ok=false from transform).
func resolveStringAlias(a stringAlias, current, currentLabel string) (value, source string, err error) {
	raw, set := os.LookupEnv(a.localstackVar)
	if !set || raw == "" {
		return current, "", nil
	}

	transformed := raw
	if a.transform != nil {
		var ok bool
		transformed, ok, err = a.transform(raw)
		if err != nil {
			return "", "", fmt.Errorf("config: %s=%q: %w", a.localstackVar, raw, err)
		}
		if !ok {
			return current, "", nil
		}
	}

	if current != "" && current != transformed {
		lsDisplay := fmt.Sprintf("%s=%q", a.localstackVar, raw)
		if transformed != raw {
			lsDisplay = fmt.Sprintf("%s=%q (interpreted as %s=%q)", a.localstackVar, raw, a.overcastVar, transformed)
		}
		return "", "", fmt.Errorf(
			"config: %s=%q and %s disagree -- %s is accepted as a LocalStack-compatibility alias for "+
				"%s, but they must agree when both are set; set only one, or set both to the same "+
				"effective value",
			currentLabel, current, lsDisplay, a.localstackVar, a.overcastVar)
	}
	return transformed, a.localstackVar, nil
}

// parseLocalstackBool parses a LocalStack boolean-shaped environment
// variable (documented default "0", set to "1" to enable). Accepts the same
// truthy/falsy spellings Overcast's own envBool does, so "true"/"false" and
// "yes"/"no" work too, since operators copy these between the two tools.
func parseLocalstackBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no", "":
		return false, nil
	default:
		return false, fmt.Errorf("expected a boolean (1/0, true/false, yes/no), got %q", raw)
	}
}

// orDefault returns v, or fallback when v is empty. Mirrors envOr's
// "empty means unset" convention for a value already read from the
// environment (possibly through an alias) rather than read fresh by key.
func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// parseIntOr parses raw as an integer, returning fallback when raw is empty
// or not a valid integer. Mirrors envInt's leniency for a value already read
// from the environment (possibly through an alias) rather than read fresh by
// key.
func parseIntOr(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// edgePortAlias: LocalStack's EDGE_PORT is the single port its gateway
// listens on -- the direct analogue of OVERCAST_PORT. Same format (a bare
// port number), so no transform.
var edgePortAlias = stringAlias{localstackVar: "EDGE_PORT", overcastVar: "OVERCAST_PORT"}

// defaultRegionAlias: LocalStack's DEFAULT_REGION is the default AWS region
// used when a request does not otherwise imply one -- the direct analogue of
// OVERCAST_DEFAULT_REGION.
var defaultRegionAlias = stringAlias{localstackVar: "DEFAULT_REGION", overcastVar: "OVERCAST_DEFAULT_REGION"}

// dataDirAlias: LocalStack's DATA_DIR is the directory persisted state is
// written under -- the direct analogue of OVERCAST_DATA_DIR. Setting it also
// counts as "the data directory was explicitly configured" for
// OVERCAST_STATE=auto's detection (see detectAutoStateSignals in
// state_auto.go), the same as OVERCAST_DATA_DIR would. In the Docker image,
// it overrides the baked-in OVERCAST_DATA_DIR=/data default instead of
// conflicting with it -- see the carve-out in the package comment above.
var dataDirAlias = stringAlias{localstackVar: "DATA_DIR", overcastVar: "OVERCAST_DATA_DIR"}

// hostnameExternalAlias: HOSTNAME_EXTERNAL is the legacy LocalStack name
// LOCALSTACK_HOST replaced (LocalStack's own changelog documents the
// rename); some still-current compose files carry it forward. Chained after
// resolveHostnameAlias (LOCALSTACK_HOST, #1190) in Load, so the three
// spellings -- OVERCAST_HOSTNAME, LOCALSTACK_HOST, HOSTNAME_EXTERNAL -- must
// all agree when more than one is set. Unlike LOCALSTACK_HOST,
// HOSTNAME_EXTERNAL never carried a port suffix, so it is a plain string
// alias with no host:port parsing.
var hostnameExternalAlias = stringAlias{localstackVar: "HOSTNAME_EXTERNAL", overcastVar: "OVERCAST_HOSTNAME"}

// lambdaInitTimeoutAlias: LocalStack's LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT is
// documented as "the time in seconds to wait for the Lambda runtime
// environment to start up" -- the same concept as Overcast's
// LAMBDA_INIT_TIMEOUT_SECONDS (the maximum time to wait for a Docker-backed
// Lambda runtime to finish INIT), just under a different name. Both are
// plain integer seconds, so no transform.
var lambdaInitTimeoutAlias = stringAlias{
	localstackVar: "LAMBDA_RUNTIME_ENVIRONMENT_TIMEOUT",
	overcastVar:   "LAMBDA_INIT_TIMEOUT_SECONDS",
}

// debugLogLevelAlias: LocalStack's DEBUG is a boolean that raises log
// verbosity; Overcast's OVERCAST_LOG_LEVEL is a five-level enum. DEBUG=1
// maps to "debug"; DEBUG=0 (or unset) is not-applicable, leaving
// OVERCAST_LOG_LEVEL's own default (info) in place rather than forcing it to
// any particular value.
var debugLogLevelAlias = stringAlias{
	localstackVar: "DEBUG",
	overcastVar:   "OVERCAST_LOG_LEVEL",
	transform: func(raw string) (string, bool, error) {
		on, err := parseLocalstackBool(raw)
		if err != nil {
			return "", false, err
		}
		if !on {
			return "", false, nil
		}
		return "debug", true, nil
	},
}

// persistenceStateAlias: LocalStack's PERSISTENCE=1 turns on its
// state-persistence mechanism, keyed off DATA_DIR being present. The closest
// named Overcast equivalent -- durability regardless of what else is
// configured -- is the "persistent" backend (StateBackendPersistent):
// PERSISTENCE=1 maps to OVERCAST_STATE=persistent; PERSISTENCE=0 (or unset)
// is not-applicable, leaving OVERCAST_STATE's own default/auto-detection in
// place (which already resolves to "hybrid", not "persistent", purely from
// OVERCAST_DATA_DIR/DATA_DIR being set -- see resolveAutoState -- so this
// alias only matters when PERSISTENCE is set without also pointing a data
// directory at something durable).
var persistenceStateAlias = stringAlias{
	localstackVar: "PERSISTENCE",
	overcastVar:   "OVERCAST_STATE",
	transform: func(raw string) (string, bool, error) {
		on, err := parseLocalstackBool(raw)
		if err != nil {
			return "", false, err
		}
		if !on {
			return "", false, nil
		}
		return string(StateBackendPersistent), true, nil
	},
}

// resolveGatewayListenAlias determines whether LocalStack's GATEWAY_LISTEN
// contributes to the effective OVERCAST_LISTEN/OVERCAST_PORT values.
// LocalStack documents its format as "<ip>:<port>[,<ip>:<port>...]" --
// Overcast's own multi-address bind support (OVERCAST_LISTEN, see
// parseHosts) already arrived at the same comma-separated-addresses idiom
// independently, but keeps the port in a separate variable (OVERCAST_PORT)
// rather than repeating it on every entry. Each GATEWAY_LISTEN entry must
// therefore agree on the port: a mismatched multi-port GATEWAY_LISTEN (e.g.
// binding two different ports at once) has no single OVERCAST_PORT to map
// it to, so it is a documented non-match -- this fails loudly naming the
// disagreement rather than picking one port and silently dropping the
// other bind.
//
// currentListenRaw/currentPortRaw are the raw OVERCAST_LISTEN/OVERCAST_PORT
// values (already folded through any earlier alias, e.g. EDGE_PORT) to
// reconcile against -- empty means unset. Returns the effective
// comma-separated address list and port string, plus which alias (if any)
// contributed -- "GATEWAY_LISTEN" or "".
func resolveGatewayListenAlias(currentListenRaw, currentPortRaw string) (listenValue, portValue, source string, err error) {
	raw, set := os.LookupEnv("GATEWAY_LISTEN")
	if !set || raw == "" {
		return currentListenRaw, currentPortRaw, "", nil
	}

	var addrs []string
	ports := map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		host, port, splitErr := net.SplitHostPort(entry)
		if splitErr != nil {
			return "", "", "", fmt.Errorf(
				"config: GATEWAY_LISTEN entry %q is not a valid ip:port (LocalStack's documented format is "+
					"\"<ip>:<port>[,<ip>:<port>...]\"): %w", entry, splitErr)
		}
		addrs = append(addrs, host)
		ports[port] = true
	}
	if len(addrs) == 0 {
		return "", "", "", fmt.Errorf("config: GATEWAY_LISTEN %q names no address to bind", raw)
	}
	if len(ports) > 1 {
		return "", "", "", fmt.Errorf(
			"config: GATEWAY_LISTEN %q names more than one port across its entries -- Overcast keeps a "+
				"single port for every bound address (OVERCAST_PORT), so a multi-port GATEWAY_LISTEN has "+
				"no single value to map it to (a documented non-match); use OVERCAST_LISTEN and "+
				"OVERCAST_PORT directly instead", raw)
	}
	var port string
	for p := range ports {
		port = p
	}

	listen := strings.Join(addrs, ",")
	if currentListenRaw != "" && currentListenRaw != listen {
		return "", "", "", fmt.Errorf(
			"config: OVERCAST_LISTEN=%q and GATEWAY_LISTEN=%q disagree (GATEWAY_LISTEN's addresses are "+
				"%q) -- GATEWAY_LISTEN is accepted as a compatibility alias for OVERCAST_LISTEN, but they "+
				"must name the same addresses when both are set",
			currentListenRaw, raw, listen)
	}
	if port != "" && currentPortRaw != "" && currentPortRaw != port {
		return "", "", "", fmt.Errorf(
			"config: GATEWAY_LISTEN=%q names port %s, which conflicts with OVERCAST_PORT=%s -- "+
				"GATEWAY_LISTEN's port must match OVERCAST_PORT when both are set, or be omitted",
			raw, port, currentPortRaw)
	}

	return listen, port, "GATEWAY_LISTEN", nil
}

// LocalStackVolumeDir is where LocalStack's own published docker-compose.yml
// mounts its state volume ("${LOCALSTACK_VOLUME_DIR:-./volume}:/var/lib/localstack").
// Overcast keeps state under OVERCAST_DATA_DIR (/data in the image), so a
// compose file carried over unchanged mounts a volume Overcast never reads.
const LocalStackVolumeDir = "/var/lib/localstack"

// adoptLocalStackVolume reports the data directory to use when a
// LocalStack-shaped volume mount is the only one present, or "" to leave the
// resolution alone.
//
// The three conditions are all necessary, and together they make this
// invisible to everyone who is not migrating:
//
//   - dataDirEnvRaw is empty and imageBakedDataDir is not: nobody configured a
//     data directory at all, and we are running inside the Docker image, whose
//     baked OVERCAST_DATA_DIR=/data is a default rather than user intent (see
//     the carve-out in this file's package comment). A native run, or any run
//     with OVERCAST_DATA_DIR/DATA_DIR set, is untouched.
//   - the image's own /data is not itself a mount: someone who mounted a
//     volume the Overcast way has already said where state goes, and that
//     always wins.
//   - /var/lib/localstack IS a mount: an unmounted directory is just a
//     directory (the image creates it so a named volume inherits the right
//     ownership), and adopting it would write state into the container layer
//     for every volume-less run.
//
// The mountpoint test is the same one OVERCAST_STATE=auto uses to recognise a
// volume at /data — see isMountpoint — so the two decisions cannot disagree
// about what "a volume is mounted here" means.
func adoptLocalStackVolume(dataDirEnvRaw, imageBakedDataDir string) string {
	return adoptLocalStackVolumeWith(dataDirEnvRaw, imageBakedDataDir, isMountpoint)
}

// adoptLocalStackVolumeWith is adoptLocalStackVolume with the mountpoint test
// injected, so the decision is a pure function of its inputs and testable on
// any platform — isMountpoint is unconditionally false on native Windows (see
// mountpoint_windows.go), which would otherwise leave every positive case
// unexercised on the platform this repository is developed on.
func adoptLocalStackVolumeWith(dataDirEnvRaw, imageBakedDataDir string, mounted func(string) bool) string {
	if dataDirEnvRaw != "" || imageBakedDataDir == "" {
		return ""
	}
	if mounted(imageBakedDataDir) {
		return ""
	}
	if !mounted(LocalStackVolumeDir) {
		return ""
	}
	return LocalStackVolumeDir
}

// ignoredLocalStackVars are LocalStack-documented variables Overcast
// recognises but that have no effect: not silently missed, and not
// rejected, just inert. Presence is logged once at startup (see
// logLocalStackAliases in cmd/overcast/cmd_serve.go) so an operator sees
// their setting was seen rather than wonder why nothing changed.
var ignoredLocalStackVars = []string{"SERVICES", "LOCALSTACK_API_KEY", "LOCALSTACK_AUTH_TOKEN"}

// detectIgnoredLocalStackVars returns which of ignoredLocalStackVars are set
// (non-empty) in the current environment, in the table's declared order.
func detectIgnoredLocalStackVars() []string {
	var found []string
	for _, name := range ignoredLocalStackVars {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			found = append(found, name)
		}
	}
	return found
}

// IgnoredLocalStackReason returns a short explanation of why name (one of
// Config.IgnoredLocalStackVars) has no effect, for the startup log line.
func IgnoredLocalStackReason(name string) string {
	switch name {
	case "SERVICES":
		return "Overcast runs every service, always -- there is nothing to select"
	case "LOCALSTACK_API_KEY", "LOCALSTACK_AUTH_TOKEN":
		return "Overcast has no LocalStack Pro/auth-gated feature set to unlock"
	default:
		return "recognised but has no effect"
	}
}

// joinNonEmpty joins the non-empty strings in parts with ", ", so several
// alias sources that jointly contributed to one Config field (e.g.
// LOCALSTACK_HOST and HOSTNAME_EXTERNAL both confirming the same hostname)
// are all named in a single log line rather than only the last one checked.
func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ", ")
}
