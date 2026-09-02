package lambda

// init_volume_test.go — the init arrives in a seeded volume, not in every
// container's provisioning archive.

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// initVolumeMountOf returns the read-only mount that delivers the init, if the
// create request carries one.
func initVolumeMountOf(create docker.CreateContainerRequest) (docker.Mount, bool) {
	if create.HostConfig == nil {
		return docker.Mount{}, false
	}
	for _, m := range create.HostConfig.Mounts {
		if m.Target == initVolumeTarget {
			return m, true
		}
	}
	return docker.Mount{}, false
}

// archiveHasInit reports whether a provisioning archive carries the init.
func archiveHasInit(t *testing.T, archive []byte) bool {
	t.Helper()
	want := strings.TrimPrefix(initproto.InitPath, "/")
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("read provisioning archive: %v", err)
		}
		if hdr.Name == want {
			return true
		}
	}
}

// The ordinary path: the volume is seeded once, the function container mounts
// it read-only where the entrypoint expects the init, and the provisioning
// archive carries nothing of the init at all.
func TestInitVolume_containerMountsTheSeededVolumeInsteadOfCopyingTheInit(t *testing.T) {
	daemon := newRecordingDaemon(t)
	create := acquireAgainstDaemon(t, daemon, zipFunction(t))

	mount, ok := initVolumeMountOf(create)
	if !ok {
		t.Fatalf("the container has no mount at %s: %+v", initVolumeTarget, create.HostConfig)
	}
	if mount.Type != "volume" {
		t.Errorf("init mount type = %q, want volume", mount.Type)
	}
	if !mount.ReadOnly {
		t.Error("the init volume is mounted writable; a function must not be able to rewrite its own init")
	}
	if !strings.HasPrefix(mount.Source, "overcast-lambda-init-") {
		t.Errorf("init volume name = %q, want the overcast-lambda-init-<version>-<arch> shape", mount.Source)
	}
	if !strings.HasSuffix(mount.Source, "-amd64") {
		t.Errorf("init volume name = %q, want it to name the architecture", mount.Source)
	}

	// The volume exists, carries our labels, and holds the init.
	labels, held := daemon.volumeLabels(mount.Source)
	if !held {
		t.Fatalf("the daemon holds no volume named %q", mount.Source)
	}
	for key, want := range map[string]string{
		docker.LabelManaged:        "true",
		docker.LabelService:        "lambda",
		docker.LabelLambdaInitArch: "amd64",
	} {
		if labels[key] != want {
			t.Errorf("volume label %s = %q, want %q", key, labels[key], want)
		}
	}
	if labels[docker.LabelLambdaInitVersion] == "" {
		t.Errorf("the volume carries no %s label, so nothing can tell it from one Docker auto-created", docker.LabelLambdaInitVersion)
	}
	if !strings.Contains(mount.Source, labels[docker.LabelLambdaInitVersion]) {
		t.Errorf("the volume's name %q does not carry its version label %q", mount.Source, labels[docker.LabelLambdaInitVersion])
	}
	if labels[docker.LabelInstance] == "" {
		t.Error("the volume carries no owner label")
	}
	if labels[docker.LabelResourceID] == "" {
		t.Error("the volume carries no resource-id label — seedInitVolume should be using cr.instances.ManagedLabels, which always sets one")
	}

	seeded, ok := daemon.seededArchive()
	if !ok {
		t.Fatal("nothing was copied into the seeding container")
	}
	if !archiveHasInitNamed(t, seeded, "init") {
		t.Error("the archive copied into the volume does not hold the init")
	}
}

// archiveHasInitNamed reports whether an archive holds an executable entry of
// this name.
func archiveHasInitNamed(t *testing.T, archive []byte, name string) bool {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false
		}
		if err != nil {
			t.Fatalf("read seed archive: %v", err)
		}
		if hdr.Name == name {
			if hdr.Mode&0o111 == 0 {
				t.Errorf("the seeded init is not executable (mode %o)", hdr.Mode)
			}
			if hdr.Size == 0 {
				t.Error("the seeded init is empty")
			}
			return true
		}
	}
}

// Two cold starts racing must produce one seed, not two.
func TestInitVolume_isSeededOncePerProcess(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)

	for i := 0; i < 3; i++ {
		fn := zipFunction(t)
		if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
			t.Fatal("expected the fake daemon's start failure")
		}
	}

	daemon.mu.Lock()
	seeds := len(daemon.seeds)
	daemon.mu.Unlock()
	if seeds != 1 {
		t.Errorf("the init volume was seeded %d times across three cold starts, want 1", seeds)
	}
}

// A volume of our name that carries none of our labels is one Docker
// auto-created empty for a container that named it, which is what is left
// behind when a user prunes ours mid-cold-start. Seeding into it would leave
// every later container without an entrypoint, so it is removed and re-seeded.
func TestInitVolume_unlabelledVolumeOfOurNameIsReplaced(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")
	daemon.seedVolume(name, nil) // as the daemon auto-creates it: no labels

	fn := zipFunction(t)
	if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}

	if removed := daemon.removedVolumes(); !slices.Contains(removed, name) {
		t.Errorf("the unlabelled volume was not removed; removals were %v", removed)
	}
	labels, held := daemon.volumeLabels(name)
	if !held {
		t.Fatal("the volume was not re-seeded")
	}
	if labels[docker.LabelLambdaInitVersion] == "" {
		t.Error("the re-seeded volume carries no version label")
	}
}

// An init volume from a previous build is removed when the current one is
// first seeded, so a long-lived daemon does not accumulate one per Overcast
// version — but only when this instance is the one that created it (see
// TestInitVolume_supersededVolumeOwnedByAnotherInstanceIsNotPruned): a stale
// volume carries no proof either way until it is labelled as this instance's
// own.
func TestInitVolume_supersededVolumesArePruned(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	domain := cr.instances.Resolve(context.Background())

	const stale = "overcast-lambda-init-deadbeef1234-amd64"
	daemon.seedVolume(stale, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: "deadbeef1234",
		docker.LabelLambdaInitArch:    "amd64",
		docker.LabelInstance:          domain,
	})
	// A Lambda volume that is not an init volume — nothing here may touch it.
	const unrelated = "overcast-lambda-something-else"
	daemon.seedVolume(unrelated, map[string]string{
		docker.LabelManaged: "true",
		docker.LabelService: "lambda",
	})

	fn := zipFunction(t)
	if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}

	held := daemon.volumeNames()
	if slices.Contains(held, stale) {
		t.Errorf("the superseded init volume survived: %v", held)
	}
	if !slices.Contains(held, unrelated) {
		t.Errorf("a Lambda volume that is not an init volume was removed: %v", held)
	}
}

// A stale init volume labelled for a different Overcast instance — or one
// that predates docker.LabelInstance entirely and so carries no owner label
// at all — is exactly the volume #1573 was filed about: this instance cannot
// prove it created it, so pruneStaleInitVolumes must leave it alone rather
// than guess. It is not swept, but it is reported (see InitVolumeProblems).
func TestInitVolume_supersededVolumeOwnedByAnotherInstanceIsNotPruned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
	}{
		{
			name: "foreign instance",
			labels: map[string]string{
				docker.LabelManaged:           "true",
				docker.LabelService:           "lambda",
				docker.LabelLambdaInitVersion: "deadbeef1234",
				docker.LabelLambdaInitArch:    "amd64",
				docker.LabelInstance:          "some-other-overcast",
			},
		},
		{
			name: "no owner label at all",
			labels: map[string]string{
				docker.LabelManaged:           "true",
				docker.LabelService:           "lambda",
				docker.LabelLambdaInitVersion: "deadbeef1234",
				docker.LabelLambdaInitArch:    "amd64",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			daemon := newRecordingDaemon(t)
			cr := newDaemonContainerRuntime(t, daemon.Server)

			const stale = "overcast-lambda-init-deadbeef1234-amd64"
			daemon.seedVolume(stale, tc.labels)

			fn := zipFunction(t)
			if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
				t.Fatal("expected the fake daemon's start failure")
			}

			if slices.Contains(daemon.removedVolumes(), stale) {
				t.Errorf("a stale init volume this instance cannot prove it created was removed")
			}
			if !slices.Contains(daemon.volumeNames(), stale) {
				t.Errorf("the stale volume is gone from the daemon")
			}
		})
	}
}

// seedInitVolume stamps the same instance identity every other Overcast
// resource carries, so a later prune (this process's own, or a future one
// reading the same store) can tell this volume apart from another instance's
// — see docker.LabelInstance and issue #1573.
func TestInitVolume_seededVolumeCarriesOwnerLabel(t *testing.T) {
	daemon := newRecordingDaemon(t)
	create := acquireAgainstDaemon(t, daemon, zipFunction(t))

	mount, ok := initVolumeMountOf(create)
	if !ok {
		t.Fatalf("the container has no mount at %s", initVolumeTarget)
	}
	labels, held := daemon.volumeLabels(mount.Source)
	if !held {
		t.Fatalf("the daemon holds no volume named %q", mount.Source)
	}
	if labels[docker.LabelInstance] == "" {
		t.Error("the seeded init volume carries no owner label")
	}
}

// Reuse is never gated on ownership: the init volume's own name already
// carries the content hash of what is inside it, so mounting a volume another
// instance seeded is exactly as safe as mounting one of this instance's
// own — see ensureInitVolume. What ownership gates is deletion, tested
// separately (TestInitVolume_supersededVolumeOwnedByAnotherInstanceIsNotPruned,
// TestInitVolume_forgetInitVolumeLeavesAVolumeItDoesNotOwn).
func TestInitVolume_reuseIgnoresOwnership(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")
	daemon.seedVolume(name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(name),
		docker.LabelLambdaInitArch:    "amd64",
		docker.LabelInstance:          "some-other-overcast",
	})

	fn := zipFunction(t)
	if _, err := cr.acquireContainer(context.Background(), fn, func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}

	if _, seeded := daemon.seededArchive(); seeded {
		t.Error("a volume seeded by another instance was reseeded instead of reused")
	}

	problems := cr.InitVolumeProblems()
	if len(problems) != 1 || problems[0].Volume != name || problems[0].Owner != "some-other-overcast" {
		t.Errorf("InitVolumeProblems() = %+v, want one entry for %q owned by \"some-other-overcast\"", problems, name)
	}
}

// forgetInitVolume is called after a container fails to start because the
// init was not where its entrypoint expects it — but the volume that
// happened to be mounted may have been reused from another instance rather
// than seeded by this process (see TestInitVolume_reuseIgnoresOwnership), and
// a start failure this instance observed is not proof that volume is broken
// for its actual owner too. Removing it anyway would be exactly the
// cross-instance deletion issue #1573 is about.
func TestInitVolume_forgetInitVolumeLeavesAVolumeItDoesNotOwn(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)

	const name = "overcast-lambda-init-deadbeef1234-amd64"
	daemon.seedVolume(name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: "deadbeef1234",
		docker.LabelLambdaInitArch:    "amd64",
		docker.LabelInstance:          "some-other-overcast",
	})

	cr.forgetInitVolume(context.Background(), name)

	if slices.Contains(daemon.removedVolumes(), name) {
		t.Error("forgetInitVolume removed a volume owned by another instance")
	}
	problems := cr.InitVolumeProblems()
	if len(problems) != 1 || problems[0].Volume != name {
		t.Errorf("InitVolumeProblems() = %+v, want one entry for %q", problems, name)
	}
	// Not being allowed to delete it also means not being allowed to reuse
	// it again — otherwise the next cold start mounts the same broken volume
	// and fails the same way forever. See TestInitVolume_emptyForeignVolumeFallsBackAfterOneFailure.
	if _, unusable := cr.initVolumeUnusable.Load(name); !unusable {
		t.Error("a volume this instance could not delete was not marked unusable for future reuse")
	}
}

// The state a process killed between creating the init volume and filling it
// leaves behind — labelled, empty, and indistinguishable from a good seed by
// inspecting it alone — is exactly what ensureInitVolume's reuse branch would
// otherwise hand straight to a container. When the failing cold start does
// not own the volume it reused, it cannot fix or delete it (see
// TestInitVolume_forgetInitVolumeLeavesAVolumeItDoesNotOwn), so the *next*
// cold start must not try the same volume again: it falls back to copying
// the init into the provisioning archive instead of failing forever.
func TestInitVolume_emptyForeignVolumeFallsBackAfterOneFailure(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.startError = "OCI runtime create failed: exec: \"" + initproto.InitPath + "\": stat " + initproto.InitPath + ": no such file or directory"
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")
	daemon.seedVolume(name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(name),
		docker.LabelLambdaInitArch:    "amd64",
		docker.LabelInstance:          "some-other-overcast",
	})

	// First cold start reuses the foreign, empty volume, fails to start, and
	// cannot delete a volume it did not create.
	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	if slices.Contains(daemon.removedVolumes(), name) {
		t.Fatal("a foreign volume was removed")
	}

	// Second cold start must not mount the same broken volume again.
	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	creates := daemon.recordedCreates()
	if len(creates) != 2 {
		t.Fatalf("expected 2 container creates across two cold starts, got %d", len(creates))
	}
	second := creates[1]
	if _, mounted := initVolumeMountOf(second); mounted {
		t.Error("the second cold start mounted the same volume already found broken, instead of falling back")
	}
	if got := second.ContainerConfig.Entrypoint; !slices.Equal(got, []string{initproto.InitPath}) {
		t.Errorf("entrypoint = %v, want the init even on the fallback path", got)
	}
}

// The same as TestInitVolume_emptyForeignVolumeFallsBackAfterOneFailure, but
// for a volume carrying no owner label at all rather than a different
// instance's — a pre-fix build's leftover, or one whose label was somehow
// lost. "Absence is not permission" applies the same way either side of that
// distinction: this instance still cannot prove it may delete it.
func TestInitVolume_emptyUnlabelledVolumeFallsBackAfterOneFailure(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.startError = "OCI runtime create failed: exec: \"" + initproto.InitPath + "\": stat " + initproto.InitPath + ": no such file or directory"
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")
	daemon.seedVolume(name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(name),
		docker.LabelLambdaInitArch:    "amd64",
		// No docker.LabelInstance — predates ownership labelling.
	})

	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	if slices.Contains(daemon.removedVolumes(), name) {
		t.Fatal("an unlabelled volume was removed")
	}

	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	creates := daemon.recordedCreates()
	if len(creates) != 2 {
		t.Fatalf("expected 2 container creates across two cold starts, got %d", len(creates))
	}
	if _, mounted := initVolumeMountOf(creates[1]); mounted {
		t.Error("the second cold start mounted the same volume already found broken, instead of falling back")
	}
}

// The counterpart of the two tests above: when the empty volume this
// instance reuses turns out to be its own — labelled with its own identity,
// e.g. left behind by this same durable-identity instance crashing mid-seed
// on a previous run — forgetInitVolume both can and does remove it, and,
// unlike the foreign/unlabelled cases, the very next cold start re-seeds a
// fresh copy rather than falling back to the archive: deleting a volume this
// instance owns is not a reason to stop trusting that name.
func TestInitVolume_ownPartialSeedIsDeletedAndReseeded(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.startError = "OCI runtime create failed: exec: \"" + initproto.InitPath + "\": stat " + initproto.InitPath + ": no such file or directory"
	cr := newDaemonContainerRuntime(t, daemon.Server)
	domain := cr.instances.Resolve(context.Background())

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")
	daemon.seedVolume(name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(name),
		docker.LabelLambdaInitArch:    "amd64",
		docker.LabelInstance:          domain,
	})

	// First cold start reuses the empty volume it already owns, fails, and
	// deletes it.
	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	if !slices.Contains(daemon.removedVolumes(), name) {
		t.Fatal("an empty volume this instance owns was not removed")
	}

	// Second cold start must re-seed a fresh copy rather than give up on the
	// name: nothing marks it unusable when this instance was the one that
	// deleted it. It also fails to start (the fake daemon always fails
	// start), and — being freshly seeded and so owned again — that copy is
	// removed too at the end of this call; the point under test is that it
	// gets a real, mounted reseed at all rather than silently falling back.
	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}
	daemon.mu.Lock()
	seeds := len(daemon.seeds)
	daemon.mu.Unlock()
	if seeds != 1 {
		t.Errorf("expected exactly 1 reseed (the second cold start's; the first reused the pre-seeded volume), got %d", seeds)
	}
	creates := daemon.recordedCreates()
	if len(creates) != 2 {
		t.Fatalf("expected 2 container creates, got %d", len(creates))
	}
	if _, mounted := initVolumeMountOf(creates[1]); !mounted {
		t.Error("the second cold start fell back to the archive instead of reseeding the volume")
	}
}

// pruneStaleInitVolumes runs once per architecture, not once per process:
// seeding an amd64 init volume must never prune the current arm64 volume (or
// vice versa), because the two are simultaneously-current, differently-named
// volumes — initVolumeName hashes a different init binary per architecture —
// not a build and its predecessor.
func TestInitVolume_pruneIsScopedPerArchitecture(t *testing.T) {
	daemon := newRecordingDaemon(t)
	cr := newDaemonContainerRuntime(t, daemon.Server)
	domain := cr.instances.Resolve(context.Background())

	arm64Binary, err := lambdaInitBinary("arm64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	arm64Name := initVolumeName(arm64Binary, "arm64")
	// The current arm64 volume, as if an earlier cold start in this same
	// process had already seeded it — own-labelled, so nothing but the
	// architecture filter stops the amd64 seed below from pruning it.
	daemon.seedVolume(arm64Name, map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(arm64Name),
		docker.LabelLambdaInitArch:    "arm64",
		docker.LabelInstance:          domain,
	})

	// zipFunction targets x86_64/amd64; seeding it triggers
	// pruneStaleInitVolumes scoped to "amd64" only.
	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the fake daemon's start failure")
	}

	if !slices.Contains(daemon.volumeNames(), arm64Name) {
		t.Error("seeding the amd64 init volume pruned the current arm64 volume")
	}
}

// A daemon that will not manage volumes still runs functions: the init goes
// back into the provisioning archive, which is the only reason that path still
// exists.
func TestInitVolume_daemonWithoutVolumesFallsBackToTheArchive(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.refuseVolumes = true
	create := acquireAgainstDaemon(t, daemon, zipFunction(t))

	if _, ok := initVolumeMountOf(create); ok {
		t.Error("the container mounts an init volume the daemon refused to create")
	}
	if got := create.ContainerConfig.Entrypoint; !slices.Equal(got, []string{initproto.InitPath}) {
		t.Errorf("entrypoint = %v, want the init even on the fallback path", got)
	}
	archive, ok := daemon.archives["overcast-lambda-zip-fn"]
	if !ok {
		// The container name carries a timestamp; find it by prefix.
		daemon.mu.Lock()
		for name, body := range daemon.archives {
			if strings.HasPrefix(name, "overcast-lambda-zip-fn-") {
				archive, ok = body, true
			}
		}
		daemon.mu.Unlock()
	}
	if !ok {
		t.Fatal("no provisioning archive was copied into the container")
	}
	if !archiveHasInit(t, archive) {
		t.Error("the provisioning archive does not carry the init, and no volume does either")
	}
}

// The volume is addressed by what is in it, so an Overcast built with a
// different init can never read a stale one.
func TestInitVolumeName_isDerivedFromTheInitsContent(t *testing.T) {
	a := initVolumeName([]byte("one init"), "amd64")
	b := initVolumeName([]byte("another init"), "amd64")
	if a == b {
		t.Fatal("two different inits share a volume name")
	}
	if again := initVolumeName([]byte("one init"), "amd64"); again != a {
		t.Fatalf("the same init produced two names: %q and %q", a, again)
	}
	if arm := initVolumeName([]byte("one init"), "arm64"); arm == a {
		t.Fatal("the two architectures share a volume name")
	}
	if got := initVolumeVersion(a); got == "" || !strings.Contains(a, got) {
		t.Errorf("initVolumeVersion(%q) = %q", a, got)
	}
}

// The mount goes alongside a function's EFS mounts, not instead of them.
func TestWithInitVolume_keepsTheMountsItWasGiven(t *testing.T) {
	efs := []docker.Mount{{Type: "volume", Source: "fs-1", Target: "/mnt/data"}}
	got := withInitVolume(efs, "overcast-lambda-init-abc-amd64")
	if len(got) != 2 || got[0].Target != "/mnt/data" || got[1].Target != initVolumeTarget {
		t.Fatalf("mounts = %+v, want the EFS mount and the init volume", got)
	}
	if unchanged := withInitVolume(efs, ""); len(unchanged) != 1 {
		t.Fatalf("mounts = %+v with no init volume, want only the EFS mount", unchanged)
	}
}

// An empty init volume — what a process killed mid-seed leaves behind, and the
// one state the labels cannot tell from a good seed — is self-healing: the
// container will not start, Docker names the path it could not exec, and the
// volume is dropped so the next cold start re-seeds it.
func TestInitVolume_anEmptyVolumeIsDroppedWhenTheContainerCannotStart(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.startError = "OCI runtime create failed: exec: \"" + initproto.InitPath + "\": stat " + initproto.InitPath + ": no such file or directory"
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")

	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the start failure")
	}
	if removed := daemon.removedVolumes(); !slices.Contains(removed, name) {
		t.Errorf("the init volume survived a start that could not find the init; removals were %v", removed)
	}
	if _, held := cr.initVolumes.Load(name); held {
		t.Error("the seeding state was kept, so the next cold start will not re-seed")
	}
}

// A start that failed for its own reasons has a perfectly good volume, and
// throwing it away would cost every such failure a re-seed.
func TestInitVolume_anUnrelatedStartFailureKeepsTheVolume(t *testing.T) {
	daemon := newRecordingDaemon(t)
	daemon.startError = "driver failed programming external connectivity: port is already allocated"
	cr := newDaemonContainerRuntime(t, daemon.Server)

	binary, err := lambdaInitBinary("x86_64")
	if err != nil {
		t.Fatalf("init binary: %v", err)
	}
	name := initVolumeName(binary, "amd64")

	if _, err := cr.acquireContainer(context.Background(), zipFunction(t), func(string) {}, initTypeOnDemand, false); err == nil {
		t.Fatal("expected the start failure")
	}
	if removed := daemon.removedVolumes(); slices.Contains(removed, name) {
		t.Errorf("an unrelated start failure removed the init volume; removals were %v", removed)
	}
	if !slices.Contains(daemon.volumeNames(), name) {
		t.Error("the init volume is gone after an unrelated start failure")
	}
}
