package serviceutil

// anchor.go derives the durable half of a sweep-domain identity: something
// about the data directory that outlives the store kept inside it.
//
// docker.LabelInstance says which Overcast instance created a Docker resource,
// and "instance" there means the data directory — instances that share one
// share records, and so share a sweep domain. InstanceIdentity used to make
// that concrete as a UUID stored in a row of the very store the directory
// holds. Wipe the directory and the next start minted a new UUID, so every
// network and container the previous incarnation created now carried a label
// matching nothing: not adopted, and — because absence of proof is not
// permission — never removed. Each wipe stranded another VPC's /16 on the
// daemon, until a redeploy failed with "Pool overlaps with other one on this
// address space".
//
// The anchor is the fix: an identity derived from *where the directory is*
// rather than from what it contains, so clearing the contents changes nothing
// and two directories still differ. It is a hash of one of three keys, tried
// in this order:
//
//   - Containerised, with the directory on a mount: the mount's origin on the
//     daemon side — the volume's or bind's path as /proc/self/mountinfo
//     reports it. This is the one that matters for `docker compose down -v`
//     and `rm -rf` alike: the volume comes back at the same path, the bind is
//     the same host directory, and either way the label the old networks
//     carry is the label the new store derives. The path *inside* the
//     container is deliberately not enough: every containerised Overcast
//     mounts /data, and a key made from that alone would make every one of
//     them on a daemon a single sweep domain.
//   - Containerised, with the directory in the container's own filesystem:
//     the container ID (from the same mountinfo) plus the path. The store
//     lives and dies with the container, and so does the identity — no worse
//     than the UUID it replaces, and honest about it.
//   - Native: the host name plus the absolute path. The daemon such a process
//     talks to is almost always its own, but one shared over DOCKER_HOST by
//     several hosts with the same layout must not fold them into one domain.
//
// Nothing here talks to Docker. The derivation is a pure function of the
// environment, so it cannot fail because the daemon is not up yet, and cannot
// disagree with itself between the sweep that runs on startup and the create
// that stamps a label later. Where the environment gives no key at all the
// anchor is empty and InstanceIdentity falls back to the stored UUID, exactly
// as before.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/overcast-sh/overcast/internal/containerendpoint"
)

// Anchor is the durable identity of a data directory, or the zero value when
// the environment offers none.
type Anchor struct {
	// ID is what InstanceIdentity stamps and compares: the first
	// anchorIDLength hex characters of a SHA-256 over Source. Hashed rather
	// than raw so that a host path never lands in a Docker label, and so the
	// label is the same shape whichever key produced it.
	ID string
	// Source is the key ID was hashed from, for the startup log: it says
	// which of the three derivations applied and to what, which is the first
	// thing to check when two instances turn out to share a domain or one
	// fails to recognise its own resources.
	Source string
}

// anchorIDLength is how much of the hash the label carries. 128 bits is
// collision-proof for this purpose and keeps the value shorter than the UUID
// it stands beside; the two are told apart by the UUID's dashes.
const anchorIDLength = 32

// mountinfoPath is where Linux describes the mounts of the calling process,
// one per line, with the field the derivation needs and /proc/mounts lacks:
// the mount's root within the filesystem that backs it.
const mountinfoPath = "/proc/self/mountinfo"

// DataDirAnchor derives the anchor for dataDir from this process's
// environment. An empty dataDir has no anchor.
func DataDirAnchor(dataDir string) Anchor {
	env := anchorEnvironment{goos: runtime.GOOS, inContainer: containerendpoint.RunningInContainer()}
	if name, err := os.Hostname(); err == nil {
		env.hostname = name
	}
	if env.inContainer {
		if text, err := os.ReadFile(mountinfoPath); err == nil {
			env.mountinfo = string(text)
		}
	}
	return deriveAnchor(dataDir, env)
}

// anchorEnvironment is everything DataDirAnchor reads from the process's
// surroundings, gathered so the derivation itself is a pure function a test
// can drive through every branch without a container to run in.
type anchorEnvironment struct {
	goos        string
	hostname    string
	inContainer bool
	// mountinfo is the text of /proc/self/mountinfo, or "" where it cannot be
	// read — a non-Linux container runtime, or a native process, which never
	// needs it.
	mountinfo string
}

// deriveAnchor is DataDirAnchor with its environment supplied.
func deriveAnchor(dataDir string, env anchorEnvironment) Anchor {
	if dataDir == "" {
		return Anchor{}
	}
	if env.inContainer {
		// Containers are Linux, and mountinfo speaks POSIX paths, so the
		// directory is read the same way whatever host the derivation is
		// tested on. A relative path is left to the native rule below: a
		// container's data directory is always absolute (/data by default).
		if path.IsAbs(dataDir) {
			return containerAnchor(path.Clean(dataDir), env.mountinfo)
		}
	}
	abs := canonicalDataDir(dataDir, env.goos)
	if abs == "" {
		return Anchor{}
	}
	if env.inContainer {
		return containerAnchor(abs, env.mountinfo)
	}
	return anchorFrom("host:" + env.hostname + ":" + abs)
}

// canonicalDataDir makes two spellings of one directory produce one key:
// absolute, cleaned, symlinks resolved where the directory already exists,
// and case-folded on Windows, whose filesystems are case-insensitive.
func canonicalDataDir(dataDir, goos string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return ""
	}
	// A directory that does not exist yet — the first start ever — cannot
	// have its links resolved; it is anchored on its name, which is what it
	// will be created under.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	abs = filepath.Clean(abs)
	if goos == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

// containerAnchor is the containerised derivation: the mount behind path
// where there is a durable one, else the container itself.
func containerAnchor(path, mountinfo string) Anchor {
	mounts := parseMountinfo(mountinfo)
	if m, rel, ok := mountFor(mounts, path); ok {
		if key, durable := m.durableKey(); durable {
			return anchorFrom("mount:" + key + rel)
		}
	}
	if id := containerIDFrom(mounts); id != "" {
		return anchorFrom("container:" + id + ":" + path)
	}
	return Anchor{}
}

// anchorFrom hashes a key into an Anchor.
func anchorFrom(key string) Anchor {
	sum := sha256.Sum256([]byte(key))
	return Anchor{ID: hex.EncodeToString(sum[:])[:anchorIDLength], Source: key}
}

// ─── /proc/self/mountinfo ────────────────────────────────────────────────────

// mountEntry is one line of mountinfo, reduced to what the derivation reads.
type mountEntry struct {
	// mountPoint is where the mount appears in this process's view.
	mountPoint string
	// root is the path within the backing filesystem that is mounted here.
	// For a Docker volume that is the volume's directory under the daemon's
	// data root; for a bind mount it is the host directory; for a filesystem
	// mounted whole — the container's own overlay root, a tmpfs, a device —
	// it is "/".
	root string
	// fsType and source name the backing filesystem and where it came from.
	fsType string
	source string
}

// parseMountinfo reads the fields the derivation needs from mountinfo text.
// The format is documented in proc(5): mount ID, parent ID, major:minor,
// root, mount point, options, zero or more optional fields, a lone "-", the
// filesystem type, the source, and the super options. Lines that do not fit
// are skipped rather than failing the whole read, since one exotic mount
// must not cost the instance its identity.
func parseMountinfo(text string) []mountEntry {
	var entries []mountEntry
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		entries = append(entries, mountEntry{
			mountPoint: unescapeMountinfo(fields[4]),
			root:       unescapeMountinfo(fields[3]),
			fsType:     fields[sep+1],
			source:     unescapeMountinfo(fields[sep+2]),
		})
	}
	return entries
}

// unescapeMountinfo undoes the octal escapes mountinfo uses for the
// characters that would otherwise break its whitespace-separated fields —
// "\040" for a space, most commonly, in a bind from a host path with one.
func unescapeMountinfo(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && isOctal(s[i+1]) && isOctal(s[i+2]) && isOctal(s[i+3]) {
			b.WriteByte((s[i+1]-'0')<<6 | (s[i+2]-'0')<<3 | (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }

// mountFor finds the mount that holds path — the one with the longest mount
// point that is path or a parent of it — and the remainder of path below it.
func mountFor(mounts []mountEntry, path string) (mount mountEntry, rel string, ok bool) {
	best := -1
	for _, m := range mounts {
		mp := strings.TrimSuffix(m.mountPoint, "/")
		switch {
		case mp == "":
			// The root mount: holds everything, and loses to any deeper one.
			if best < 0 {
				mount, rel, ok, best = m, path, true, 0
			}
		case path == mp || strings.HasPrefix(path, mp+"/"):
			if len(mp) > best {
				mount, rel, ok, best = m, path[len(mp):], true, len(mp)
			}
		}
	}
	return mount, rel, ok
}

// ephemeralFilesystems are the types a mount of root "/" can have and still
// name nothing that outlives the container: the container's own overlay, a
// tmpfs, and the kernel's pseudo-filesystems. A Docker volume or bind mount
// never has root "/" — it is a subtree of the daemon's or the host's
// filesystem — so this list only matters for whole filesystems mounted at
// the data directory.
var ephemeralFilesystems = map[string]bool{
	"overlay": true, "tmpfs": true, "ramfs": true, "devtmpfs": true, "proc": true,
	"sysfs": true, "cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true,
	"nsfs": true, "fusectl": true, "debugfs": true, "securityfs": true, "shm": true,
}

// durableKey names what backs the mount, if that is something that survives
// the container being recreated. A subtree of a larger filesystem — every
// volume, every bind — is named by the subtree: the same volume name or host
// directory reproduces it. A whole filesystem is named by type and source,
// unless the type says it is ephemeral.
func (m mountEntry) durableKey() (string, bool) {
	if m.root != "/" && m.root != "" {
		return m.root, true
	}
	if ephemeralFilesystems[m.fsType] {
		return "", false
	}
	return m.fsType + ":" + m.source, true
}

// containerIDPattern matches the 64-hex container ID Docker and Podman put in
// the path of the per-container files they bind into a container.
var containerIDPattern = regexp.MustCompile(`(?:^|/)([0-9a-f]{64})(?:/|$)`)

// containerIdentityFiles are the files a container runtime bind-mounts from a
// per-container directory on the daemon side, which is how the container's
// own ID can be read without asking the daemon or trusting the hostname —
// which `hostname:` in a compose file overrides.
var containerIdentityFiles = []string{"/etc/hostname", "/etc/hosts", "/etc/resolv.conf"}

// containerIDFrom reads the container's ID off the bind mounts of its
// identity files, or "" when none of them reveals one.
func containerIDFrom(mounts []mountEntry) string {
	for _, m := range mounts {
		for _, file := range containerIdentityFiles {
			if m.mountPoint != file {
				continue
			}
			if match := containerIDPattern.FindStringSubmatch(m.root); match != nil {
				return match[1]
			}
		}
	}
	return ""
}
