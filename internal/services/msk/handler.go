package msk

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

const (
	// The two members AWS requires on a provisioned cluster and Overcast lets
	// a caller omit. Both create paths default to the same values.
	defaultKafkaVersion = "3.5.1"
	defaultBrokerNodes  = 3

	clusterTypeProvisioned = "PROVISIONED"
	clusterTypeServerless  = "SERVERLESS"

	// maxClustersPerPage is the MaxResults ceiling AWS documents for MSK's list
	// operations. It comes from the API reference, not from the pinned model —
	// this repo vendors no Smithy source to read a range trait out of. No
	// default is documented, so the ceiling doubles as one.
	maxClustersPerPage = 100
)

// errBadRequest builds MSK's 400. The kafka model defines no
// ValidationException: BadRequestException is the only client-error shape its
// operations declare for a malformed request.
func errBadRequest(format string, args ...any) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BadRequestException",
		Message:    fmt.Sprintf(format, args...),
		HTTPStatus: http.StatusBadRequest,
	}
}

// mskTagCfg tunes the shared tag validator to MSK's error shape (#1052).
// MSK reports every other rejected-input case this package models the same
// way (errBadRequest), and declares no dedicated tag-count exception, so the
// 50-tag limit is reported the same way an invalid key or value is.
var mskTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "BadRequestException",
	InvalidCode:     "BadRequestException",
	ExceededMessage: "You have exceeded the number of tags allowed for a resource.",
}

// errConflict builds MSK's 409. The pinned kafka model gives ConflictException
// @httpError(409) and declares it on CreateCluster, CreateClusterV2 and
// CreateConfiguration alike; the API reference spells the condition out on
// POST /v1/clusters and POST /v1/configurations: "This cluster name already
// exists. Retry your request using another name".
func errConflict(format string, args ...any) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ConflictException",
		Message:    fmt.Sprintf(format, args...),
		HTTPStatus: http.StatusConflict,
	}
}

// nameLockKey scopes a create's name-uniqueness lock to one kind of resource in
// one region. AWS scopes both MSK names — cluster and configuration — to an
// account and a region, so the same name in two regions is two resources and
// must not serialise against each other. Overcast serves one account, so the
// account half of that scope is implicit in the store.
func nameLockKey(kind, region, name string) string {
	return kind + "/" + serviceutil.RegionKey(region, name)
}

// claimClusterName holds this region's claim on a cluster name for the rest of
// the create, and refuses the create if a cluster already holds it.
//
// The lock matters and the per-record lock in locks.go cannot stand in for it:
// that one is keyed by ARN, and two creates racing over one name mint two
// different ARNs, so they would never meet. Between an unlocked check and the
// store write, both creates see a free name and both write — which is the
// duplicate the guard exists to prevent, arrived at by a different route.
//
// The caller must call the returned release once the record is stored.
func (h *Handler) claimClusterName(ctx context.Context, name string) (func(), *protocol.AWSError) {
	release := h.nameLocks.Lock(nameLockKey("cluster", middleware.RegionFromContext(ctx, h.cfg.Region), name))
	all, aerr := h.store.listClusters(ctx)
	if aerr != nil {
		release()
		return nil, aerr
	}
	for _, c := range all {
		// A cluster on its way out still holds its name: DeleteCluster answers
		// immediately and drops the record afterwards, and until it is gone the
		// cluster exists — which is what AWS reports too.
		if c.ClusterName == name {
			release()
			return nil, errConflict("This cluster name already exists. Retry your request using another name.")
		}
	}
	return release, nil
}

// claimConfigurationName is claimClusterName for configuration names, which AWS
// scopes the same way and rejects a repeat of the same way.
func (h *Handler) claimConfigurationName(ctx context.Context, name string) (func(), *protocol.AWSError) {
	release := h.nameLocks.Lock(nameLockKey("configuration", middleware.RegionFromContext(ctx, h.cfg.Region), name))
	all, aerr := h.store.listConfigurations(ctx)
	if aerr != nil {
		release()
		return nil, aerr
	}
	for _, c := range all {
		if c.Name == name {
			release()
			return nil, errConflict("This configuration name already exists. Retry your request using another name.")
		}
	}
	return release, nil
}

// arnFromPath extracts an ARN from a chi path parameter, percent-decoding it.
//
// AWS models every MSK ARN path member as a non-greedy httpLabel, which the
// SDKs percent-encode whole — ':' as %3A and '/' as %2F — so the ARN arrives as
// one path segment. smithy-go's httpbinding.EscapePath escapes the separator
// for a non-greedy label, and botocore's percent_encode and the JS SDK's
// extendedEncodeURIComponent do the same. chi matches against URL.RawPath when
// the request carries one, so the parameter holds the encoded form and this
// decodes it; for a caller that sends literal slashes instead the decode is a
// no-op.
//
// It serves the routes whose whole tail is the ARN: the tag router's, and the
// v2 cluster label. The subtrees that hang a sub-resource off the ARN cannot
// use it, because there the tail is not an ARN — see modeledOperation in
// dispatch.go.
func arnFromPath(r *http.Request, param string) (string, error) {
	raw := chi.URLParam(r, param)
	if raw == "" {
		return "", fmt.Errorf("missing ARN in path")
	}
	return url.PathUnescape(raw)
}

// provisionedInput is the broker configuration of a provisioned cluster.
// CreateCluster carries these members at the top level of its request and
// CreateClusterV2 nests the same three under `provisioned`, so the defaulting
// lives here rather than in each handler.
type provisionedInput struct {
	KafkaVersion        string              `json:"kafkaVersion"`
	NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes"`
	BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo"`
}

// applyTo writes the broker configuration onto a cluster record.
func (p *provisionedInput) applyTo(c *Cluster) {
	c.KafkaVersion = p.KafkaVersion
	if c.KafkaVersion == "" {
		c.KafkaVersion = defaultKafkaVersion
	}
	c.NumberOfBrokerNodes = serviceutil.DefaultInt(p.NumberOfBrokerNodes, defaultBrokerNodes)
	c.BrokerNodeGroupInfo = p.BrokerNodeGroupInfo
}

// mskARN mints an MSK ARN in the request's region. resource is everything after
// the account ID, e.g. "cluster/my-cluster/<uuid>".
func (h *Handler) mskARN(ctx context.Context, resource string) string {
	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	return fmt.Sprintf("arn:aws:kafka:%s:%s:%s", region, h.cfg.AccountID, resource)
}

// newCluster builds the record both create paths store — everything that does
// not depend on whether the caller used the v1 or the v2 request shape.
func (h *Handler) newCluster(ctx context.Context, name string) *Cluster {
	id := uuid.NewString()
	return &Cluster{
		ClusterArn:     h.mskARN(ctx, "cluster/"+name+"/"+id),
		ClusterName:    name,
		State:          "CREATING",
		CreationTime:   h.clk.Now(),
		CurrentVersion: id,
	}
}

// saveCreationTags persists the tags a create request carried into the shared
// tag namespace — the one TagResource writes and ListTagsForResource reads.
//
// Both create paths used to hang them off the cluster record instead, where no
// tag operation could see them: the call was accepted, the tags were stored,
// and ListTagsForResource reported the cluster as untagged. The same "answered
// 200, kept nothing a caller can read" shape as #980.
func (h *Handler) saveCreationTags(ctx context.Context, resourceArn string, tags map[string]string) *protocol.AWSError {
	if len(tags) == 0 {
		return nil
	}
	// Validated here too, in addition to the create handlers' own earlier
	// check (#1052) — callers besides CreateCluster/CreateClusterV2 may add
	// tags through this entry point later, and this is the one place all of
	// them funnel through before a write.
	if aerr := serviceutil.ValidateTags(mskTagCfg, tags); aerr != nil {
		return aerr
	}
	return h.store.tags().Save(ctx, resourceArn, tags)
}

// clusterTags reads a cluster's tags for a response body. Both cluster views
// carry a `tags` member, and it has to answer the same question
// ListTagsForResource does.
func (h *Handler) clusterTags(ctx context.Context, resourceArn string) (map[string]string, *protocol.AWSError) {
	return h.store.tags().Load(ctx, resourceArn)
}

// startClusterAsync starts a cluster's Redpanda container in the background.
//
// It reports whether a broker is on its way, which is what decides who owns the
// cluster's state from here. True means a start is running or already running,
// and the readiness watch that start schedules is the only thing entitled to
// move the cluster out of CREATING — a create that promoted it itself would
// make that watch a no-op, since it only ever moves a cluster out of CREATING.
// False means nothing is coming at all, and a caller that leaves the cluster in
// CREATING has parked it in a state nothing will ever change; see
// settleClusterWithoutBroker for what to do instead.
//
// The reconcile and Docker-event callers only run with a daemon wired, so the
// false case belongs to the create paths.
func (h *Handler) startClusterAsync(clusterARN string) bool {
	h.dockerLifecycle.Lock()
	defer h.dockerLifecycle.Unlock()
	if h.dockerStopping || !h.dockerReady.Load() {
		return false
	}
	if _, loaded := h.dockerStarts.LoadOrStore(clusterARN, struct{}{}); loaded {
		// A start is already in flight and owns the transition.
		return true
	}
	if h.puller != nil {
		h.puller.Prewarm(redpandaImage)
	}
	h.dockerWg.Add(1)
	go func() {
		defer h.dockerWg.Done()
		defer h.dockerStarts.Delete(clusterARN)
		ctx := clusterRegionCtx(clusterARN)
		if err := h.startClusterContainer(ctx, clusterARN); err != nil {
			// The caller was told a broker was coming — this function returned
			// true — and the readiness watch that would have owned the cluster
			// from here is only scheduled once the container is up. So a
			// failure here leaves nothing behind that could ever move the
			// cluster out of CREATING; the failure has to be recorded on the
			// record itself. failCluster's own guard decides whether this
			// cluster is one that may still be settled: a reconcile or
			// Docker-event start for a cluster that is already ACTIVE is left
			// alone, because ACTIVE is not in clusterAwaiting.
			h.failCluster(ctx, clusterARN, fmt.Sprintf("the broker container could not be started: %v", err))
		}
	}()
	return true
}

// clusterTypeOf reports a stored cluster's type. CreateCluster (v1) creates
// only provisioned clusters and does not record a type, so an unset value means
// provisioned rather than unknown.
func clusterTypeOf(c *Cluster) string {
	if c.ClusterType == "" {
		return clusterTypeProvisioned
	}
	return c.ClusterType
}

// matchesNameFilter applies MSK's clusterNameFilter, which AWS documents as a
// prefix over the cluster name rather than an exact match.
func matchesNameFilter(c *Cluster, filter string) bool {
	return filter == "" || strings.HasPrefix(c.ClusterName, filter)
}

// ── createCluster ─────────────────────────────────────────────────────────────

func (h *Handler) createCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName      string            `json:"clusterName"`
		Tags             map[string]string `json:"tags"`
		provisionedInput                   // promoted: v1 carries these at the top level
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ClusterName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("clusterName is required"))
		return
	}
	// Request-shape validation before the name claim resolves against the
	// store — the same ordering createLogGroupTyped uses
	// (internal/services/cloudwatch/logs/typed_logic.go) — so a rejected
	// create must not leave a cluster behind with nothing able to fix its
	// tags (#1052). saveCreationTags below validates again on the merged
	// path other callers may reach, but by then the cluster already exists.
	if aerr := serviceutil.ValidateTags(mskTagCfg, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	release, aerr := h.claimClusterName(r.Context(), req.ClusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	defer release()

	cluster := h.newCluster(r.Context(), req.ClusterName)
	req.provisionedInput.applyTo(cluster)

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.saveCreationTags(r.Context(), cluster.ClusterArn, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if !h.startClusterAsync(cluster.ClusterArn) {
		h.settleClusterWithoutBroker(cluster.ClusterArn)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":  cluster.ClusterArn,
		"clusterName": cluster.ClusterName,
		"state":       cluster.State,
	})
}

// ── listClusters ──────────────────────────────────────────────────────────────

func (h *Handler) listClusters(w http.ResponseWriter, r *http.Request) {
	nameFilter := r.URL.Query().Get("clusterNameFilter")
	all, aerr := h.store.listClusters(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	result := make([]map[string]any, 0, len(all))
	for _, c := range all {
		if !matchesNameFilter(c, nameFilter) {
			continue
		}
		tags, aerr := h.clusterTags(r.Context(), c.ClusterArn)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		result = append(result, clusterV1View(c, tags))
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfoList": result,
		"nextToken":       nil,
	})
}

// ── describeCluster ───────────────────────────────────────────────────────────

func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request, clusterArn string) {
	if clusterArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	tags, aerr := h.clusterTags(r.Context(), cluster.ClusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfo": clusterV1View(cluster, tags),
	})
}

// clusterV1View renders a stored cluster as the v1 API's ClusterInfo — the
// shape DescribeCluster returns as `clusterInfo` and ListClusters as each
// element of `clusterInfoList`.
//
// AWS binds no top-level kafkaVersion member on ClusterInfo, so
// currentBrokerSoftwareInfo.kafkaVersion is the only member an SDK client can
// read the version back from. Both operations used to marshal the stored record
// straight onto the wire, which put the version under a name no generated
// client reads — a cluster created at 3.5.1 described itself as running
// nothing — and put the emulator's own Docker bookkeeping in the response
// alongside it.
func clusterV1View(c *Cluster, tags map[string]string) map[string]any {
	view := map[string]any{
		"clusterArn":          c.ClusterArn,
		"clusterName":         c.ClusterName,
		"state":               c.State,
		"creationTime":        c.CreationTime,
		"currentVersion":      c.CurrentVersion,
		"numberOfBrokerNodes": c.NumberOfBrokerNodes,
		"brokerNodeGroupInfo": c.BrokerNodeGroupInfo,
		"currentBrokerSoftwareInfo": map[string]any{
			"kafkaVersion": c.KafkaVersion,
		},
	}
	if len(tags) > 0 {
		view["tags"] = tags
	}
	return view
}

// ── deleteCluster ─────────────────────────────────────────────────────────────

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request, clusterArn string) {
	if clusterArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.mutateCluster(r.Context(), clusterArn, func(cluster *Cluster) *protocol.AWSError {
		cluster.State = "DELETING"
		return nil
	})
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	clusterARNCopy := clusterArn
	h.scheduler.CancelScoped(serviceutil.ARNRegion(clusterArn), clusterArn, "health")

	id := cluster.DockerContainerID
	if h.gc != nil && id != "" {
		h.gc.StopNow(id)
		h.gc.ScheduleRemove(id)
	}
	if cluster.HostPort > 0 {
		_ = h.store.releasePort(r.Context(), cluster.HostPort) //nolint:errcheck
	}

	h.scheduler.AfterScoped(serviceutil.ARNRegion(clusterArn), clusterArn, "delete", 50*time.Millisecond, func(ctx context.Context) {
		if aerr := h.store.deleteCluster(ctx, clusterARNCopy); aerr != nil {
			h.log.Warn("failed to delete MSK cluster record", zap.String("cluster", clusterARNCopy), zap.Error(aerr))
		}
		// The tags outlive the record otherwise, and ListTagsForResource would
		// keep answering for an ARN that no longer names anything.
		if aerr := h.store.deleteTags(ctx, clusterARNCopy); aerr != nil {
			h.log.Warn("failed to delete MSK cluster tags", zap.String("cluster", clusterARNCopy), zap.Error(aerr))
		}
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn": clusterArn,
		"state":      "DELETING",
	})
}

// ── getBootstrapBrokers ───────────────────────────────────────────────────────

func (h *Handler) getBootstrapBrokers(w http.ResponseWriter, r *http.Request, clusterArn string) {
	if clusterArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"bootstrapBrokerString":    h.bootstrapBrokerString(r.Context(), cluster),
		"bootstrapBrokerStringTls": "",
	})
}

// bootstrapBrokerString is the address this caller should dial for cluster.
//
// Both halves are per-caller, on the same rule every other data-plane endpoint
// follows (docs/networking.md § Data-plane endpoints):
//
//   - A sibling container resolves the bootstrap hostname to the broker
//     container through Docker's embedded resolver — the alias set
//     clusterEndpointAliases registers — and dials 9092, as on AWS.
//   - The host reaches the same container only through its published port
//     binding, and is given a loopback address when the base carries no
//     wildcard DNS, since *.localhost does not resolve on Windows.
//
// Before this was per-caller it returned ExternalHostname():hostPort to
// everyone, which inside any container resolves to Overcast — a process that
// serves no Kafka on that port.
func (h *Handler) bootstrapBrokerString(ctx context.Context, cluster *Cluster) string {
	if cluster == nil || cluster.HostPort == 0 {
		if !h.dockerReady.Load() {
			return net.JoinHostPort("127.0.0.1", strconv.Itoa(h.cfg.MSKPortBase))
		}
		return ""
	}

	base := serviceutil.ClientBaseURLFromOrigin(h.cfg, middleware.ClientEndpointFromContext(ctx))
	host := h.cfg.ExternalHostname()
	if u, err := url.Parse(base); err == nil {
		if parsed := u.Hostname(); parsed != "" && net.ParseIP(parsed) == nil {
			host = parsed
		}
	}
	name := bootstrapHostname(clusterNameFromARN(cluster.ClusterArn), serviceutil.ARNRegion(cluster.ClusterArn), host)

	if serviceutil.CallerIsSiblingContainer(middleware.ClientAddrFromContext(ctx)) {
		return net.JoinHostPort(name, strconv.Itoa(kafkaPort))
	}
	if serviceutil.SupportsHostRouting("http://" + host) {
		return net.JoinHostPort(name, strconv.Itoa(cluster.HostPort))
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(cluster.HostPort))
}

// ── createConfiguration ───────────────────────────────────────────────────────

func (h *Handler) createConfiguration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name             string   `json:"name"`
		Description      string   `json:"description"`
		KafkaVersions    []string `json:"kafkaVersions"`
		ServerProperties string   `json:"serverProperties"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		protocol.WriteJSONError(w, r, errBadRequest("name is required"))
		return
	}

	release, aerr := h.claimConfigurationName(r.Context(), req.Name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	defer release()

	id := uuid.NewString()
	arn := h.mskARN(r.Context(), "configuration/"+req.Name+"/"+id)
	now := h.clk.Now()

	cfg := &ClusterConfiguration{
		Arn:           arn,
		Name:          req.Name,
		Description:   req.Description,
		KafkaVersions: req.KafkaVersions,
		CreationTime:  now,
		LatestRevision: Revision{
			CreationTime: now,
			Revision:     1,
		},
		State: "ACTIVE",
	}

	if aerr := h.store.putConfiguration(r.Context(), cfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"arn":          arn,
		"creationTime": now,
		"latestRevision": map[string]any{
			"creationTime": now,
			"revision":     1,
		},
		"name":  req.Name,
		"state": "ACTIVE",
	})
}

// ── listConfigurations ────────────────────────────────────────────────────────

func (h *Handler) listConfigurations(w http.ResponseWriter, r *http.Request) {
	all, aerr := h.store.listConfigurations(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"configurations": all,
	})
}

// ── describeConfiguration ─────────────────────────────────────────────────────

func (h *Handler) describeConfiguration(w http.ResponseWriter, r *http.Request, configArn string) {
	if configArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid configArn"))
		return
	}
	cfg, aerr := h.store.getConfiguration(r.Context(), configArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, cfg)
}

// ── deleteConfiguration ───────────────────────────────────────────────────────

func (h *Handler) deleteConfiguration(w http.ResponseWriter, r *http.Request, configArn string) {
	if configArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid configArn"))
		return
	}
	if _, aerr := h.store.getConfiguration(r.Context(), configArn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteConfiguration(r.Context(), configArn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ── listKafkaVersions ─────────────────────────────────────────────────────────

func (h *Handler) listKafkaVersions(w http.ResponseWriter, r *http.Request) {
	type kafkaVersion struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	versions := []kafkaVersion{
		{Status: "ACTIVE", Version: "3.6.0"},
		{Status: "ACTIVE", Version: "3.5.1"},
		{Status: "ACTIVE", Version: "3.4.0"},
		{Status: "ACTIVE", Version: "2.8.1"},
		{Status: "DEPRECATED", Version: "2.6.0"},
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"kafkaVersions": versions,
	})
}

// ── Tag operations ────────────────────────────────────────────────────────────

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid resourceArn"))
		return
	}
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	// ApplyStoreTags does the load-merge-validate-save in one call — the
	// merged set is what gets checked against the 50-tag limit, not just
	// this call's incoming delta (#1052).
	if _, aerr := serviceutil.ApplyStoreTags(r.Context(), h.store.tags(), resourceArn, req.Tags, mskTagCfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid resourceArn"))
		return
	}
	tagStore := h.store.tags()
	tags, aerr := tagStore.Load(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"tags": tags,
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid resourceArn"))
		return
	}
	tagKeysParam := r.URL.Query().Get("tagKeys")
	var tagKeys []string
	if tagKeysParam != "" {
		tagKeys = strings.Split(tagKeysParam, ",")
	}
	tagStore := h.store.tags()
	tags, aerr := tagStore.Load(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, k := range tagKeys {
		delete(tags, strings.TrimSpace(k))
	}
	if aerr := tagStore.Save(r.Context(), resourceArn, tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

// ── createClusterV2 ──────────────────────────────────────────────────────────

func (h *Handler) createClusterV2(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterName string            `json:"clusterName"`
		Tags        map[string]string `json:"tags"`
		Provisioned *provisionedInput `json:"provisioned"`
		Serverless  *struct{}         `json:"serverless"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ClusterName == "" {
		protocol.WriteJSONError(w, r, errBadRequest("clusterName is required"))
		return
	}
	// CreateClusterV2 carries the cluster's shape in exactly one of two
	// mutually exclusive members. Defaulting a request that names neither — as
	// this handler did while nothing could call it — invents a serverless
	// cluster the caller never asked for.
	if (req.Provisioned == nil) == (req.Serverless == nil) {
		protocol.WriteJSONError(w, r, errBadRequest("Exactly one of provisioned or serverless must be specified."))
		return
	}
	// See createCluster (v1) above for why this runs before the cluster
	// exists (#1052).
	if aerr := serviceutil.ValidateTags(mskTagCfg, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// v1 and v2 are two bindings onto one cluster namespace, so the guard is
	// the same one and reads the same records: a name a v1 create took is not
	// available to a v2 create, whichever type it asks for.
	release, aerr := h.claimClusterName(r.Context(), req.ClusterName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	defer release()

	cluster := h.newCluster(r.Context(), req.ClusterName)
	if req.Provisioned != nil {
		cluster.ClusterType = clusterTypeProvisioned
		req.Provisioned.applyTo(cluster)
	} else {
		// A serverless cluster has no brokers to provision, so there is nothing
		// to wait for and it is ACTIVE straight away.
		cluster.ClusterType = clusterTypeServerless
		cluster.State = "ACTIVE"
	}

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if aerr := h.saveCreationTags(r.Context(), cluster.ClusterArn, req.Tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if cluster.ClusterType == clusterTypeProvisioned {
		if !h.startClusterAsync(cluster.ClusterArn) {
			h.settleClusterWithoutBroker(cluster.ClusterArn)
		}
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":  cluster.ClusterArn,
		"clusterName": cluster.ClusterName,
		"clusterType": cluster.ClusterType,
		"state":       cluster.State,
	})
}

// ── listClustersV2 ────────────────────────────────────────────────────────────

func (h *Handler) listClustersV2(w http.ResponseWriter, r *http.Request) {
	// Every ListClustersV2 input is an httpQuery member — AWS binds none of
	// them to the body, and the request has none. Reading them from the body
	// would be the second half of #793.
	query := r.URL.Query()
	nameFilter := query.Get("clusterNameFilter")
	typeFilter := strings.ToUpper(query.Get("clusterTypeFilter"))

	all, aerr := h.store.listClusters(r.Context())
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	matched := make([]map[string]any, 0, len(all))
	for _, c := range all {
		if !matchesNameFilter(c, nameFilter) {
			continue
		}
		// clusterTypeFilter selects one type; "ALL" is its documented
		// everything value and an absent filter means the same.
		if typeFilter != "" && typeFilter != "ALL" && clusterTypeOf(c) != typeFilter {
			continue
		}
		tags, aerr := h.clusterTags(r.Context(), c.ClusterArn)
		if aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
		matched = append(matched, clusterV2View(c, tags))
	}

	page, err := serviceutil.Paginate(matched, serviceutil.QueryInt(r, "maxResults", 0), query.Get("nextToken"),
		serviceutil.PaginateOptions{DefaultLimit: maxClustersPerPage, MaxLimit: maxClustersPerPage})
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("The nextToken is not valid."))
		return
	}

	body := map[string]any{"clusterInfoList": page.Items}
	if page.NextToken != "" {
		body["nextToken"] = page.NextToken
	}
	protocol.WriteJSON(w, r, http.StatusOK, body)
}

// ── describeClusterV2 ─────────────────────────────────────────────────────────

// describeClusterV2 serves the modeled label route. The wildcard beside it
// resolves the ARN itself and calls describeClusterV2Arn directly.
func (h *Handler) describeClusterV2(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromPath(r, "clusterArn")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	h.describeClusterV2Arn(w, r, clusterArn)
}

func (h *Handler) describeClusterV2Arn(w http.ResponseWriter, r *http.Request, clusterArn string) {
	if clusterArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	tags, aerr := h.clusterTags(r.Context(), cluster.ClusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	docker.SetBackingHeaders(w, h.dockerReady.Load(), docker.ContainerHealthUnknown)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfo": clusterV2View(cluster, tags),
	})
}

// clusterV2View renders a stored cluster in the v2 API's shape. DescribeClusterV2's
// `clusterInfo` and every element of ListClustersV2's `clusterInfoList` are the
// same modeled Cluster structure, so both are shaped here.
func clusterV2View(c *Cluster, tags map[string]string) map[string]any {
	clusterType := clusterTypeOf(c)
	view := map[string]any{
		"clusterArn":     c.ClusterArn,
		"clusterName":    c.ClusterName,
		"clusterType":    clusterType,
		"state":          c.State,
		"creationTime":   c.CreationTime,
		"currentVersion": c.CurrentVersion,
	}
	if c.StateInfo != nil {
		view["stateInfo"] = c.StateInfo
	}
	if len(tags) > 0 {
		view["tags"] = tags
	}
	if clusterType == clusterTypeProvisioned {
		view["provisioned"] = map[string]any{
			"numberOfBrokerNodes": c.NumberOfBrokerNodes,
			"brokerNodeGroupInfo": c.BrokerNodeGroupInfo,
			"currentBrokerSoftwareInfo": map[string]any{
				"kafkaVersion": c.KafkaVersion,
			},
		}
	} else {
		view["serverless"] = map[string]any{}
	}
	return view
}

// ── updateClusterConfiguration ────────────────────────────────────────────────

func (h *Handler) updateClusterConfiguration(w http.ResponseWriter, r *http.Request, clusterArn string) {
	if clusterArn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}

	// The configuration reference is a modeled `configurationInfo` object of
	// {arn, revision}, and AWS marks it required. This read a flat
	// `configurationArn` until #996 caught it: no AWS client sends that member,
	// so the ARN was always empty, the check below it never ran, and an update
	// naming a configuration that does not exist was answered 200 — the caller
	// told its reference had been resolved when nothing had resolved it.
	var req struct {
		ConfigurationInfo *struct {
			Arn      string `json:"arn"`
			Revision int64  `json:"revision"`
		} `json:"configurationInfo"`
		CurrentVersion string `json:"currentVersion"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ConfigurationInfo == nil || req.ConfigurationInfo.Arn == "" {
		protocol.WriteJSONError(w, r, errBadRequest("configurationInfo is required"))
		return
	}

	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if req.CurrentVersion != "" && req.CurrentVersion != cluster.CurrentVersion {
		protocol.WriteJSONError(w, r, errBadRequest("currentVersion does not match the cluster's current version"))
		return
	}

	// Resolved before the cluster is touched, so a rejected update does not
	// spend the optimistic-concurrency token on a mutation that never happened.
	if _, aerr := h.store.getConfiguration(r.Context(), req.ConfigurationInfo.Arn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	opID := uuid.NewString()
	opArn := h.mskARN(r.Context(), "cluster-operation/"+opID)

	if _, aerr := h.mutateCluster(r.Context(), cluster.ClusterArn, func(stored *Cluster) *protocol.AWSError {
		stored.CurrentVersion = opID
		return nil
	}); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	cluster.CurrentVersion = opID

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":          clusterArn,
		"clusterOperationArn": opArn,
	})
}

// clusterRegionCtx returns a background context carrying the region embedded
// in the cluster ARN — the same region the store keys the cluster under. MSK
// background callbacks (container starts, health checks, Docker events) run
// outside any request context, where a bare context.Background() would
// resolve to the default region and miss clusters created elsewhere.
func clusterRegionCtx(clusterARN string) context.Context {
	return middleware.ContextWithRegion(context.Background(), serviceutil.ARNRegion(clusterARN))
}
