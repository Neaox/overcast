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

	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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

// arnFromPath extracts an ARN from a chi path parameter, percent-decoding it.
//
// AWS models every MSK ARN path member as a non-greedy httpLabel, which the
// SDKs percent-encode whole — ':' as %3A and '/' as %2F — so the ARN arrives as
// one path segment. smithy-go's httpbinding.EscapePath escapes the separator
// for a non-greedy label, and botocore's percent_encode and the JS SDK's
// extendedEncodeURIComponent do the same. chi matches against URL.RawPath when
// the request carries one, so the parameter holds the encoded form and this
// decodes it. A caller that sends literal slashes instead reaches the v1
// subtree wildcards, where the decode is a no-op.
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
func (h *Handler) newCluster(ctx context.Context, name string, tags map[string]string) *Cluster {
	id := uuid.NewString()
	return &Cluster{
		ClusterArn:     h.mskARN(ctx, "cluster/"+name+"/"+id),
		ClusterName:    name,
		State:          "CREATING",
		CreationTime:   h.clk.Now(),
		CurrentVersion: id,
		Tags:           tags,
	}
}

// startClusterAsync starts a cluster's Redpanda container in the background.
// Without Docker there is nothing to start and the cluster stays in CREATING,
// which is what a caller polling DescribeCluster sees on AWS while brokers are
// still being provisioned.
func (h *Handler) startClusterAsync(clusterARN string) {
	h.dockerLifecycle.Lock()
	defer h.dockerLifecycle.Unlock()
	if h.dockerStopping || !h.dockerReady.Load() {
		return
	}
	if _, loaded := h.dockerStarts.LoadOrStore(clusterARN, struct{}{}); loaded {
		return
	}
	if h.puller != nil {
		h.puller.Prewarm(redpandaImage)
	}
	h.dockerWg.Add(1)
	go func() {
		defer h.dockerWg.Done()
		defer h.dockerStarts.Delete(clusterARN)
		if err := h.startClusterContainer(clusterRegionCtx(clusterARN), clusterARN); err != nil {
			h.log.Warn("failed to start Docker container for MSK cluster — current lifecycle state is retained",
				zap.String("cluster", clusterARN), zap.Error(err))
		}
	}()
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

// ── clusterGetDispatch ────────────────────────────────────────────────────────

// clusterGetDispatch dispatches GET /v1/clusters/* to either getBootstrapBrokers
// (when the path ends in /bootstrap-brokers) or describeCluster.
func (h *Handler) clusterGetDispatch(w http.ResponseWriter, r *http.Request) {
	wild := chi.URLParam(r, "*")
	if strings.HasSuffix(wild, "/bootstrap-brokers") {
		h.getBootstrapBrokers(w, r)
		return
	}
	h.describeCluster(w, r)
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

	cluster := h.newCluster(r.Context(), req.ClusterName, req.Tags)
	req.provisionedInput.applyTo(cluster)

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	h.startClusterAsync(cluster.ClusterArn)

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
	result := make([]*Cluster, 0, len(all))
	for _, c := range all {
		if !matchesNameFilter(c, nameFilter) {
			continue
		}
		result = append(result, c)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfoList": result,
		"nextToken":       nil,
	})
}

// ── describeCluster ───────────────────────────────────────────────────────────

func (h *Handler) describeCluster(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfo": cluster,
	})
}

// ── deleteCluster ─────────────────────────────────────────────────────────────

func (h *Handler) deleteCluster(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromPath(r, "*")
	if err != nil {
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
	})

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn": clusterArn,
		"state":      "DELETING",
	})
}

// ── getBootstrapBrokers ───────────────────────────────────────────────────────

func (h *Handler) getBootstrapBrokers(w http.ResponseWriter, r *http.Request) {
	// clusterGetDispatch routed this off the /v1/clusters/* wildcard, so the
	// parameter still holds the whole tail — the ARN and the sub-resource.
	clusterArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	clusterArn = strings.TrimSuffix(clusterArn, "/bootstrap-brokers")
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

func (h *Handler) describeConfiguration(w http.ResponseWriter, r *http.Request) {
	configArn, err := arnFromPath(r, "*")
	if err != nil {
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

func (h *Handler) deleteConfiguration(w http.ResponseWriter, r *http.Request) {
	configArn, err := arnFromPath(r, "*")
	if err != nil {
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
	tagStore := &serviceutil.NSStore{Store: h.store.store, NS: nsTags}
	tags, aerr := tagStore.Load(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for k, v := range req.Tags {
		tags[k] = v
	}
	if aerr := tagStore.Save(r.Context(), resourceArn, tags); aerr != nil {
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
	tagStore := &serviceutil.NSStore{Store: h.store.store, NS: nsTags}
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
	tagStore := &serviceutil.NSStore{Store: h.store.store, NS: nsTags}
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

	cluster := h.newCluster(r.Context(), req.ClusterName, req.Tags)
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
	if cluster.ClusterType == clusterTypeProvisioned {
		h.startClusterAsync(cluster.ClusterArn)
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
		matched = append(matched, clusterV2View(c))
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

func (h *Handler) describeClusterV2(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromPath(r, "clusterArn")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	docker.SetBackingHeaders(w, h.dockerReady.Load(), docker.ContainerHealthUnknown)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfo": clusterV2View(cluster),
	})
}

// clusterV2View renders a stored cluster in the v2 API's shape. DescribeClusterV2's
// `clusterInfo` and every element of ListClustersV2's `clusterInfoList` are the
// same modeled Cluster structure, so both are shaped here.
func clusterV2View(c *Cluster) map[string]any {
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
	if len(c.Tags) > 0 {
		view["tags"] = c.Tags
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

// ── clusterPutDispatch ────────────────────────────────────────────────────────

// clusterPutDispatch dispatches PUT /v1/clusters/* based on the path suffix.
func (h *Handler) clusterPutDispatch(w http.ResponseWriter, r *http.Request) {
	wild := chi.URLParam(r, "*")
	if strings.HasSuffix(wild, "/configuration") {
		h.updateClusterConfiguration(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// ── updateClusterConfiguration ────────────────────────────────────────────────

func (h *Handler) updateClusterConfiguration(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromPath(r, "*")
	if err != nil {
		protocol.WriteJSONError(w, r, errBadRequest("invalid clusterArn"))
		return
	}
	clusterArn = strings.TrimSuffix(clusterArn, "/configuration")

	var req struct {
		ConfigurationArn      string `json:"configurationArn"`
		ConfigurationRevision int64  `json:"configurationRevision"`
		CurrentVersion        string `json:"currentVersion"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
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

	if req.ConfigurationArn != "" {
		if _, aerr := h.store.getConfiguration(r.Context(), req.ConfigurationArn); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
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
