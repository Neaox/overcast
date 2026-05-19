package msk

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// arnFromWildcard extracts the ARN from a chi wildcard URL param.
// The wildcard may be URL-encoded, so we PathUnescape it.
func arnFromWildcard(r *http.Request) (string, error) {
	wild := chi.URLParam(r, "*")
	if wild == "" {
		return "", fmt.Errorf("missing ARN in path")
	}
	return url.PathUnescape(wild)
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
		ClusterName         string              `json:"clusterName"`
		KafkaVersion        string              `json:"kafkaVersion"`
		NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes"`
		BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo"`
		Tags                map[string]string   `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ClusterName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "clusterName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if req.KafkaVersion == "" {
		req.KafkaVersion = "3.5.1"
	}
	if req.NumberOfBrokerNodes == 0 {
		req.NumberOfBrokerNodes = 3
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	id := uuid.NewString()
	clusterARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s/%s", region, h.cfg.AccountID, req.ClusterName, id)

	cluster := &Cluster{
		ClusterArn:          clusterARN,
		ClusterName:         req.ClusterName,
		State:               "CREATING",
		CreationTime:        h.clk.Now(),
		CurrentVersion:      id,
		BrokerNodeGroupInfo: req.BrokerNodeGroupInfo,
		NumberOfBrokerNodes: req.NumberOfBrokerNodes,
		KafkaVersion:        req.KafkaVersion,
		Tags:                req.Tags,
	}

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	clusterARNCopy := clusterARN
	if h.dockerReady.Load() {
		if h.puller != nil {
			h.puller.Prewarm(redpandaImage)
		}
		h.dockerWg.Add(1)
		go func() {
			defer h.dockerWg.Done()
			bgCtx := context.Background()
			if err := h.startClusterContainer(bgCtx, clusterARNCopy); err != nil {
				h.log.Warn("failed to start Docker container for MSK cluster — falling back to metadata-only",
					zap.String("cluster", clusterARNCopy), zap.Error(err))
				h.clusterFallbackActive(clusterARNCopy)
			}
		}()
	} else {
		h.scheduler.After(clusterARNCopy+":active", 0, func() {
			ctx := context.Background()
			got, aerr := h.store.getCluster(ctx, clusterARNCopy)
			if aerr != nil {
				return
			}
			if got.State == "CREATING" {
				got.State = "ACTIVE"
				h.store.putCluster(ctx, got) //nolint:errcheck
			}
		})
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":  clusterARN,
		"clusterName": req.ClusterName,
		"state":       "CREATING",
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
		if nameFilter != "" && c.ClusterName != nameFilter {
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
	clusterArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid clusterArn",
			HTTPStatus: http.StatusBadRequest,
		})
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
	clusterArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid clusterArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	cluster.State = "DELETING"
	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	clusterARNCopy := clusterArn
	h.scheduler.Cancel(clusterArn + ":health")

	id := cluster.DockerContainerID
	if h.gc != nil && id != "" {
		h.gc.StopNow(id)
		h.gc.ScheduleRemove(id)
	}
	if cluster.HostPort > 0 {
		_ = h.store.releasePort(r.Context(), cluster.HostPort) //nolint:errcheck
	}

	h.scheduler.After(clusterArn+":delete", 50*time.Millisecond, func() {
		ctx := context.Background()
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
	// For /v1/clusters/*/bootstrap-brokers, extract from path directly.
	// The chi wildcard for route /*/bootstrap-brokers captures only the ARN part.
	clusterArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid clusterArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// Strip trailing /bootstrap-brokers if chi didn't strip it (depends on route matching)
	clusterArn = strings.TrimSuffix(clusterArn, "/bootstrap-brokers")
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	var bootstrapBrokerString string
	if cluster.HostPort > 0 {
		host := h.cfg.ExternalHostname()
		bootstrapBrokerString = fmt.Sprintf("%s:%d", host, cluster.HostPort)
	} else if !h.dockerReady.Load() {
		bootstrapBrokerString = fmt.Sprintf("127.0.0.1:%d", h.cfg.MSKPortBase)
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"bootstrapBrokerString":    bootstrapBrokerString,
		"bootstrapBrokerStringTls": "",
	})
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
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "name is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	id := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:kafka:%s:%s:configuration/%s/%s", region, h.cfg.AccountID, req.Name, id)
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
	configArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid configArn",
			HTTPStatus: http.StatusBadRequest,
		})
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
	configArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid configArn",
			HTTPStatus: http.StatusBadRequest,
		})
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
	resourceArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid resourceArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	var req struct {
		Tags map[string]string `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	tags, aerr := h.store.getTags(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for k, v := range req.Tags {
		tags[k] = v
	}
	if aerr := h.store.setTags(r.Context(), resourceArn, tags); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid resourceArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	tags, aerr := h.store.getTags(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"tags": tags,
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	resourceArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid resourceArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	// tagKeys is comma-separated in the query param
	tagKeysParam := r.URL.Query().Get("tagKeys")
	var tagKeys []string
	if tagKeysParam != "" {
		tagKeys = strings.Split(tagKeysParam, ",")
	}
	tags, aerr := h.store.getTags(r.Context(), resourceArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	for _, k := range tagKeys {
		delete(tags, strings.TrimSpace(k))
	}
	if aerr := h.store.setTags(r.Context(), resourceArn, tags); aerr != nil {
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
		Provisioned *struct {
			KafkaVersion        string              `json:"kafkaVersion"`
			NumberOfBrokerNodes int                 `json:"numberOfBrokerNodes"`
			BrokerNodeGroupInfo BrokerNodeGroupInfo `json:"brokerNodeGroupInfo"`
		} `json:"provisioned"`
		Serverless *struct{} `json:"serverless"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if req.ClusterName == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "clusterName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	id := uuid.NewString()
	clusterARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s/%s", region, h.cfg.AccountID, req.ClusterName, id)

	clusterType := "SERVERLESS"
	initialState := "ACTIVE"
	cluster := &Cluster{
		ClusterArn:     clusterARN,
		ClusterName:    req.ClusterName,
		ClusterType:    clusterType,
		State:          initialState,
		CreationTime:   h.clk.Now(),
		CurrentVersion: id,
		Tags:           req.Tags,
	}

	if req.Provisioned != nil {
		cluster.ClusterType = "PROVISIONED"
		cluster.State = "CREATING"
		cluster.KafkaVersion = req.Provisioned.KafkaVersion
		cluster.NumberOfBrokerNodes = req.Provisioned.NumberOfBrokerNodes
		cluster.BrokerNodeGroupInfo = req.Provisioned.BrokerNodeGroupInfo
		if cluster.KafkaVersion == "" {
			cluster.KafkaVersion = "3.5.1"
		}
		if cluster.NumberOfBrokerNodes == 0 {
			cluster.NumberOfBrokerNodes = 3
		}
	}

	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if cluster.ClusterType == "PROVISIONED" {
		clusterARNCopy := clusterARN
		if h.dockerReady.Load() {
			if h.puller != nil {
				h.puller.Prewarm(redpandaImage)
			}
			h.dockerWg.Add(1)
			go func() {
				defer h.dockerWg.Done()
				bgCtx := context.Background()
				if err := h.startClusterContainer(bgCtx, clusterARNCopy); err != nil {
					h.log.Warn("failed to start Docker container for MSK V2 cluster — falling back to metadata-only",
						zap.String("cluster", clusterARNCopy), zap.Error(err))
					h.clusterFallbackActive(clusterARNCopy)
				}
			}()
		} else {
			h.scheduler.After(clusterARNCopy+":active", 0, func() {
				ctx := context.Background()
				got, aerr := h.store.getCluster(ctx, clusterARNCopy)
				if aerr != nil {
					return
				}
				if got.State == "CREATING" {
					got.State = "ACTIVE"
					h.store.putCluster(ctx, got) //nolint:errcheck
				}
			})
		}
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":  clusterARN,
		"clusterName": req.ClusterName,
		"clusterType": cluster.ClusterType,
		"state":       cluster.State,
	})
}

// ── describeClusterV2 ─────────────────────────────────────────────────────────

func (h *Handler) describeClusterV2(w http.ResponseWriter, r *http.Request) {
	clusterArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid clusterArn",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	cluster, aerr := h.store.getCluster(r.Context(), clusterArn)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	clusterType := cluster.ClusterType
	if clusterType == "" {
		clusterType = "PROVISIONED"
	}

	info := map[string]any{
		"clusterArn":     cluster.ClusterArn,
		"clusterName":    cluster.ClusterName,
		"clusterType":    clusterType,
		"state":          cluster.State,
		"creationTime":   cluster.CreationTime,
		"currentVersion": cluster.CurrentVersion,
		"tags":           cluster.Tags,
	}

	if clusterType == "PROVISIONED" {
		info["provisioned"] = map[string]any{
			"numberOfBrokerNodes": cluster.NumberOfBrokerNodes,
			"brokerNodeGroupInfo": cluster.BrokerNodeGroupInfo,
			"currentBrokerSoftwareInfo": map[string]any{
				"kafkaVersion": cluster.KafkaVersion,
			},
		}
	} else {
		info["serverless"] = map[string]any{}
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterInfo": info,
	})
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
	clusterArn, err := arnFromWildcard(r)
	if err != nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ValidationException",
			Message:    "invalid clusterArn",
			HTTPStatus: http.StatusBadRequest,
		})
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
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "BadRequestException",
			Message:    "currentVersion does not match the cluster's current version",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	if req.ConfigurationArn != "" {
		if _, aerr := h.store.getConfiguration(r.Context(), req.ConfigurationArn); aerr != nil {
			protocol.WriteJSONError(w, r, aerr)
			return
		}
	}

	region := middleware.RegionFromContext(r.Context(), h.cfg.Region)
	opID := uuid.NewString()
	opArn := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster-operation/%s", region, h.cfg.AccountID, opID)

	cluster.CurrentVersion = opID
	if aerr := h.store.putCluster(r.Context(), cluster); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"clusterArn":          clusterArn,
		"clusterOperationArn": opArn,
	})
}

// ── clusterFallbackActive ─────────────────────────────────────────────────────

// clusterFallbackActive sets a cluster to "ACTIVE" if it is still in "CREATING".
func (h *Handler) clusterFallbackActive(clusterARN string) {
	ctx := context.Background()
	got, aerr := h.store.getCluster(ctx, clusterARN)
	if aerr != nil {
		return
	}
	if got.State == "CREATING" || got.State == "STARTING" {
		got.State = "ACTIVE"
		h.store.putCluster(ctx, got) //nolint:errcheck
	}
}
