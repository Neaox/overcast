package lambda

// init_volume.go — getting the in-container init into a Lambda container
// without paying for it on every cold start.
//
// The init is the container's entrypoint, so every execution environment needs
// it at initproto.InitPath before it starts. The obvious way to put it there is
// the provisioning archive that already carries the function's code, and that
// is what this replaces, because it is not cheap: the artefact is ~6.5 MB and
// the daemon extracts a provisioning archive at roughly 20 MB/s, so the copy
// measured 333 ms p50 against a 341 ms cold start — the init doubled it.
//
// Instead the init lives in a named volume, seeded once per Overcast process
// per architecture, and every function container mounts it read-only at
// /var/overcast. A cold start then pays one volume inspect (a daemon round
// trip, under a millisecond) instead of 333 ms of copying, and the archive goes
// back to carrying only what is genuinely per-function.
//
// The archive path is kept as the fallback for a daemon that will not give us a
// volume — see initDelivery.

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

// initVolumeTarget is where the init volume is mounted in a function
// container: the directory initproto.InitPath names, so that mounting the
// volume is exactly equivalent to having copied the binary in.
var initVolumeTarget = path.Dir(initproto.InitPath)

// initSeedTimeout bounds the whole seeding sequence — create the volume, create
// a container to mount it, copy the binary in, remove the container. It runs on
// a cold start, once per process and architecture, and a daemon that cannot do
// it in this long is one the archive fallback should take over from.
const initSeedTimeout = 60 * time.Second

// initDelivery is how one cold start will get the init into its container.
type initDelivery struct {
	// volume is the name of the seeded volume to mount read-only at
	// initVolumeTarget. Empty when the archive fallback is in use.
	volume string
	// binary is the init to write into the provisioning archive. Nil when the
	// volume is in use.
	binary []byte
}

// initVolumeState is one architecture's seeding attempt, so that two cold
// starts racing never both seed.
type initVolumeState struct {
	once sync.Once
	err  error
}

// resolveInitDelivery decides how this cold start gets its init, preferring the
// volume and falling back to the archive.
//
// A missing init artefact is fatal either way — that error names the file and
// the command that builds it, and a container without an init is one whose
// output cannot be attributed to an invocation, which is the failure this whole
// mechanism exists to remove. A *daemon* that will not give us a volume is not
// fatal: the archive still works, it is only slower, so it costs one warning
// and the cold start proceeds.
func (cr *ContainerRuntime) resolveInitDelivery(ctx context.Context, fn *Function, resolvedRef, platform string) (initDelivery, error) {
	arch := ""
	if len(fn.Architectures) > 0 {
		arch = fn.Architectures[0]
	}
	binary, err := lambdaInitBinary(arch)
	if err != nil {
		return initDelivery{}, err
	}

	volume, verr := cr.ensureInitVolume(ctx, binary, arch, resolvedRef, platform)
	if verr == nil {
		return initDelivery{volume: volume}, nil
	}
	// One line, not one per cold start: the warn-once is keyed on the volume so
	// a daemon that never manages it does not fill the log.
	if _, warned := cr.initVolumeWarned.LoadOrStore(volume, struct{}{}); !warned {
		cr.logger.Warn("lambda: could not prepare the init volume; falling back to copying the init into every container, which costs roughly 300 ms per cold start",
			zap.String("volume", volume),
			zap.String("function", fn.Name),
			zap.Error(verr),
		)
	}
	return initDelivery{binary: binary}, nil
}

// initVolumeName addresses a volume by what is in it: the init's own content
// hash and the architecture it was built for. An Overcast built with a
// different init therefore reads a different volume and can never run against a
// stale one, and two Overcasts sharing a daemon share the volume when — and
// only when — they would have copied identical bytes.
func initVolumeName(binary []byte, goarch string) string {
	sum := sha256.Sum256(binary)
	return "overcast-lambda-init-" + hex.EncodeToString(sum[:])[:12] + "-" + goarch
}

// ensureInitVolume returns the name of a volume holding this init, seeding it
// if the daemon does not already have one.
//
// The daemon is asked on every cold start rather than a "verified" flag being
// cached, deliberately. The inspect is one round trip against the 333 ms the
// volume saves, so the saving is not what a cache would protect; what a cache
// would cost is correctness, because a volume can be removed out from under us
// at any moment by `docker volume prune`, and a cached flag would then hand
// every later cold start a container with no entrypoint — silently, and until
// the process restarted. Asking each time makes that self-healing.
//
// The seeding itself is not repeated: it runs once per process per
// architecture, behind a sync.Once, so two cold starts racing produce one seed.
func (cr *ContainerRuntime) ensureInitVolume(ctx context.Context, binary []byte, arch, resolvedRef, platform string) (string, error) {
	goarch := goarchForLambda(arch)
	name := initVolumeName(binary, goarch)

	vol, found, err := cr.docker.InspectVolume(ctx, name)
	if err != nil {
		return name, fmt.Errorf("inspect init volume: %w", err)
	}
	switch {
	case found && vol.Labels[docker.LabelLambdaInitVersion] != "":
		// Ours, and holding this exact init.
		return name, nil
	case found:
		// A volume of our name that we did not label. The daemon creates one
		// like this the moment a container names a volume that does not exist,
		// so this is what is left after a user prunes ours while a function
		// container is being created: same name, no contents. Seeding into it
		// would leave the init missing, so it goes.
		if rerr := cr.docker.RemoveVolume(ctx, name, true); rerr != nil {
			return name, fmt.Errorf("remove the unlabelled init volume so it can be re-seeded: %w", rerr)
		}
	}

	state, _ := cr.initVolumes.LoadOrStore(name, &initVolumeState{})
	seeding := state.(*initVolumeState)
	seeding.once.Do(func() {
		seedCtx, cancel := context.WithTimeout(ctx, initSeedTimeout)
		defer cancel()
		seeding.err = cr.seedInitVolume(seedCtx, name, binary, goarch, resolvedRef, platform)
	})
	if seeding.err != nil {
		return name, seeding.err
	}

	// A seed that succeeded earlier in this process but has since been pruned
	// leaves the sync.Once spent, so re-check and re-seed under a fresh state.
	if _, found, err := cr.docker.InspectVolume(ctx, name); err == nil && !found {
		cr.initVolumes.Delete(name)
		return cr.ensureInitVolume(ctx, binary, arch, resolvedRef, platform)
	}
	return name, nil
}

// seedInitVolume creates the volume and writes the init into it.
//
// Docker will extract an archive into a volume mounted over the destination
// path of a container that has been *created* and never started, which is what
// makes this cheap: no container start, no runtime, just create, copy, remove.
// (Pinned against a real daemon by TestInvoke_initIsMountedFromASeededVolume
// in tests/integration/lambda, because it is the assumption the whole
// mechanism rests on and it is not in any Docker API guarantee.)
func (cr *ContainerRuntime) seedInitVolume(ctx context.Context, name string, binary []byte, goarch, resolvedRef, platform string) error {
	labels := map[string]string{
		docker.LabelManaged:           "true",
		docker.LabelService:           "lambda",
		docker.LabelLambdaInitVersion: initVolumeVersion(name),
		docker.LabelLambdaInitArch:    goarch,
	}
	if err := cr.docker.CreateVolumeWithOptions(ctx, name, docker.VolumeOptions{Labels: labels}); err != nil {
		return fmt.Errorf("create init volume: %w", err)
	}

	// The function's own image, because it is the one image this cold start has
	// already established is present — pulling anything else here would be a
	// second registry round trip on the critical path.
	seederName := fmt.Sprintf("%s-seed-%d", name, cr.clk.Now().UnixNano())
	seeder, err := cr.docker.CreateContainer(ctx, seederName, &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:  resolvedRef,
			Labels: cr.instances.ManagedLabels(ctx, "lambda", "init-volume-seed"),
		},
		Platform: platform,
		HostConfig: &docker.HostConfig{
			Mounts: []docker.Mount{{Type: "volume", Source: name, Target: initVolumeTarget}},
		},
	})
	if err != nil {
		return fmt.Errorf("create the container that seeds the init volume: %w", err)
	}
	defer func() { _ = cr.docker.RemoveContainerForce(seeder) }()

	if err := cr.docker.CopyToContainer(ctx, seeder, initVolumeTarget, bytes.NewReader(initSeedTar(binary))); err != nil {
		return fmt.Errorf("copy the init into its volume: %w", err)
	}

	cr.pruneStaleInitVolumes(ctx, name)
	return nil
}

// initVolumeVersion is the content hash out of a volume name.
func initVolumeVersion(name string) string {
	rest := strings.TrimPrefix(name, "overcast-lambda-init-")
	version, _, _ := strings.Cut(rest, "-")
	return version
}

// initSeedTar is the archive extracted into the volume: the init alone, at the
// name initproto.InitPath ends in, executable.
func initSeedTar(binary []byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	// Entry names in a Put Archive body are relative to the destination path,
	// so this single entry becomes <initVolumeTarget>/init.
	_ = tw.WriteHeader(&tar.Header{Name: path.Base(initproto.InitPath), Mode: 0o755, Size: int64(len(binary))})
	_, _ = tw.Write(binary)
	_ = tw.Close()
	return buf.Bytes()
}

// pruneStaleInitVolumes removes init volumes this Overcast built for an older
// init, once per process, when the current one is first seeded.
//
// It is safe from every other service's sweep and every other service's sweep
// is safe from it. ListUnusedVolumes filters on LabelManaged plus
// LabelService, so ECS's task and orphan sweeps (internal/services/ecs,
// serviceName "ecs") and EFS's reconciliation (internal/services/efs,
// serviceName "efs") never see a volume labelled "lambda"; and this only
// removes volumes carrying LabelLambdaInitVersion, which nothing else stamps.
// `dangling=true` leaves anything a container still references alone, so a
// container kept by LAMBDA_KEEP_CONTAINERS keeps its init volume with it.
func (cr *ContainerRuntime) pruneStaleInitVolumes(ctx context.Context, keep string) {
	if !cr.initVolumePruned.CompareAndSwap(false, true) {
		return
	}
	volumes, err := cr.docker.ListUnusedVolumes(ctx, "lambda")
	if err != nil {
		cr.logger.Debug("lambda: could not list init volumes to prune", zap.Error(err))
		return
	}
	for _, v := range volumes {
		if v.Name == keep || v.Labels[docker.LabelLambdaInitVersion] == "" {
			continue
		}
		if err := cr.docker.RemoveVolume(ctx, v.Name, false); err != nil {
			cr.logger.Debug("lambda: could not remove a superseded init volume",
				zap.String("volume", v.Name), zap.Error(err))
			continue
		}
		cr.logger.Info("lambda: removed the init volume of a previous build",
			zap.String("volume", v.Name))
	}
}

// forgetInitVolume drops a volume from the seeded set and from the daemon, so
// the next cold start inspects and re-seeds it. Called only when a container
// failed to start because the init was not at the path its entrypoint names,
// which is what an empty volume looks like from here.
//
// Removing it is best-effort by design: Docker refuses to remove a volume any
// container still references, so warm environments holding this one keep it and
// the removal simply does not happen — which is the right answer, since a
// volume those containers started from is not the empty one.
func (cr *ContainerRuntime) forgetInitVolume(ctx context.Context, name string) {
	if name == "" {
		return
	}
	cr.initVolumes.Delete(name)
	if err := cr.docker.RemoveVolume(ctx, name, true); err != nil {
		cr.logger.Debug("lambda: could not remove the init volume after a container failed to start",
			zap.String("volume", name), zap.Error(err))
	}
}

// goarchForLambda maps a function's Architectures value to the GOARCH its init
// was built for. It mirrors initbin's own mapping and
// dockerPlatformForLambdaArchitectures: the function's architecture picks the
// image platform and the init together, so the three must never disagree.
func goarchForLambda(arch string) string {
	if arch == "arm64" {
		return "arm64"
	}
	return "amd64"
}
