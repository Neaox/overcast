---
title: ECS hot reload — editing local source inside an emulated task
description: A clean inner loop for applications that run on ECS in staging and prod — mount the working tree into the task container with no rebuild — without ever making the ECS API accept a task definition real AWS would reject. Fidelity and convenience land as separate layers, and the developer guide ships with the feature.
---

# ECS hot reload — editing local source inside an emulated task

**Status:** **complete** — all three layers shipped (#923, #926, and the guide) · **Opened:** 2026-08-12 · **Revised:** 2026-08-12 (design reworked around API-fidelity/convenience separation; guide made a deliverable; open questions decided — full volume-type coverage now in scope)

> **Closed out.** What remains is follow-up work, tracked here rather than left
> implied:
>
> - **Three validation messages are Overcast's wording, not AWS's.** Only the
>   Fargate `host.sourcePath` string is verbatim from a real `ClientException`.
>   Capture AWS's own text for the Fargate `dockerVolumeConfiguration`,
>   task-scope `autoprovision`, and one-configuration-per-volume rejections
>   before pinning them in tests.
> - **The docker-gated lifecycle tests are not written.** Shared volumes
>   surviving a task stop, `autoprovision: false` failing a placement, and a
>   real bind surviving an edit — see the testing section, and use the
>   fake-daemon rig rather than `SkipWithoutDocker`.
> - **`configuredAtLaunch` volumes** remain unmodelled; the branch structure in
>   `containerMounts` takes them the same way it took
>   `dockerVolumeConfiguration`.
> - **The server-side mount overlay** stays deferred — see Alternatives
>   considered.
**Scope:** `internal/services/ecs`, `internal/docker` (volume driver options, inspect), `internal/config`, a shared host-path helper lifted out of `internal/services/lambda`, `docs/services/ecs.md`, a new published guide
**Related:** [efs-data-plane.md](./efs-data-plane.md) (the mount path this extends), Lambda hot reload ([docs/services/lambda.md § Hot Reload](../services/lambda.md#hot-reload)) — the precedent this deliberately mirrors

## The ask

An application — the motivating case is Laravel/PHP-FPM, but Rails, Django, and
Express have the same shape — runs on ECS Fargate in staging and production. The
developer wants to run the *same* task definition locally against Overcast with
their working tree mounted into the container, so a save is live on the next
request: no image rebuild, no `docker cp`, no redeploy.

Lambda already has this. ECS has nothing equivalent.

## Design principle

**The AWS API surface never bends for local convenience.** Real Fargate rejects
`host.sourcePath` with `ClientException: host.sourcePath should not be set for
volumes in Fargate`; Overcast must reject it identically, always, with no flag
that softens it — otherwise a template that passes locally fails its first real
deploy, and the emulator has trained its user wrong.

Local-dev intent instead travels in a **tag**, which is inert metadata on real
AWS. That is exactly how Lambda's hot reload works today
(`OVERCAST_LAMBDA_HOT_RELOAD` server flag + `overcast:hot-reload-path` tag), and
ECS gets the same mechanism rather than a new one:

> A task definition declares an ordinary, **Fargate-legal** name-only volume and
> mounts it where the code lives. A tag on the task definition —
> `overcast:hot-reload-path/<volume-name> = <host path>` — tells Overcast to back
> that volume with a bind to the developer's working tree instead of an empty
> scratch directory.

Why this shape is the elegant one:

- **Everything AWS-shaped stays AWS-legal.** The volume, the `mountPoints` entry
  carrying the container path and `readOnly`, multi-container sharing — all of it
  is AWS's own model, valid on Fargate, and validated exactly as AWS validates it.
  Overcast adds no new fields to the task definition schema.
- **Leaking to prod is graceful, not fatal.** The tag alone is completely inert
  on AWS. Even if the guard fails and the volume + mount point reach a real
  deploy, the deploy *succeeds* (a scratch volume is legal) — degraded, not
  broken, and trivially diagnosable. A `host.sourcePath` leak fails the deploy
  outright.
- **One mental model across services.** A developer who has used Lambda hot
  reload already knows the flag family, the tag namespace, the Windows path
  normalization, and the Docker Desktop file-sharing troubleshooting. The docs
  cross-link instead of re-explaining.
- **The divergence budget is zero.** Nothing in the divergence ledger; the tag
  namespace `overcast:*` is already established as Overcast-only and ignored by
  AWS.

## What Overcast does today

ECS starts real containers at one site, and the only mounts it ever attaches
come from `efsMountsForContainer`:

- [handler_tasks.go:382](../../internal/services/ecs/handler_tasks.go) — the
  single container-creation site sets `HostConfig.Mounts` from that function and
  nothing else. Every mount it emits is `Type: "volume"`.
- [handler_tasks.go:500](../../internal/services/ecs/handler_tasks.go) — every
  mount point whose source volume is not EFS-backed is **skipped with a
  warning** (`"ecs: mount point skipped — source volume is not EFS-backed"`).
- [store.go:125](../../internal/services/ecs/store.go) — `TaskVolume` models
  `name` and `efsVolumeConfiguration` only. `volumes[].host.sourcePath` sent by
  a client is **dropped at decode** on both wire paths
  ([handler.go:435](../../internal/services/ecs/handler.go) awsJson1_1,
  [typed_logic.go:62](../../internal/services/ecs/typed_logic.go) rpcv2Cbor).
- `RegisterTaskDefinition` does not decode `tags` at all, though AWS accepts
  them there; ECS tags exist only via `TagResource`
  ([handler_tagging.go](../../internal/services/ecs/handler_tagging.go)).

Three consequences, all of which look like bugs from the outside:

1. A task definition carrying `host: { sourcePath }` **registers successfully
   and describes back without it** — no error, no mount, writes go to the
   container's own layer. Silent drops are the worst failure mode an emulator
   has; whatever else this plan does, these become either honoured or loudly
   rejected.
2. A **name-only scratch volume** — AWS's supported way to share a directory
   between containers in a task, valid on Fargate, and exactly what an
   nginx + php-fpm pair sharing `/var/www/html` uses — is also silently skipped.
3. An **EC2-launch-type** task definition with `host.sourcePath` — fully
   supported on real AWS — is silently skipped too.

CloudFormation needs no work: `ecsTaskDefinitionHandler.Create`
([provisioner_ecs.go:114](../../internal/services/cloudformation/provisioner_ecs.go))
passes resource properties through `convertCFKeysToAPI` verbatim, so both
`Volumes` and `Tags` arrive the moment the API models them. CDK synthesises
`host` volumes without client-side rejection (verified against
`aws-ecs/lib/base/task-definition.ts` on `main`: `validateVolume` checks
`configuredAtLaunch` and Managed Instances rules only), so the server-side
rejection is the only line of defence — another reason it must match AWS.

## Layer 1 — AWS-faithful volume semantics (unconditional, no flags) — **done**

> **Landed.** `internal/services/ecs` (volume model, validation, provisioning,
> mount resolution, lifecycle), `internal/docker` (`CreateVolumeWithOptions`,
> `VolumeExists`), `volumes_test.go`, service doc, capability notes, changelog
> fragment. Five things came out differently from the plan; each is recorded in
> place below and summarised here:
>
> 1. **Both wire paths came free.** `RegisterTaskDefinition` (awsJson1_1) and
>    `registerTaskDefinitionRequest` (rpcv2Cbor) both decode into `[]TaskVolume`,
>    so adding fields to the shared type covered both — no separate CBOR work,
>    and `fxamacker/cbor` falls back to the `json` tag, matching how
>    `ContainerDefinition` already rides that path.
> 2. **One name scheme for both task-lifetime shapes**, not the two the table
>    below proposed: `overcast-ecs-task-<taskID[:8]>-<volumeName>`. A scratch
>    volume *is* a task-scoped Docker volume with no configuration, and giving
>    them different prefixes would have implied a distinction that does not
>    exist.
> 3. **Cleanup finds volumes by label, not by re-reading the task definition.**
>    A definition can be deregistered while its tasks still run; the labels are
>    on the objects being removed. This also removed the need for a task
>    definition lookup on the stop path entirely. The labels have to carry the
>    **region** (`overcast.ecs.region`) for this to be safe: the managed
>    resource ID is `cluster/taskID`, which names a different task in a
>    different region, and the orphan sweep asks whether the owner still
>    exists. Without it the sweep would look the owner up in the default region
>    and delete a live task's volumes. A volume with no region label is left
>    alone — an unswept volume costs disk, a wrongly swept one costs data.
> 4. **Removal has to be retried.** Docker refuses to remove a volume any
>    container still references — *including a stopped one* — and container
>    removal is itself asynchronous through the GC, so the first attempt races
>    it and normally fails. Three attempts, 15 s apart, mirroring EFS's policy,
>    with the startup sweep as the backstop.
> 5. **An empty `host: {}` block is not a configuration.** CDK emits it for a
>    plain shared volume, so counting it toward the one-configuration-per-volume
>    rule would have rejected every task definition that shares a directory
>    between containers. `TaskVolume.configCount` encodes this.

Pure fidelity work; every piece matches real ECS and none of it is gated. With
`dockerVolumeConfiguration` included (decision 2), this closes **all four** of
AWS's volume types — the type switch is written once, correctly, instead of
twice.

`TaskVolume` gains the missing shapes:

```go
type TaskVolume struct {
	Name                      string                     `json:"name"`
	Host                      *HostVolumeProperties      `json:"host,omitempty"`
	DockerVolumeConfiguration *DockerVolumeConfiguration `json:"dockerVolumeConfiguration,omitempty"`
	EFSVolumeConfiguration    *EFSVolumeConfiguration    `json:"efsVolumeConfiguration,omitempty"`
}

// HostVolumeProperties is AWS's bind-mount volume. With SourcePath set the
// volume is a path on the container instance; empty, it is a scratch
// directory shared by the containers in the task.
type HostVolumeProperties struct {
	SourcePath string `json:"sourcePath,omitempty"`
}

// DockerVolumeConfiguration is AWS's Docker-managed volume (EC2 launch type
// only). Scope "task" ties the volume to the task's lifetime; "shared"
// outlives it, and Autoprovision decides whether ECS creates it.
type DockerVolumeConfiguration struct {
	Scope         string            `json:"scope,omitempty"`         // "task" | "shared"
	Autoprovision bool              `json:"autoprovision,omitempty"`
	Driver        string            `json:"driver,omitempty"`
	DriverOpts    map[string]string `json:"driverOpts,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}
```

Added to both request structs
([handler.go:428](../../internal/services/ecs/handler.go),
[typed_logic.go:49](../../internal/services/ecs/typed_logic.go)) — the CBOR path
is a separate decode and would otherwise stay blind. Alongside it,
`RegisterTaskDefinition` decodes `tags` and writes them through the existing
ARN-keyed tag store, which is AWS parity in its own right and what Layer 2
rides on.

**Validation** (in `validateTaskVolumes`,
[typed_logic.go:67](../../internal/services/ecs/typed_logic.go)), matching AWS:

- Fargate-compatible definition + `host.sourcePath` → `ClientException:
  host.sourcePath should not be set for volumes in Fargate`. Always. No flag.
- Fargate-compatible definition + `dockerVolumeConfiguration` → `ClientException`
  (Fargate supports only bind mounts and EFS).
- `scope: "task"` + `autoprovision: true` → `ClientException` (AWS: with task
  scope, `autoprovision` must be omitted or false).
- More than one of `host` / `dockerVolumeConfiguration` /
  `efsVolumeConfiguration` on a single volume → `ClientException`.

> **Do not invent the message strings.** Only the first is quoted verbatim from
> AWS above (captured from a real `ClientException`). Capture the other three
> against real AWS — or from the SDK's own validation — before pinning them in a
> test. A plausible-looking wrong string is worse than a generic one, because it
> reads as verified.

**Placement** — rename `efsMountsForContainer` → `containerMounts`
([handler_tasks.go:500](../../internal/services/ecs/handler_tasks.go)) with a
branch per volume shape, walked in `mountPoints` order:

| Volume shape | Emitted mount |
| --- | --- |
| `efsVolumeConfiguration` | `Type: "volume"`, `Source: overcast-efs-<fsID>`, optional `Subpath` — unchanged |
| name only, or `host: {}` | `Type: "volume"`, `Source: overcast-ecs-scratch-<taskID>-<volumeName>` — per-task scratch volume |
| `host.sourcePath` (EC2 only — the only way it passes validation) | `Type: "bind"`, `Source: sourcePath` as given |
| `dockerVolumeConfiguration`, `scope: "task"` | `Type: "volume"`, same per-task name as a scratch volume, created from the config's driver/opts/labels |
| `dockerVolumeConfiguration`, `scope: "shared"` | `Type: "volume"`, `Source: <volumeName>` — the bare name, as on a real container instance |

- Scratch and task-scoped volumes are labelled
  `docker.ManagedLabels("ecs", clusterName+"/"+taskID)` plus
  `overcast.ecs.volume-scope=task`; created at placement, removed when the task
  stops. First-mount copy-up gives them the image's ownership at the target
  path, which mirrors Fargate's writable scratch behaviour closely enough for
  the framework cases that matter.
- **Shared-scope volumes use the bare volume name and are never swept.** On AWS
  a shared Docker volume lives on the container instance until someone removes
  it, and ECS never deletes it; locally the daemon *is* the container instance,
  so the same rule applies. `autoprovision: true` creates it if missing (labelled
  `overcast.ecs.volume-scope=shared` so the sweep skips it);
  `autoprovision: false` requires it to already exist and fails the task with
  `ResourceInitializationError` naming the volume if it does not — matching AWS's
  placement failure. A user's pre-created volume is adopted untouched and never
  labelled.
- That `autoprovision: false` path is quietly the **AWS-native version of the
  EFS hack** below: pre-create a volume however you like — including
  `--opt o=bind --opt device=…` — and an EC2-launch-type task adopts it, no
  Overcast flag involved. Worth a callout in the guide.
- EC2 `sourcePath` is honoured **as AWS semantics**: a path on the container
  instance, which locally *is* the Docker daemon host. No normalization, no
  Windows path rewriting — it means what it means on AWS. (On Docker Desktop the
  daemon host is the VM; a developer who wants their laptop's tree uses Layer 2,
  which is built for exactly that.)
- `containerMounts` drops the current `h.efsResolver == nil` early return —
  scratch, host, and Docker volumes have nothing to do with EFS being wired.

**Two `internal/docker` gaps this opened**, both small and both filled:

- `createVolumeRequest` carried only `Name` and `Labels`. `CreateVolumeWithOptions`
  adds `Driver` and `DriverOpts`; `CreateVolume` stays as the two-argument
  wrapper its existing EFS callers use.
- There was **no volume inspection**, and `ListVolumes` filters on
  `overcast.managed=true`, so it cannot see a user's unmanaged pre-created
  volume. `VolumeExists` answers the `autoprovision: false` question directly —
  named for what the one caller needs rather than exposing a whole inspect
  payload nothing reads.

**Volume lifecycle was new work.** `docker.GC` handles containers only, and ECS
had no volume sweep. Volumes are provisioned in one pass *before* the container
loop (`provisionTaskVolumes`), which keeps `containerMounts` a pure function and
therefore unit-testable without a daemon. Removal hangs off the exit notifier's
`allStopped` edge — the single point where a task is known to be fully
stopped — rather than the four separate container-removal sites, with the
retry policy in note 4 above and a startup sweep
(`sweepOrphanedTaskVolumes`, wired in `SetDocker` after the container sweep)
as the backstop. The `overcast.ecs.volume-scope` label keeps both off shared
volumes; `ECS_KEEP_CONTAINERS` suppresses volume removal too, since a kept
container with its volume deleted is useless for the post-mortem it was kept
for.

A volume failure now maps to `ResourceInitializationError` in
`stoppedReasonFor` ([launch.go](../../internal/services/ecs/launch.go)), which
is the AWS code for it — checked before the `network` case, since a driver named
after a network filesystem would otherwise match that first.

## Layer 2 — hot reload via the volume-redirect tag — **done**

> **Landed.** `internal/services/ecs/hot_reload.go` (tag resolution, redirect,
> bind-error decoration), `internal/hostpath` (shared with Lambda),
> `RegisterTaskDefinition` tag storage on both wire paths, the unified flags,
> `hot_reload_test.go`, the config inheritance matrix, service docs on both
> sides, changelog fragment. Deltas from the plan:
>
> 1. **`redirectableVolume` is the rule the whole feature rests on**, and it is
>    narrower than "a volume with no configuration": only a volume that declares
>    *nothing* can be redirected. An EFS, Docker, or `host.sourcePath` volume
>    already names its own storage, so redirecting one would mean overriding
>    what the definition asked for rather than filling in what it deliberately
>    left open. Same reason the tag cannot invent a volume that is not declared.
> 2. **The flag is checked after a tag has asked for a redirect**, not before, so
>    the warning naming `OVERCAST_ECS_HOT_RELOAD` only reaches the user who
>    wanted it. Checking first would have made the feature silent for exactly
>    the person trying to turn it on.
> 3. **Bind-error decoration names every bind source, not the culprit**, because
>    the daemon does not say which mount it rejected. `decorateBindMountError`
>    covers `host.sourcePath` binds too, not just hot-reload ones.
> 4. **Lambda's error prefix changed** from "hot reload mount failed" to "bind
>    mount failed" now that the helper is shared. Nothing asserted the old text.
> 5. Tag storage is keyed on the **revision's** ARN, so a later revision that
>    names no tags has none rather than inheriting its predecessor's — which is
>    what AWS does, and what makes a redirect follow a `cdk deploy` cleanly.

The developer-machine story, gated exactly as Lambda's is.

**Server flags (decision 4 — unified).** One umbrella with per-service
overrides, so a developer turns on the inner loop once and does not learn a new
variable per compute service:

| Variable | Default | Meaning |
| --- | --- | --- |
| `OVERCAST_HOT_RELOAD` | `false` | Umbrella: enables host-path hot reload for every compute service |
| `OVERCAST_LAMBDA_HOT_RELOAD` | inherits umbrella | Explicit override for Lambda |
| `OVERCAST_ECS_HOT_RELOAD` | inherits umbrella | Explicit override for ECS |

The existing config helper already expresses this exactly, with no new
machinery, and the idiom is established — `ECS_DOCKER_SOCKET` already falls back
to the Lambda socket the same way
([config.go:1404](../../internal/config/config.go)):

```go
cfg.HotReload = envBool("OVERCAST_HOT_RELOAD", false)
cfg.LambdaHotReload = envBool("OVERCAST_LAMBDA_HOT_RELOAD", cfg.HotReload)
cfg.ECSHotReload = envBool("OVERCAST_ECS_HOT_RELOAD", cfg.HotReload)
```

`envBool` returns its fallback when the variable is unset and `false` when it is
explicitly `false`/`0`/`no` ([config.go:1535](../../internal/config/config.go)),
so "inherit unless explicitly set" — including *opting one service out* of an
umbrella `true` — falls out for free. **`OVERCAST_LAMBDA_HOT_RELOAD` keeps its
exact current behaviour**, so this is additive: no deprecation, no migration
note, and every existing setup keeps working.

**Tag:** on the task definition, one per redirected volume:

```
overcast:hot-reload-path/<volume-name> = <absolute host path>
```

The volume name lives in the key (tag keys permit `/`), so multiple volumes need
no value-side syntax — which matters because Windows paths contain `:` and would
make any `<path>:<container-path>` value format ambiguous. The container path,
`readOnly`, and which containers mount it all come from the task definition's
own `mountPoints`, where AWS already puts them.

**Bare-key shorthand (decision 3).** `overcast:hot-reload-path` with no suffix
applies to the task definition's single redirectable volume. This is the common
case — one application, one source tree — and it makes the ECS tag *identical to
the one Lambda already uses*, which is the real argument for it: a developer
carries one tag name across both services and only reaches for the suffix when a
task genuinely has several volumes. Resolution rules, in order:

1. If any suffixed key is present, suffixed keys win and the bare key is ignored
   entirely (one WARN if a bare key was set, so the precedence is never silent).
2. Otherwise the bare key applies to the sole name-only volume.
3. If the bare key is set and the task definition has **no** redirectable volume,
   or **more than one**, nothing is redirected and a WARN names the volumes and
   prints the suffixed keys the user should have written. The task still runs on
   plain scratch volumes — a mistyped convenience tag is not worth failing a
   placement over, and the warning says exactly what to fix.

> **Interaction worth knowing before writing the guide:** the recommended
> Laravel layout declares *extra* name-only volumes to overlay `vendor/` and
> `storage/` (see the traps below), so a realistic task definition has three
> redirectable volumes and rule 3 applies — the shorthand does not help the very
> example the guide leads with. That is the correct outcome (the overlays must
> not be redirected), but it means the shorthand is a convenience for
> single-volume tasks and the guide's worked example uses the suffixed form.
> Rule 3's warning is what turns that from a puzzle into a one-line fix.

At placement, `containerMounts` resolves the task definition's tags (by ARN,
from the tag store): a name-only volume whose tag matches and whose flag is on
becomes `Type: "bind"` to the normalized host path instead of a scratch volume.
Flag off, or no tag → plain scratch volume, exactly what real AWS would run.
Tag present but flag off → the scratch volume still mounts (the task is not
broken) plus one WARN naming the flag, following the `efsMountSkipReason`
pattern ([handler_tasks.go:486](../../internal/services/ecs/handler_tasks.go)):
what was skipped, why, and what enables it.

**Shared plumbing with Lambda** — the two hard parts are already solved in
[hot_reload.go](../../internal/services/lambda/hot_reload.go) and must not be
written twice. Lift into a shared package (`internal/hostpath`, or
`internal/docker` next to `Mount`):

- `normalizeHotReloadPath` → `Normalize`: absolute-path check plus the Windows
  drive-letter rewrite (`C:\Users\dev\app` → `/c/Users/dev/app`).
- `decorateHotReloadMountError` → `DecorateMountError`: turns Docker's
  `mounts denied` / `invalid mount config` / `bind source path does not exist`
  into a message pointing at Docker Desktop's File Sharing settings.

On the ECS side the decorated error flows through `containerFailure`, so it
lands in the task's `stoppedReason` as `CannotStartContainerError: …`
([launch.go:215](../../internal/services/ecs/launch.go)) — where a user actually
looks when a task will not start. And with `Type: "bind"` the daemon **rejects a
missing source path** rather than creating it, which is the right behaviour: a
typo'd path that silently mounts an empty directory over `/var/www/html`
produces a framework 500 and no clue; a task stopped with the path named in its
reason explains itself.

**Optional hardening:** `ECS_HOST_MOUNT_ROOT` confines accepted paths (tag and
EC2 `sourcePath` both) to a prefix, for anyone running Overcast on a
non-loopback bind. Note the precedent before over-rotating: container
definitions already honour `privileged`
([handler_tasks.go:381](../../internal/services/ecs/handler_tasks.go)), so the
flag-off default is the real boundary and this is defence in depth.

## Layer 3 — the guide is a deliverable, not an afterthought — **done**

> **Landed.** `docs/local-dev.md` ("The inner loop"), linked from the docs index
> and cross-linked with both service pages; the ECS and Lambda sections shipped
> with Layers 1–2. One correction found while writing it: **published-doc
> anchors use the web UI's `slug()`**, which collapses a run of
> non-alphanumerics to a *single* hyphen, so an em-dashed heading is
> `#hot-reload-editing-local-source-inside-a-task`. The double-hyphen form is
> GitHub's, and is correct in `docs/plans/` and `docs/dev/` — which are read
> there — but wrong in published docs. `internal/router/advisories.go` already
> used the single-hyphen form and is the precedent. Two links were fixed, one of
> them introduced by Layer 2; the same mistake elsewhere in published docs
> predates this work and is raised separately.

The feature is only done when a developer can go from "my Laravel app deploys to
ECS" to "my save is live on the next request" from the docs alone.

1. **`docs/services/ecs.md`** gains two sections beside § EFS volumes:
   - **Host and scratch volumes** — the Layer 1 semantics: what each volume
     shape does, the Fargate rejection, EC2 `sourcePath` meaning
     daemon-host path.
   - **Hot reload** — the flag + tag mechanism, cross-linked both ways with
     Lambda's § Hot Reload so the two read as one feature. Configuration
     Reference rows for `OVERCAST_HOT_RELOAD` and `OVERCAST_ECS_HOT_RELOAD`;
     Lambda's existing row gains the "inherits umbrella" default.
2. **A new published guide — `docs/local-dev.md`, "The inner loop: live code
   inside emulated compute"** — the cohesive story the individual service pages
   cannot carry: when to use `cdk watch` (works today, every runtime), Lambda
   hot reload, ECS hot reload, and a full worked example. Outline below.
3. `capabilities_dev.go`: extend `RegisterTaskDefinition`'s Notes to name host,
   scratch, and tag-redirected volumes — **the doc op-table is generated from
   that file**; never hand-edit the table rows.
4. Both published pages → `make docs-index`, commit the result. `.changelog/`
   fragment required (shipped behaviour).

### The worked example (guide content, abridged)

Everything local stays behind one guard, so the production synth is untouched
— and if the guard ever fails, the leaked artifact still deploys (see Design
principle):

```typescript
const taskDef = new ecs.FargateTaskDefinition(this, "AppTask", {
  cpu: 512,
  memoryLimitMiB: 1024,
});

const app = taskDef.addContainer("app", {
  image: ecs.ContainerImage.fromAsset("./docker/php-fpm"),
  logging: ecs.LogDrivers.awsLogs({ streamPrefix: "app" }),
  portMappings: [{ containerPort: 8000 }],
});

if (process.env.OVERCAST_LOCAL === "true") {
  // Plain scratch volumes — all Fargate-legal. The tag redirects app-src to the
  // working tree; vendor and storage stay scratch, so the image's vendor/ and a
  // writable storage/ shadow the host tree at those paths.
  taskDef.addVolume({ name: "app-src" });
  taskDef.addVolume({ name: "app-vendor" });
  taskDef.addVolume({ name: "app-storage" });

  app.addMountPoints(
    { sourceVolume: "app-src", containerPath: "/var/www/html", readOnly: false },
    { sourceVolume: "app-vendor", containerPath: "/var/www/html/vendor", readOnly: false },
    { sourceVolume: "app-storage", containerPath: "/var/www/html/storage", readOnly: false },
  );

  // Suffixed key: this task has three redirectable volumes, so the bare
  // `overcast:hot-reload-path` shorthand (single-volume tasks) does not apply.
  cdk.Tags.of(taskDef).add(
    "overcast:hot-reload-path/app-src",
    path.resolve(__dirname, ".."),
  );
}
```

```bash
OVERCAST_HOT_RELOAD=true overcast serve
```

```bash
OVERCAST_LOCAL=true AWS_ENDPOINT_URL=http://localhost:4566 npx cdk deploy
```

Then the traps, in the order they bite — this list is the difference between a
feature and a support queue:

- **OPcache.** Production PHP images ship `opcache.validate_timestamps=0`; your
  edits land in the container and are ignored. Ship a dev-only ini
  (`opcache.validate_timestamps=1`, `opcache.revalidate_freq=0`). The single
  most common "the mount isn't working" report when the mount is fine.
- **`vendor/` stays in the image** — the `app-vendor` volume above. A bind from
  a Windows/macOS host crosses a VM boundary and Composer's autoloader stats
  thousands of files per request; the scratch volume shadows the host's
  `vendor/` and copy-up populates it from the image. Same reasoning as
  [performance.md § Data dir placement](../performance.md#data-dir-placement--avoid-host-bind-mounts-on-docker-desktop).
  Re-run `composer install` in the image, not on the host, when dependencies
  change.
- **Writable paths** — the `app-storage` volume above, and `bootstrap/cache`
  the same way. Writes stay out of the working tree, and copy-up hands the
  directories to the image's `www-data` ownership, which also dissolves the
  uid-mismatch failure on native-Linux hosts. (Docker Desktop maps bind
  ownership permissively; Linux hosts do not.) The trade-off to state plainly:
  logs under `storage/logs` then live in the volume rather than the working
  tree — read them through the task's `awslogs` stream instead.
- **File watching.** inotify does not cross Docker Desktop's host boundary, and
  `php artisan queue:work` caches the whole application in memory and never sees
  an edit regardless. Use `queue:listen` locally; anything that watches needs
  polling.
- **Reaching the app.** Overcast publishes a host port only when a port mapping
  carries an explicit `hostPort`
  ([handler_tasks.go:353](../../internal/services/ecs/handler_tasks.go)), and
  Fargate mappings usually carry only `containerPort`. Front the service with
  `ApplicationLoadBalancedFargateService` and use the stack's `ServiceURL`
  output, which resolves to Overcast and forwards to the task
  ([ecs.md § Load balancers](../services/ecs.md#load-balancers)) — or add a
  `hostPort` under the same local guard.

## Alternatives considered

- **Flag-gated `host.sourcePath` on Fargate** (this plan's first draft).
  Rejected: the flag changes what `RegisterTaskDefinition` accepts, so local
  validation diverges from AWS and a template developed locally can fail its
  first real deploy — the exact failure an emulator exists to prevent. The tag
  keeps validation byte-identical to AWS in every configuration.
- **Server-side mount overlay** (an Overcast config mapping task family →
  container → host path, template completely untouched — the
  Tilt/Skaffold/compose-override shape). The purest fidelity of all, and the
  volume declaration wouldn't need a CDK guard. Deferred, not rejected: it adds
  a new configuration surface, the mapping is invisible from the template, and
  the tag already gets the template delta down to a handful of guarded lines.
  If a future need arises (e.g. teams that cannot modify the CDK app), it can
  layer on top of the same `containerMounts` branch without rework.
- **The EFS named-volume hack** — works **today** with no code change, kept
  here as the bridge: EFS live mode backs each file system with a named Docker
  volume (`overcast-efs-<fsID>`,
  [live_volumes.go:23](../../internal/services/efs/live_volumes.go)), and
  Docker's `local` driver can create a named volume that is itself a bind. ECS
  resolves volumes by name and never learns the difference.

  ```bash
  aws --endpoint-url http://localhost:4566 efs create-file-system --creation-token laravel-src
  docker volume rm overcast-efs-<fsID>
  docker volume create --driver local \
    --opt type=none --opt o=bind --opt device=/run/desktop/mnt/host/f/dev/myapp \
    --label overcast.managed=true --label overcast.service=efs \
    --label overcast.resource-id=<fsID> \
    overcast-efs-<fsID>
  ```

  Then a normal `efsVolumeConfiguration` on the task definition. Sharp edges:
  `device=` is a path inside the daemon's VM, not a host path (on WSL2 a
  Windows drive is at `/run/desktop/mnt/host/<drive>/…` — probe with
  `docker run --rm -v /run/desktop/mnt/host/f:/probe alpine ls /probe/dev`);
  the three labels must match `docker.ManagedLabels("efs", fsID)` exactly or
  startup reconciliation sweeps the volume as an orphan
  ([live_volumes.go:196](../../internal/services/efs/live_volumes.go));
  deleting the file system deletes the volume. Unblocks today; not the answer —
  it also changes the task definition (EFS where prod has none), so it loses
  even the fidelity argument.

## Testing plan

- **Unit, no Docker — done.** `TestContainerMounts` asserts all nine emitted
  mounts positionally (type, source, target, `ReadOnly`, subpath) across every
  branch, plus `TestContainerMounts_withoutEFSResolver` pinning that host,
  scratch and Docker volumes survive when no EFS resolver is wired — the case
  that used to short-circuit the whole function. `efs_binds_test.go` became
  [volumes_test.go](../../internal/services/ecs/volumes_test.go); its
  `{Name: "host-vol"}` volume asserted a *skip* and now asserts a mount, which
  is the regression signal that was wanted.
- **Unit — done.** Validation: Fargate + `sourcePath` rejected with AWS's
  message asserted **verbatim** (the fidelity invariant, and the one assertion
  that encodes the whole design principle); Fargate +
  `dockerVolumeConfiguration`; `scope: task` + `autoprovision: true`; unknown
  scope; mutually exclusive configs; and the empty-`host`-block cases in both
  directions. Still outstanding: tags decoded and stored by
  `RegisterTaskDefinition` on both wire paths (Layer 2).
- **Unit.** Tag resolution: suffixed key; bare key with exactly one redirectable
  volume; bare key with zero and with several (WARN, no redirect, task still
  placed); suffixed-wins-over-bare precedence.
- **Unit.** Flag inheritance: umbrella on with no override; umbrella on with a
  service explicitly `false`; umbrella off with a service explicitly `true`;
  neither set. Cheap, and it pins the one behaviour a future `envBool` refactor
  could silently break.
- **Unit.** Path normalization and error decoration, running once in the shared
  package (Lambda's `hot_reload_test.go` cases move with the code).
- **Docker-gated.** A real `RunTask` with a tag-redirected volume: file written
  on the host is readable in the container, an edit is visible without
  restarting the task, and a missing source path stops the task with the path
  named in `stoppedReason`. Follow the fake-daemon rig in
  [docker_fake_test.go](../../internal/services/ecs/docker_fake_test.go) rather
  than `SkipWithoutDocker`, which never wires a daemon on Windows and skips
  silently — a test written that way debuts in CI.
- **Docker-gated.** Volume lifecycle: a task-scoped volume is gone after the
  task stops; a shared-scope volume **survives** it; an `autoprovision: false`
  volume that does not exist fails the task with the volume named; a user's
  pre-created volume is adopted and left unlabelled. The survival case is the
  one worth guarding — an over-eager sweep deleting a developer's data is the
  expensive bug in this plan.
- **Compat.** The scratch-volume, Docker-volume, and Fargate-rejection
  behaviours are host-independent and belong in the CDK/SDK compat suites; the
  hot-reload bind itself is host-path-dependent and stays in the docker-gated
  Go test.

## Decisions

Recorded 2026-08-12, resolving this plan's open questions.

1. **Scratch-volume lifetime — per-task named volumes.** Labelled and therefore
   sweepable; Docker's anonymous volumes would be invisible to the label-based
   GC. Carries the `overcast.ecs.volume-scope` label that keeps the sweep off
   shared-scope volumes.
2. **`dockerVolumeConfiguration` is in scope for this change.** The type switch
   in `containerMounts` gets written once rather than twice, and it completes
   AWS's volume model in one pass. It brings the two `internal/docker` additions
   (driver options on create, `InspectVolume`) and the volume-lifecycle work
   listed in Layer 1 — the honest cost of the decision, and still cheaper than a
   second pass. It also lands an AWS-native host-path route for EC2-launch-type
   task definitions (`scope: shared` + `autoprovision: false` adopting a
   pre-created bind-backed volume) that needs no Overcast flag at all.
3. **Bare-key shorthand is in.** `overcast:hot-reload-path` with no suffix
   applies to a task definition's sole redirectable volume, making the ECS tag
   identical to Lambda's for the common case; suffixed keys win where present,
   and an ambiguous bare key warns with the exact keys to write instead. Note
   the interaction flagged in Layer 2: the recommended Laravel layout has three
   volumes, so the guide's own example uses the suffixed form.
4. **Flags unified under `OVERCAST_HOT_RELOAD`,** with
   `OVERCAST_LAMBDA_HOT_RELOAD` and `OVERCAST_ECS_HOT_RELOAD` as per-service
   overrides. Purely additive — existing Lambda setups are unaffected — and it
   needs no new config machinery, just `envBool`'s existing fallback semantics.

## Follow-ups noted, not taken

- **Server-side mount overlay** — see Alternatives considered above; deferred,
  and it layers onto the same `containerMounts` branch without rework.
- **`configuredAtLaunch` volumes** — the fourth wrinkle in AWS's volume model
  (volume configuration supplied at `RunTask` rather than in the task
  definition). Untouched here; the branch structure takes it the same way it
  takes `dockerVolumeConfiguration`.
