package efs

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// The NFS data plane (OVERCAST_EFS_NFS=true, live mode only) gives each mount
// target a userspace NFS server: one NFS-Ganesha container per mount target,
// exporting the file system's Docker volume with container port 2049 published
// on a dynamic host port. Nothing here is privileged and no kernel module is
// involved — Ganesha is a plain userspace daemon, so the export works the same
// on Linux, macOS and Windows Docker hosts.
//
// This is opt-in because it is not needed for the common case: Lambda and ECS
// mount the same named volume directly (steps 2 and 3), so cross-service data
// sharing already works without an NFS hop. Exports exist for clients that
// genuinely speak NFS — a host mount, or a sibling container.

const (
	// nfsContainerPrefix names export containers "overcast-efs-nfs-<MountTargetId>".
	nfsContainerPrefix = "overcast-efs-nfs-"
	// nfsExportRoot is where the file system's volume is mounted inside the
	// export container; every EXPORT path is rooted here.
	nfsExportRoot = "/export"
	// nfsContainerPort is the NFS port inside the container, published on a
	// dynamic host port.
	nfsContainerPort = "2049/tcp"
	// nsNFSPorts reserves host ports: key is the port, value the mount target.
	nsNFSPorts = "efs:nfsports"
	// nfsPortRange bounds the search above EFSNFSPortBase.
	nfsPortRange = 1000
)

// Readiness polling: bounded retries, no fixed sleeps. An export that never
// answers falls back to "available" rather than wedging the mount target —
// same degradation the rest of live mode uses when Docker misbehaves.
const (
	nfsReadyInterval = 500 * time.Millisecond
	nfsReadyAttempts = 60
	nfsProbeTimeout  = 2 * time.Second
	// nfsStartTimeout bounds creating and starting the container, once the
	// image is local.
	nfsStartTimeout = 2 * time.Minute
	// nfsPullTimeout bounds the image pull on its own, because the first one
	// on a machine fetches a few hundred megabytes and legitimately outlasts
	// any sane container-start budget. Sharing one deadline meant a slow cold
	// pull silently downgraded the mount target to metadata-only.
	nfsPullTimeout = 15 * time.Minute
)

func nfsContainerName(mountTargetID string) string { return nfsContainerPrefix + mountTargetID }

// nfsActive reports whether mount targets should run an export container.
func (s *Service) nfsActive() bool {
	return s.volumesActive() && s.cfg != nil && s.cfg.EFSNFSExport
}

// ─── Host-port reservations ───────────────────────────────────────────────────

// allocateNFSPort reserves the first free port at or above EFS_NFS_PORT_BASE.
// Serialized so concurrent mount-target creates cannot claim the same port.
func (s *Service) allocateNFSPort(ctx context.Context, mountTargetID string) (int, error) {
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()

	pairs, err := s.store.Scan(ctx, nsNFSPorts, "")
	if err != nil {
		return 0, fmt.Errorf("scan port reservations: %w", err)
	}
	used := make(map[int]struct{}, len(pairs))
	for _, kv := range pairs {
		if port, convErr := strconv.Atoi(kv.Key); convErr == nil {
			used[port] = struct{}{}
		}
	}
	base := s.cfg.EFSNFSPortBase
	for port := base; port < base+nfsPortRange; port++ {
		if _, taken := used[port]; taken {
			continue
		}
		if err := s.store.Set(ctx, nsNFSPorts, strconv.Itoa(port), mountTargetID); err != nil {
			return 0, fmt.Errorf("reserve port %d: %w", port, err)
		}
		return port, nil
	}
	return 0, fmt.Errorf("no free host port in [%d,%d)", base, base+nfsPortRange)
}

// reserveNFSPort records a port discovered on an already-running container.
func (s *Service) reserveNFSPort(ctx context.Context, mountTargetID string, port int) {
	if port <= 0 {
		return
	}
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if err := s.store.Set(ctx, nsNFSPorts, strconv.Itoa(port), mountTargetID); err != nil {
		s.log.Warn("efs: reserve adopted NFS port", zap.Int("port", port), zap.Error(err))
	}
}

func (s *Service) releaseNFSPort(ctx context.Context, port int) {
	if port <= 0 {
		return
	}
	s.nfsMu.Lock()
	defer s.nfsMu.Unlock()
	if err := s.store.Delete(ctx, nsNFSPorts, strconv.Itoa(port)); err != nil {
		s.log.Warn("efs: release NFS port", zap.Int("port", port), zap.Error(err))
	}
}

// nfsPortInUse reports whether a host port is currently reserved.
func (s *Service) nfsPortInUse(ctx context.Context, port int) (bool, error) {
	_, found, err := s.store.Get(ctx, nsNFSPorts, strconv.Itoa(port))
	return found, err
}

// ─── Ganesha configuration ────────────────────────────────────────────────────

// ganeshaExport is one EXPORT block. Path is absolute inside the container;
// Pseudo is the NFSv4 pseudo-filesystem path clients mount.
type ganeshaExport struct {
	ID     int
	Path   string
	Pseudo string
	// AnonUID/AnonGID come from an access point's PosixUser. When set, the
	// export squashes every client identity onto them — which is what an EFS
	// access point with a PosixUser does.
	AnonUID *int64
	AnonGID *int64
}

// ganeshaExportsFor builds the export list for a file system: the volume root
// at pseudo path "/", then one export per access point at "/<AccessPointId>",
// rooted at the access point's root directory.
//
// Exports are fixed when the mount target starts. An access point created
// afterwards has no pseudo path until the mount target is recreated — Ganesha
// reloads exports only on restart, and churning a live NFS server on every
// CreateAccessPoint would break clients holding open files.
func (s *Service) ganeshaExportsFor(ctx context.Context, region, fsID string) ([]ganeshaExport, error) {
	exports := []ganeshaExport{{ID: 1, Path: nfsExportRoot, Pseudo: "/"}}

	aps, err := s.listAccessPointsForFileSystem(ctx, region, fsID)
	if err != nil {
		return nil, err
	}
	for i, ap := range aps {
		path := nfsExportRoot
		if ap.RootDirectory != nil {
			if sub := strings.Trim(ap.RootDirectory.Path, "/"); sub != "" {
				path = nfsExportRoot + "/" + sub
				// AWS creates the root directory on first mount when
				// CreationInfo says how; without it a missing directory makes
				// the mount fail, and Ganesha will reject just this export.
				if ap.RootDirectory.CreationInfo != nil {
					s.ensureVolumeSubdir(ctx, volumeName(fsID), sub, ap.RootDirectory.CreationInfo)
				}
			}
		}
		exp := ganeshaExport{ID: i + 2, Path: path, Pseudo: "/" + ap.AccessPointId}
		if !safeExportToken(exp.Path) || !safeExportToken(exp.Pseudo) {
			// CreateAccessPoint does not constrain RootDirectory.Path, so it
			// can hold characters that would corrupt the generated
			// configuration — or terminate the heredoc that carries it into
			// the container. Skip just this export: the access point loses its
			// pseudo-path, the rest of the file system stays exported.
			s.log.Warn("efs: access point root directory is not representable as an NFS export — skipping",
				zap.String("access_point", ap.AccessPointId), zap.String("path", exp.Path))
			continue
		}
		if ap.PosixUser != nil {
			exp.AnonUID, exp.AnonGID = ap.PosixUser.Uid, ap.PosixUser.Gid
		}
		exports = append(exports, exp)
	}
	return exports, nil
}

// safeExportToken reports whether a value can be embedded verbatim in
// ganesha.conf. Deliberately strict: anything outside this set either breaks
// the configuration grammar (whitespace, quotes, ';', '#') or the shell
// heredoc that delivers it (newlines).
func safeExportToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// buildGaneshaConfig renders ganesha.conf for the given exports. NFSv4 only:
// no rpcbind, no NLM, no RQUOTA and no UDP, which is what lets the daemon run
// unprivileged in a container.
func buildGaneshaConfig(exports []ganeshaExport) string {
	var b strings.Builder
	b.WriteString("NFS_CORE_PARAM {\n")
	b.WriteString("\tProtocols = 4;\n")
	b.WriteString("\tNFS_Port = 2049;\n")
	b.WriteString("\tEnable_NLM = false;\n")
	b.WriteString("\tEnable_RQUOTA = false;\n")
	b.WriteString("\tEnable_UDP = false;\n")
	b.WriteString("}\n")
	// Graceless start: a fresh container has no state to recover, and a grace
	// period would stall the first mount for no benefit.
	b.WriteString("NFSV4 {\n\tGraceless = true;\n\tGrace_Period = 5;\n}\n")
	b.WriteString("LOG {\n\tDefault_Log_Level = EVENT;\n}\n")

	for _, e := range exports {
		b.WriteString("EXPORT {\n")
		fmt.Fprintf(&b, "\tExport_Id = %d;\n", e.ID)
		fmt.Fprintf(&b, "\tPath = %s;\n", e.Path)
		fmt.Fprintf(&b, "\tPseudo = %s;\n", e.Pseudo)
		b.WriteString("\tAccess_Type = RW;\n")
		b.WriteString("\tProtocols = 4;\n")
		b.WriteString("\tTransports = TCP;\n")
		b.WriteString("\tSecType = sys;\n")
		if e.AnonUID != nil && e.AnonGID != nil {
			b.WriteString("\tSquash = All_Squash;\n")
			fmt.Fprintf(&b, "\tAnonymous_Uid = %d;\n", *e.AnonUID)
			fmt.Fprintf(&b, "\tAnonymous_Gid = %d;\n", *e.AnonGID)
		} else {
			b.WriteString("\tSquash = No_Root_Squash;\n")
		}
		b.WriteString("\tFSAL {\n\t\tName = VFS;\n\t}\n")
		b.WriteString("}\n")
	}
	return b.String()
}

// ganeshaStartScript writes the configuration into the container and execs the
// daemon. The image is used purely as a carrier for ganesha.nfsd and its VFS
// FSAL, so the entrypoint is replaced rather than configured: writing the file
// from the command line keeps the whole export self-contained, with no host
// bind mount and no dependence on the image's own entrypoint.
func ganeshaStartScript(conf string) string {
	const confPath = "/etc/overcast-ganesha.conf"
	var b strings.Builder
	b.WriteString("set -e\n")
	// ganesha.nfsd writes a pid file and callback credentials here and exits
	// if the directory is missing.
	b.WriteString("mkdir -p /usr/local/var/run/ganesha " + shQuote(nfsExportRoot) + "\n")
	b.WriteString("cat > " + confPath + " <<'OVERCAST_GANESHA_EOF'\n")
	b.WriteString(conf)
	b.WriteString("\nOVERCAST_GANESHA_EOF\n")
	b.WriteString("exec ganesha.nfsd -F -L /dev/stdout -f " + confPath + " -N NIV_EVENT\n")
	return b.String()
}

// ─── Export container lifecycle ───────────────────────────────────────────────

// startExportAsync launches the export for a new mount target off the request
// path — an image pull must never delay CreateMountTarget.
// The phases carry their own deadlines rather than one budget for the whole
// call — an image pull and a container start differ by orders of magnitude,
// and a single deadline generous enough for the first is useless for the
// second.
func (s *Service) startExportAsync(region, mountTargetID, fsID string) {
	s.nfsWg.Add(1)
	go func() {
		defer s.nfsWg.Done()
		s.startExport(middleware.ContextWithRegion(context.Background(), region),
			region, mountTargetID, fsID)
	}()
}

// startExport creates and starts one mount target's NFS-Ganesha container,
// then hands off to the readiness probe. Every failure path leaves the mount
// target usable as metadata and transitions it to "available" — live mode
// degrades, it does not fail the control plane.
func (s *Service) startExport(ctx context.Context, region, mountTargetID, fsID string) {
	if !s.nfsActive() {
		return
	}
	name := nfsContainerName(mountTargetID)

	// Post-restart reuse: an export container for this mount target may
	// already exist and still be ours.
	if existing, err := s.docker.GetContainerByName(ctx, name); err == nil && existing != nil {
		if !existing.HasOvercastLabels(serviceName, mountTargetID) {
			s.log.Warn("efs: container name taken by a container we do not manage — skipping export",
				zap.String("mount_target", mountTargetID), zap.String("container", name))
			s.markMountTargetAvailable(ctx, region, mountTargetID)
			return
		}
		hostPort := publishedPort(existing.NetworkSettings.Ports[nfsContainerPort])
		if hostPort == 0 {
			var err error
			if hostPort, err = s.allocateNFSPort(ctx, mountTargetID); err != nil {
				s.log.Warn("efs: allocate NFS port", zap.Error(err))
			}
		} else {
			s.reserveNFSPort(ctx, mountTargetID, hostPort)
		}
		if !existing.State.Running {
			if err := s.docker.StartContainer(ctx, existing.ID); err != nil {
				s.log.Warn("efs: start existing export container",
					zap.String("mount_target", mountTargetID), zap.Error(err))
			}
		}
		s.recordExport(ctx, region, mountTargetID, existing.ID, hostPort)
		return
	}

	exports, err := s.ganeshaExportsFor(ctx, region, fsID)
	if err != nil {
		s.log.Warn("efs: build exports — mount target stays metadata-only",
			zap.String("mount_target", mountTargetID), zap.Error(err))
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}

	hostPort, err := s.allocateNFSPort(ctx, mountTargetID)
	if err != nil {
		s.log.Warn("efs: allocate NFS port — mount target stays metadata-only",
			zap.String("mount_target", mountTargetID), zap.Error(err))
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}

	if s.puller == nil {
		s.releaseNFSPort(ctx, hostPort)
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}
	pullCtx, cancelPull := context.WithTimeout(ctx, nfsPullTimeout)
	err = s.puller.Ensure(pullCtx, s.cfg.EFSNFSImage)
	cancelPull()
	if err != nil {
		s.log.Warn("efs: pull NFS image — mount target stays metadata-only",
			zap.String("image", s.cfg.EFSNFSImage), zap.Error(err))
		s.releaseNFSPort(ctx, hostPort)
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}

	// The image is local now, so the rest is fast: bound it tightly.
	startCtx, cancelStart := context.WithTimeout(ctx, nfsStartTimeout)
	defer cancelStart()

	// Exports also join a user-defined network so a containerized Overcast and
	// sibling NFS clients can reach them by container IP; the published host
	// port serves clients on the host itself.
	network := s.cfg.EFSNetwork
	if network != "" {
		if _, err := s.docker.CreateNetwork(startCtx, network); err != nil {
			s.log.Debug("efs: create export network (may already exist)",
				zap.String("network", network), zap.Error(err))
		}
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image:        s.cfg.EFSNFSImage,
			Entrypoint:   []string{"/bin/sh", "-c"},
			Cmd:          []string{ganeshaStartScript(buildGaneshaConfig(exports))},
			ExposedPorts: map[string]struct{}{nfsContainerPort: {}},
			Labels:       docker.ManagedLabels(serviceName, mountTargetID),
		},
		HostConfig: &docker.HostConfig{
			Mounts: []docker.Mount{{Type: "volume", Source: volumeName(fsID), Target: nfsExportRoot}},
			PortBindings: map[string][]docker.PortBinding{
				nfsContainerPort: {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(hostPort)}},
			},
		},
	}
	if network != "" {
		req.HostConfig.NetworkMode = network
		req.NetworkingConfig = &docker.NetworkingConfig{
			EndpointsConfig: map[string]*docker.EndpointSettings{network: {}},
		}
	}
	containerID, err := s.docker.CreateContainer(startCtx, name, req)
	if err != nil {
		s.log.Warn("efs: create export container — mount target stays metadata-only",
			zap.String("mount_target", mountTargetID), zap.Error(err))
		s.releaseNFSPort(ctx, hostPort)
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}

	if err := s.docker.StartContainer(startCtx, containerID); err != nil {
		s.log.Warn("efs: start export container — mount target stays metadata-only",
			zap.String("mount_target", mountTargetID), zap.Error(err))
		if rmErr := s.docker.RemoveContainer(context.Background(), containerID, true); rmErr != nil {
			s.log.Warn("efs: remove failed export container", zap.String("container", containerID), zap.Error(rmErr))
		}
		s.releaseNFSPort(ctx, hostPort)
		s.markMountTargetAvailable(ctx, region, mountTargetID)
		return
	}

	s.recordExport(ctx, region, mountTargetID, containerID, hostPort)
}

// recordExport persists the container and port on the mount target, or tears
// the container down when the mount target went away while it was starting.
//
// The start takes real time (an image pull on first use), so a delete can land
// in the middle. deleteMountTarget stops the container ID it finds on the
// record — which is empty until this call — so the start goroutine is the only
// party that still knows the ID and owns the cleanup. Same race as RDS (#412),
// ElastiCache (#459) and MSK/Lambda (#461).
func (s *Service) recordExport(ctx context.Context, region, mountTargetID, containerID string, hostPort int) {
	rec, found, err := s.getMountTarget(ctx, region, mountTargetID)
	if err != nil || !found || rec.LifeCycleState == stateDeleting {
		s.teardownOrphanedExport(mountTargetID, containerID, hostPort)
		return
	}
	rec.NFSContainerId = containerID
	rec.NFSHostPort = hostPort
	if err := s.putMountTarget(ctx, region, rec); err != nil {
		s.log.Warn("efs: persist export container",
			zap.String("mount_target", mountTargetID), zap.Error(err))
		return
	}
	s.scheduleExportReadiness(region, mountTargetID, containerID, hostPort)
}

// teardownOrphanedExport removes a container whose mount target was deleted
// while it was still starting, and releases the port it held.
func (s *Service) teardownOrphanedExport(mountTargetID, containerID string, hostPort int) {
	// A background context: the caller's may already be cancelled, and this
	// cleanup must still run.
	ctx, cancel := context.WithTimeout(context.Background(), volumeOpTimeout)
	defer cancel()

	s.log.Info("efs: mount target deleted while its export was starting — removing container",
		zap.String("mount_target", mountTargetID), zap.String("container", containerID))
	if containerID != "" {
		if err := s.docker.StopContainer(ctx, containerID, 5); err != nil {
			s.log.Debug("efs: stop orphaned export container", zap.String("container", containerID), zap.Error(err))
		}
		if err := s.docker.RemoveContainer(ctx, containerID, true); err != nil {
			s.log.Warn("efs: remove orphaned export container", zap.String("container", containerID), zap.Error(err))
		}
	}
	s.releaseNFSPort(ctx, hostPort)
}

// stopExportAsync tears the export down off the request path. DeleteMountTarget
// answers 204 on AWS without waiting for anything, and stopping a container
// costs a stop timeout, so the delete must not block on it. Safe to run
// detached: the record is already "deleting" by the time this is called, so a
// start still in flight tears down its own container instead of racing here.
func (s *Service) stopExportAsync(rec *mountTargetRecord) {
	if rec == nil || !s.volumesActive() || (rec.NFSContainerId == "" && rec.NFSHostPort == 0) {
		return
	}
	snapshot := *rec
	s.nfsWg.Add(1)
	go func() {
		defer s.nfsWg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), volumeOpTimeout)
		defer cancel()
		s.stopExport(ctx, &snapshot)
	}()
}

// stopExport tears down a mount target's export container and releases its
// host port. Safe to call for a mount target that never had one.
func (s *Service) stopExport(ctx context.Context, rec *mountTargetRecord) {
	if rec == nil || !s.volumesActive() {
		return
	}
	if rec.NFSContainerId != "" {
		if err := s.docker.StopContainer(ctx, rec.NFSContainerId, 5); err != nil {
			s.log.Debug("efs: stop export container",
				zap.String("container", rec.NFSContainerId), zap.Error(err))
		}
		if err := s.docker.RemoveContainer(ctx, rec.NFSContainerId, true); err != nil {
			s.log.Warn("efs: remove export container",
				zap.String("container", rec.NFSContainerId), zap.Error(err))
		}
	}
	s.releaseNFSPort(ctx, rec.NFSHostPort)
}

// markMountTargetAvailable completes the creating → available transition for a
// mount target whose export could not be started.
func (s *Service) markMountTargetAvailable(ctx context.Context, region, mountTargetID string) {
	rec, found, err := s.getMountTarget(ctx, region, mountTargetID)
	if err != nil || !found || rec.LifeCycleState != stateCreating {
		return
	}
	rec.LifeCycleState = stateAvailable
	if err := s.putMountTarget(ctx, region, rec); err != nil {
		s.log.Warn("efs: mount target transition to available failed",
			zap.String("mount_target", mountTargetID), zap.Error(err))
	}
}

// ─── Readiness ────────────────────────────────────────────────────────────────

// scheduleExportReadiness polls the export with bounded retries and marks the
// mount target available once it answers an NFSv4 NULL call — or once the
// retries run out, so a wedged export cannot leave a mount target "creating"
// forever.
func (s *Service) scheduleExportReadiness(region, mountTargetID, containerID string, hostPort int) {
	if s.skipNFSReadiness {
		s.markMountTargetAvailable(
			middleware.ContextWithRegion(context.Background(), region), region, mountTargetID)
		return
	}
	attempt := 0
	var probe func(ctx context.Context)
	probe = func(ctx context.Context) {
		attempt++
		// A delete landing between two probes cancels the pending timer, but
		// not a probe already running — which would then reschedule itself
		// and keep dialling a container that is being torn down.
		if rec, found, err := s.getMountTarget(ctx, region, mountTargetID); err != nil ||
			!found || rec.LifeCycleState == stateDeleting {
			return
		}
		addr := s.exportProbeAddr(ctx, containerID, hostPort)
		if err := nfsNullPing(ctx, addr, nfsProbeTimeout); err == nil {
			s.markMountTargetAvailable(ctx, region, mountTargetID)
			return
		}
		if attempt >= nfsReadyAttempts {
			s.log.Warn("efs: NFS export never answered — mount target available without a working export",
				zap.String("mount_target", mountTargetID), zap.String("addr", addr), zap.Int("attempts", attempt))
			s.markMountTargetAvailable(ctx, region, mountTargetID)
			return
		}
		s.scheduler.AfterScoped(region, mountTargetID, "nfs-ready", nfsReadyInterval, probe)
	}
	s.scheduler.AfterScoped(region, mountTargetID, "nfs-ready", nfsReadyInterval, probe)
}

// exportProbeAddr returns the address to probe: the container's own IP when
// Overcast itself runs in a container (published host ports are not reachable
// from a sibling), otherwise 127.0.0.1 and the published port.
func (s *Service) exportProbeAddr(ctx context.Context, containerID string, hostPort int) string {
	if runningInContainer() {
		// Join the export network so the container IP below is routable.
		if network := s.cfg.EFSNetwork; network != "" {
			if hostname, err := os.Hostname(); err == nil && hostname != "" {
				if err := s.docker.ConnectNetwork(ctx, network, hostname); err != nil {
					s.log.Debug("efs: join export network", zap.String("network", network), zap.Error(err))
				}
			}
		}
		if info, err := s.docker.InspectContainer(ctx, containerID); err == nil {
			if ep, ok := info.NetworkSettings.Networks[s.cfg.EFSNetwork]; ok && ep.IPAddress != "" {
				return net.JoinHostPort(ep.IPAddress, "2049")
			}
			for _, ep := range info.NetworkSettings.Networks {
				if ep.IPAddress != "" {
					return net.JoinHostPort(ep.IPAddress, "2049")
				}
			}
		}
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(hostPort))
}

// nfsNullPing sends an NFSv4 NULL call (program 100003, version 4, procedure 0)
// over TCP and waits for an accepted RPC reply.
//
// A bare TCP dial is not a readiness signal: Docker's published port accepts
// connections as soon as the proxy is up, well before ganesha.nfsd is serving.
// The NULL procedure is the cheapest call that proves the NFS program itself
// answered.
func nfsNullPing(ctx context.Context, addr string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}

	const xid = 0x0efc0a57 // any value; the reply must echo it
	body := make([]byte, 0, 40)
	for _, v := range []uint32{
		xid,
		0,      // msg_type: CALL
		2,      // rpcvers
		100003, // program: NFS
		4,      // version
		0,      // procedure: NULL
		0, 0,   // cred: AUTH_NONE, length 0
		0, 0, // verf: AUTH_NONE, length 0
	} {
		body = binary.BigEndian.AppendUint32(body, v)
	}
	// Record marking: high bit set marks the last fragment.
	frame := binary.BigEndian.AppendUint32(nil, 0x80000000|uint32(len(body)))
	if _, err := conn.Write(append(frame, body...)); err != nil {
		return fmt.Errorf("write call: %w", err)
	}

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return fmt.Errorf("read record mark: %w", err)
	}
	n := be32(hdr) &^ 0x80000000
	// An accepted reply is 24 bytes (xid, type, stat, verf, accept_stat);
	// anything wildly larger is not an answer to a NULL call.
	if n < 24 || n > 1024 {
		return fmt.Errorf("implausible reply length %d", n)
	}
	reply := make([]byte, n)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return fmt.Errorf("read reply: %w", err)
	}
	if got := be32(reply[0:4]); got != xid {
		return fmt.Errorf("xid mismatch: want %#x, got %#x", uint32(xid), got)
	}
	if got := be32(reply[4:8]); got != 1 {
		return fmt.Errorf("not an RPC reply (msg_type %d)", got)
	}
	if got := be32(reply[8:12]); got != 0 {
		return fmt.Errorf("RPC denied (reply_stat %d)", got)
	}
	// reply[12:20] is the AUTH_NONE verifier; accept_stat follows it.
	if got := be32(reply[20:24]); got != 0 {
		return fmt.Errorf("RPC not accepted (accept_stat %d)", got)
	}
	return nil
}

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }

// runningInContainer reports whether Overcast itself is running inside a
// container, in which case a sibling's published host port is not reachable
// and its container IP must be used instead. Same probe MSK uses.
func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// publishedPort returns the host port from a container's port bindings.
func publishedPort(bindings []docker.PortBinding) int {
	for _, b := range bindings {
		if p, err := strconv.Atoi(b.HostPort); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// ─── Reconciliation ───────────────────────────────────────────────────────────

// reconcileExports aligns export containers with persisted mount targets
// across every region: a mount target whose container is still running adopts
// it (container ID and published port), a mount target whose container is gone
// gets a fresh one, and any managed EFS container without a live mount target
// behind it is removed. Idempotent.
//
// The sweep also covers root-directory materialization helpers: those are
// one-shot containers removed by the call that starts them, so one surviving a
// restart is by definition an orphan.
func (s *Service) reconcileExports(ctx context.Context) {
	if !s.volumesActive() {
		return
	}

	containers, err := s.docker.ListContainers(ctx, serviceName)
	if err != nil {
		s.log.Warn("efs: export reconciliation: list containers", zap.Error(err))
		return
	}
	byMountTarget := make(map[string]*docker.ContainerSummary, len(containers))
	for i := range containers {
		if id := containers[i].ResourceID(); strings.HasPrefix(id, "fsmt-") {
			byMountTarget[id] = &containers[i]
		}
	}

	// Every region: scan with an empty region so keys keep their region prefix.
	pairs, err := s.store.Scan(ctx, nsMountTargets, "")
	if err != nil {
		s.log.Warn("efs: export reconciliation: list mount targets", zap.Error(err))
		return
	}
	known := make(map[string]struct{}, len(pairs))
	for _, kv := range pairs {
		region, mtID := serviceutil.SplitRegionKey(kv.Key)
		if mtID == "" {
			continue
		}
		known[mtID] = struct{}{}
		if !s.nfsActive() {
			continue
		}
		rctx := middleware.ContextWithRegion(ctx, region)
		rec, found, err := s.getMountTarget(rctx, region, mtID)
		if err != nil || !found || rec.LifeCycleState == stateDeleting {
			continue
		}
		c, running := byMountTarget[mtID]
		switch {
		case running && c.State == "running":
			// Adopt: keep the container, re-reserve the port it publishes.
			hostPort := 0
			for _, p := range c.Ports {
				if p.ContainerPort == 2049 && p.HostPort > 0 {
					hostPort = p.HostPort
					break
				}
			}
			if rec.NFSContainerId == c.ID && rec.NFSHostPort == hostPort {
				s.reserveNFSPort(rctx, mtID, hostPort)
				continue
			}
			rec.NFSContainerId = c.ID
			rec.NFSHostPort = hostPort
			if err := s.putMountTarget(rctx, region, rec); err != nil {
				s.log.Warn("efs: adopt export container", zap.String("mount_target", mtID), zap.Error(err))
				continue
			}
			s.reserveNFSPort(rctx, mtID, hostPort)
			s.log.Info("efs: adopted running export container",
				zap.String("mount_target", mtID), zap.String("container", c.ID))
		case running:
			// Present but not running — remove it and start a fresh export.
			if err := s.docker.RemoveContainer(rctx, c.ID, true); err != nil {
				s.log.Warn("efs: remove stopped export container", zap.String("container", c.ID), zap.Error(err))
			}
			s.releaseNFSPort(rctx, rec.NFSHostPort)
			rec.NFSContainerId, rec.NFSHostPort = "", 0
			if err := s.putMountTarget(rctx, region, rec); err != nil {
				s.log.Warn("efs: clear stale export fields", zap.String("mount_target", mtID), zap.Error(err))
			}
			s.startExportAsync(region, mtID, rec.FileSystemId)
		default:
			s.startExportAsync(region, mtID, rec.FileSystemId)
		}
	}

	// Sweep: any managed EFS container whose mount target is gone, plus any
	// materialization helper that outlived the process that started it.
	for i := range containers {
		c := &containers[i]
		resourceID := c.ResourceID()
		if strings.HasPrefix(resourceID, "fsmt-") {
			if _, ok := known[resourceID]; ok && s.nfsActive() {
				continue
			}
			s.log.Info("efs: removing orphaned export container",
				zap.String("container", c.FirstName()), zap.String("mount_target", resourceID))
		} else {
			s.log.Info("efs: removing stale materialization helper",
				zap.String("container", c.FirstName()))
		}
		if err := s.docker.RemoveContainer(ctx, c.ID, true); err != nil {
			s.log.Warn("efs: remove orphaned container", zap.String("container", c.ID), zap.Error(err))
		}
	}

	s.sweepNFSPorts(ctx, known)
}

// sweepNFSPorts releases host-port reservations whose mount target no longer
// exists. Without this a restart-and-delete cycle would leak the range.
func (s *Service) sweepNFSPorts(ctx context.Context, known map[string]struct{}) {
	pairs, err := s.store.Scan(ctx, nsNFSPorts, "")
	if err != nil {
		s.log.Warn("efs: export reconciliation: scan port reservations", zap.Error(err))
		return
	}
	for _, kv := range pairs {
		if _, ok := known[kv.Value]; ok {
			continue
		}
		port, convErr := strconv.Atoi(kv.Key)
		if convErr != nil {
			continue
		}
		s.releaseNFSPort(ctx, port)
	}
}
