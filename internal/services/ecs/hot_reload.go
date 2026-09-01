package ecs

// hot_reload.go — redirecting a task definition's scratch volume at a host
// path, so a save on the developer's machine is live in the task on the next
// request.
//
// The mechanism is deliberately not a new field on the task definition. A
// task definition says what it says on AWS: the volume is an ordinary
// name-only scratch volume, legal on Fargate, and the mount point carries the
// container path and readOnly exactly as it would in production. Only a tag —
// inert metadata that real AWS stores and ignores — says to back that volume
// with a bind instead. So nothing here can make a task definition pass
// registration that AWS would reject, and a tag that leaks into a real deploy
// costs nothing: the volume is still a legal scratch volume there.
//
// This mirrors Lambda's overcast:hot-reload-path tag, down to the tag name and
// the server-side flag, because a developer should not have to learn two
// mechanisms for one idea.

import (
	"sort"
	"strings"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/hostpath"
)

// decorateBindMountError names the host paths a container binds when Docker
// refuses one of them.
//
// Docker's own message says neither which path failed nor that Docker
// Desktop's File Sharing setting is the usual reason, and this error becomes
// the task's stoppedReason — the one place a user looks when a task will not
// start. Every bind source is named rather than a guess at the culprit: the
// daemon does not say which one it rejected.
func decorateBindMountError(err error, mounts []docker.Mount) error {
	if err == nil {
		return err
	}
	sources := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if m.Type == "bind" && m.Source != "" {
			sources = append(sources, m.Source)
		}
	}
	if len(sources) == 0 {
		return err
	}
	sort.Strings(sources)
	return hostpath.DecorateMountError(err, strings.Join(sources, ", "))
}

// hotReloadTagPrefix redirects one named volume:
// "overcast:hot-reload-path/<volume-name>". Tag keys admit "/", which keeps
// the volume name out of the value — necessary because a Windows path contains
// ":" and would make any "<path>:<container-path>" value format ambiguous.
const hotReloadTagPrefix = "overcast:hot-reload-path/"

// hotReloadTagBare is the suffix-free form, which applies to a task
// definition's single redirectable volume. It is the same key Lambda uses, so
// the common case — one application, one source tree — needs no new spelling.
const hotReloadTagBare = "overcast:hot-reload-path"

// redirectableVolume reports whether a volume can be pointed at a host path.
// Only a scratch volume can: an EFS volume belongs to a file system, a Docker
// volume names its own storage, and a host volume already is a bind.
// Redirecting any of those would mean overriding what the definition asked
// for, rather than supplying what it deliberately left open.
func redirectableVolume(v *TaskVolume) bool {
	return v.EFSVolumeConfiguration == nil &&
		v.DockerVolumeConfiguration == nil &&
		(v.Host == nil || v.Host.SourcePath == "")
}

// hotReloadPaths resolves a task definition's tags into volume name → host
// path. It returns nil when the feature is off, when no tag asks for it, or
// when a tag cannot be honoured — in every one of those cases the task runs on
// the plain scratch volumes it declared, which is what AWS would run.
//
// Resolution order is deliberate. A suffixed key names its volume and is
// unambiguous, so suffixed keys win outright; the bare key is a convenience
// that only makes sense when there is exactly one candidate, and saying so out
// loud beats guessing.
func (h *Handler) hotReloadPaths(td *TaskDefinition, tags map[string]string) map[string]string {
	if len(tags) == 0 || h.cfg == nil {
		return nil
	}

	redirectable := make([]string, 0, len(td.Volumes))
	byName := make(map[string]*TaskVolume, len(td.Volumes))
	for i := range td.Volumes {
		v := &td.Volumes[i]
		byName[v.Name] = v
		if redirectableVolume(v) {
			redirectable = append(redirectable, v.Name)
		}
	}

	requested := make(map[string]string)
	bare := ""
	for key, value := range tags {
		switch {
		case key == hotReloadTagBare:
			bare = strings.TrimSpace(value)
		case strings.HasPrefix(key, hotReloadTagPrefix):
			name := strings.TrimPrefix(key, hotReloadTagPrefix)
			if name != "" {
				requested[name] = strings.TrimSpace(value)
			}
		}
	}
	if len(requested) == 0 && bare == "" {
		return nil
	}

	// The flag is checked only once a tag has actually asked for a redirect,
	// so the warning naming it reaches the one user who wants to hear it.
	if !h.cfg.ECSHotReload {
		h.log.Warn("ecs: hot reload requested by tag but not enabled — the task runs on its declared scratch volumes",
			zap.String("task_definition", td.TaskDefinitionArn),
			zap.String("hint", "start Overcast with OVERCAST_ECS_HOT_RELOAD=true (or OVERCAST_HOT_RELOAD=true for every compute service) to bind the host path into the task"))
		return nil
	}

	if bare != "" {
		switch {
		case len(requested) > 0:
			h.log.Warn("ecs: bare overcast:hot-reload-path tag ignored — a volume-specific tag takes precedence",
				zap.String("task_definition", td.TaskDefinitionArn))
		case len(redirectable) == 1:
			requested[redirectable[0]] = bare
		case len(redirectable) == 0:
			h.log.Warn("ecs: overcast:hot-reload-path tag ignored — the task definition declares no volume that can be redirected",
				zap.String("task_definition", td.TaskDefinitionArn),
				zap.String("hint", "declare a volume with a name and no configuration, and mount it where the source should appear"))
		default:
			h.log.Warn("ecs: overcast:hot-reload-path tag ignored — the task definition declares more than one redirectable volume, so the bare tag is ambiguous",
				zap.String("task_definition", td.TaskDefinitionArn),
				zap.Strings("candidates", redirectable),
				zap.String("hint", "name the volume in the tag key, for example overcast:hot-reload-path/"+redirectable[0]))
		}
	}

	out := make(map[string]string, len(requested))
	for name, raw := range requested {
		v, declared := byName[name]
		switch {
		case !declared:
			h.log.Warn("ecs: hot-reload tag ignored — the task definition declares no such volume",
				zap.String("task_definition", td.TaskDefinitionArn), zap.String("volume", name))
			continue
		case !redirectableVolume(v):
			h.log.Warn("ecs: hot-reload tag ignored — the volume already has a configuration of its own",
				zap.String("task_definition", td.TaskDefinitionArn), zap.String("volume", name),
				zap.String("hint", "only a volume declared with a name and no configuration can be redirected at a host path"))
			continue
		}
		path, err := hostpath.Normalize(raw)
		if err != nil {
			h.log.Warn("ecs: hot-reload tag ignored — the path must be absolute",
				zap.String("task_definition", td.TaskDefinitionArn), zap.String("volume", name),
				zap.String("path", raw), zap.Error(err))
			continue
		}
		out[name] = path
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
