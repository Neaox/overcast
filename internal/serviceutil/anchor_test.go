package serviceutil

// anchor_test.go — the sweep-domain identity outlives the store that holds
// it, because it is derived from where the data directory is rather than from
// a row inside it. Each derivation is driven through a supplied environment,
// so the container branches run on any developer machine.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/state"
)

// A mountinfo as a containerised Overcast sees it with /data on a named
// volume: the volume's directory under the daemon's data root is the root of
// that mount, and the container's own ID shows in the identity-file binds.
const volumeMountinfo = `1000 999 0:60 / / rw,relatime - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/l/abc,upperdir=/var/lib/docker/overlay2/xyz/diff
1001 1000 0:61 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
1002 1000 0:62 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k,mode=755
1003 1000 8:1 /var/lib/docker/volumes/app_overcast-data/_data /data rw,relatime - ext4 /dev/sda1 rw
1004 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/resolv.conf /etc/resolv.conf rw,relatime - ext4 /dev/sda1 rw
1005 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw
1006 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/hosts /etc/hosts rw,relatime - ext4 /dev/sda1 rw
`

// The same image on a different daemon-side volume (a second compose
// project), and the same container ID: the volume must decide, not the
// container.
const otherVolumeMountinfo = `1000 999 0:60 / / rw,relatime - overlay overlay rw
1003 1000 8:1 /var/lib/docker/volumes/other_overcast-data/_data /data rw,relatime - ext4 /dev/sda1 rw
1005 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw
`

// A bind mount from a Docker Desktop host, with a space in the host path as
// mountinfo escapes it.
const bindMountinfo = `1000 999 0:60 / / rw,relatime - overlay overlay rw
1003 1000 0:70 /run/desktop/mnt/host/f/dev/my\040project/.overcast /data rw,relatime - fakeowner /run/desktop/mnt/host/f rw
1005 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw
`

// No mount at /data at all: the store lives in the container's writable
// layer.
const ephemeralMountinfo = `1000 999 0:60 / / rw,relatime - overlay overlay rw
1005 1000 8:1 /var/lib/docker/containers/6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw
`

// The same, in a second container.
const otherEphemeralMountinfo = `1000 999 0:60 / / rw,relatime - overlay overlay rw
1005 1000 8:1 /var/lib/docker/containers/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/hostname /etc/hostname rw,relatime - ext4 /dev/sda1 rw
`

func containerEnv(mountinfo string) anchorEnvironment {
	return anchorEnvironment{goos: "linux", hostname: "6b73d961aaaa", inContainer: true, mountinfo: mountinfo}
}

func TestDeriveAnchor_volumeMountOutlivesTheContainerAndItsContents(t *testing.T) {
	// Given: an instance whose /data is a named volume.
	first := deriveAnchor("/data", containerEnv(volumeMountinfo))
	if first.ID == "" {
		t.Fatal("no anchor for a data directory on a named volume")
	}
	if len(first.ID) != anchorIDLength || strings.Contains(first.ID, "-") {
		t.Errorf("anchor %q is not %d hex characters", first.ID, anchorIDLength)
	}
	if !strings.HasPrefix(first.Source, "mount:/var/lib/docker/volumes/app_overcast-data/_data") {
		t.Errorf("anchor derived from %q, want the volume's daemon-side directory", first.Source)
	}

	// When: the container is recreated on the same volume, its contents wiped
	// or not — a new container ID, a new hostname, the same mount.
	recreated := containerEnv(strings.ReplaceAll(volumeMountinfo, "6b73d961aaaa", "0123456789ab"))
	recreated.hostname = "0123456789ab"

	// Then: the same anchor.
	if again := deriveAnchor("/data", recreated); again.ID != first.ID {
		t.Fatalf("anchor changed with the container: %q then %q", first.ID, again.ID)
	}
	// And: a different volume is a different anchor, even in the same
	// container.
	if other := deriveAnchor("/data", containerEnv(otherVolumeMountinfo)); other.ID == first.ID {
		t.Fatal("two volumes derived one anchor")
	}
}

func TestDeriveAnchor_subdirectoryOfAMountIsItsOwnAnchor(t *testing.T) {
	// Given: two instances sharing one volume under different directories.
	a := deriveAnchor("/data/one", containerEnv(volumeMountinfo))
	b := deriveAnchor("/data/two", containerEnv(volumeMountinfo))
	if a.ID == "" || b.ID == "" {
		t.Fatal("no anchor for a directory below a mount")
	}
	if a.ID == b.ID {
		t.Fatal("two directories on one volume derived one anchor")
	}
	if !strings.HasSuffix(a.Source, "/_data/one") {
		t.Errorf("anchor %q does not carry the path below the mount", a.Source)
	}
}

func TestDeriveAnchor_bindMountFromTheHost(t *testing.T) {
	a := deriveAnchor("/data", containerEnv(bindMountinfo))
	if a.ID == "" {
		t.Fatal("no anchor for a bind-mounted data directory")
	}
	// The escaped space is decoded, so the key is the host path as a human
	// would read it in the log.
	if want := "mount:/run/desktop/mnt/host/f/dev/my project/.overcast"; a.Source != want {
		t.Errorf("anchor derived from %q, want %q", a.Source, want)
	}
}

func TestDeriveAnchor_containerWithoutAMountIsAnchoredToTheContainer(t *testing.T) {
	// Given: /data in the container's own writable layer.
	a := deriveAnchor("/data", containerEnv(ephemeralMountinfo))
	if a.ID == "" {
		t.Fatal("no anchor for a data directory in the container's own filesystem")
	}
	if want := "container:6b73d961aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:/data"; a.Source != want {
		t.Errorf("anchor derived from %q, want %q", a.Source, want)
	}
	// Then: the identity is the container's, which is what the store's
	// lifetime is too — and another container is another domain.
	if other := deriveAnchor("/data", containerEnv(otherEphemeralMountinfo)); other.ID == a.ID {
		t.Fatal("two containers derived one anchor from the same in-container path")
	}
	// The hostname plays no part: `hostname:` in a compose file must not be
	// able to fold two containers into one domain.
	renamed := containerEnv(ephemeralMountinfo)
	renamed.hostname = "overcast"
	if again := deriveAnchor("/data", renamed); again.ID != a.ID {
		t.Fatal("the anchor of a container without a mount depends on its hostname")
	}
}

func TestDeriveAnchor_containerThatRevealsNothingHasNoAnchor(t *testing.T) {
	// A runtime that binds no identity files and mounts nothing durable gives
	// the derivation nothing to hold on to; InstanceIdentity then mints a
	// UUID as it always did.
	for name, mountinfo := range map[string]string{
		"no mountinfo":  "",
		"overlay only":  "1000 999 0:60 / / rw,relatime - overlay overlay rw\n",
		"tmpfs at data": "1000 999 0:60 / / rw - overlay overlay rw\n1003 1000 0:70 / /data rw - tmpfs tmpfs rw\n",
		"garbage":       "not a mountinfo line\n- - -\n",
	} {
		t.Run(name, func(t *testing.T) {
			if a := deriveAnchor("/data", containerEnv(mountinfo)); a.ID != "" {
				t.Fatalf("derived %q from an environment with no durable identity", a.Source)
			}
		})
	}
}

func TestDeriveAnchor_wholeFilesystemAtTheDataDirectory(t *testing.T) {
	// A device or a network share mounted whole at /data has root "/", so it
	// is named by what it is rather than by a subtree.
	mountinfo := "1000 999 0:60 / / rw - overlay overlay rw\n1003 1000 0:70 / /data rw - nfs4 nas:/export/overcast rw\n"
	a := deriveAnchor("/data", containerEnv(mountinfo))
	if want := "mount:nfs4:nas:/export/overcast"; a.Source != want {
		t.Errorf("anchor derived from %q, want %q", a.Source, want)
	}
}

func TestDeriveAnchor_nativeIsTheHostAndThePath(t *testing.T) {
	dir := t.TempDir()
	env := anchorEnvironment{goos: "linux", hostname: "laptop"}

	// Given/When: the same directory, spelled two ways.
	first := deriveAnchor(dir, env)
	again := deriveAnchor(filepath.Join(dir, "sub", ".."), env)

	// Then: one anchor, and it names the host and the directory.
	if first.ID == "" || first.ID != again.ID {
		t.Fatalf("one directory derived two anchors: %q and %q", first.Source, again.Source)
	}
	if !strings.HasPrefix(first.Source, "host:laptop:") {
		t.Errorf("anchor derived from %q, want the host name first", first.Source)
	}
	// And: another directory on the same host, or the same directory on
	// another host sharing the daemon, is another domain.
	if other := deriveAnchor(t.TempDir(), env); other.ID == first.ID {
		t.Fatal("two directories derived one anchor")
	}
	elsewhere := env
	elsewhere.hostname = "ci-runner-2"
	if other := deriveAnchor(dir, elsewhere); other.ID == first.ID {
		t.Fatal("two hosts derived one anchor for the same path")
	}
}

func TestDeriveAnchor_windowsPathsFoldCase(t *testing.T) {
	// Windows filesystems are case-insensitive, so two spellings are one
	// directory and must be one domain. Absolute paths are used so the
	// derivation does not depend on the working directory.
	env := anchorEnvironment{goos: "windows", hostname: "desk"}
	dir := t.TempDir()
	a := deriveAnchor(dir, env)
	b := deriveAnchor(strings.ToUpper(dir), env)
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("case-folded spellings derived two anchors: %q and %q", a.Source, b.Source)
	}
}

func TestDeriveAnchor_emptyDataDirHasNone(t *testing.T) {
	if a := deriveAnchor("", anchorEnvironment{goos: "linux", hostname: "laptop"}); a.ID != "" {
		t.Fatalf("derived %q with no data directory", a.Source)
	}
}

func TestParseMountinfo_skipsLinesItCannotRead(t *testing.T) {
	entries := parseMountinfo("short line\n" + volumeMountinfo + "\n\n")
	if len(entries) != 7 {
		t.Fatalf("parsed %d entries, want 7", len(entries))
	}
	data := entries[3]
	if data.mountPoint != "/data" || data.fsType != "ext4" || data.source != "/dev/sda1" {
		t.Errorf("parsed %+v for the /data line", data)
	}
	// Optional fields between the mount options and the separator are
	// skipped, not mistaken for the type.
	shared := parseMountinfo("1 0 8:1 /x /y rw shared:1 master:2 - xfs /dev/sdb1 rw\n")
	if len(shared) != 1 || shared[0].fsType != "xfs" || shared[0].source != "/dev/sdb1" {
		t.Errorf("parsed %+v for a line with optional fields", shared)
	}
}

// ─── The anchor through InstanceIdentity ─────────────────────────────────────

// A wiped store is the incident: the same data directory, an empty store. The
// identity must come back the same, or every network the previous
// incarnation created is stranded.
func TestInstanceIdentity_survivesTheStoreBeingWiped(t *testing.T) {
	ctx := context.Background()
	anchor := deriveAnchor(t.TempDir(), anchorEnvironment{goos: "linux", hostname: "laptop"})

	before := InstanceIdentity(ctx, state.NewMemoryStore(), testNS, anchor)
	after := InstanceIdentity(ctx, state.NewMemoryStore(), testNS, anchor)
	if before == "" || before != after {
		t.Fatalf("the identity did not survive the store: %q then %q", before, after)
	}
	if before != anchor.ID {
		t.Errorf("identity %q is not the anchor %q", before, anchor.ID)
	}
}

// A store from before anchors existed holds a minted UUID, and every resource
// it created is labelled with it. That identity keeps winning for as long as
// the store lives, or an upgrade would make an instance a stranger to its own
// running containers.
func TestInstanceIdentity_aRecordedIdentityOutranksTheAnchor(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemoryStore()
	if err := st.Set(ctx, testNS, InstanceKey, "legacy-uuid"); err != nil {
		t.Fatal(err)
	}
	anchor := deriveAnchor(t.TempDir(), anchorEnvironment{goos: "linux", hostname: "laptop"})

	if got := InstanceIdentity(ctx, st, testNS, anchor); got != "legacy-uuid" {
		t.Fatalf("identity = %q, want the one the store already holds", got)
	}
}

// The anchor is recorded, so a later start that reads the store gets the same
// answer without deriving anything.
func TestInstanceIdentity_recordsTheAnchor(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemoryStore()
	anchor := deriveAnchor(t.TempDir(), anchorEnvironment{goos: "linux", hostname: "laptop"})

	InstanceIdentity(ctx, st, testNS, anchor)
	stored, found, err := st.Get(ctx, testNS, InstanceKey)
	if err != nil || !found || stored != anchor.ID {
		t.Fatalf("stored %q (found=%v, err=%v), want the anchor %q", stored, found, err, anchor.ID)
	}
}

// A store that cannot be read yields no identity even with an anchor in hand:
// it may hold an older identity the resources are labelled with, and stamping
// the anchor meanwhile would split the domain.
func TestInstanceIdentity_anchorDoesNotStandInForAnUnreadableStore(t *testing.T) {
	anchor := deriveAnchor(t.TempDir(), anchorEnvironment{goos: "linux", hostname: "laptop"})
	if got := InstanceIdentity(context.Background(), nil, testNS, anchor); got != "" {
		t.Fatalf("identity = %q from a nil store, want none", got)
	}
}

// Without an anchor the identity is minted per store, as it always was.
func TestInstanceIdentity_withoutAnAnchorIsMintedPerStore(t *testing.T) {
	ctx := context.Background()
	a := InstanceIdentity(ctx, state.NewMemoryStore(), testNS, Anchor{})
	b := InstanceIdentity(ctx, state.NewMemoryStore(), testNS, Anchor{})
	if a == "" || a == b {
		t.Fatalf("two anchorless stores yielded %q and %q", a, b)
	}
}

const testNS = "test:instance"
