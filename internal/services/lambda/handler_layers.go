package lambda

// handler_layers.go — implemented handlers for Lambda layers.
//
// Implemented:
//   - PublishLayerVersion  POST   /2018-10-31/layers/{layerName}/versions
//   - GetLayerVersion      GET    /2018-10-31/layers/{layerName}/versions/{versionNumber}
//   - ListLayerVersions    GET    /2018-10-31/layers/{layerName}/versions
//   - ListLayers           GET    /2018-10-31/layers
//   - DeleteLayerVersion   DELETE /2018-10-31/layers/{layerName}/versions/{versionNumber}

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

// ─── wire types ──────────────────────────────────────────────────────────────

// publishLayerVersionRequest mirrors the AWS PublishLayerVersion request body.
// https://docs.aws.amazon.com/lambda/latest/api/API_PublishLayerVersion.html
type publishLayerVersionRequest struct {
	Description             string       `json:"Description,omitempty"`
	Content                 layerContent `json:"Content"`
	CompatibleRuntimes      []string     `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string     `json:"CompatibleArchitectures,omitempty"`
	LicenseInfo             string       `json:"LicenseInfo,omitempty"`
}

type layerContent struct {
	ZipFile  []byte `json:"ZipFile,omitempty"`
	S3Bucket string `json:"S3Bucket,omitempty"`
	S3Key    string `json:"S3Key,omitempty"`
}

// layerVersionContentResponse is the Content sub-object in layer version responses.
type layerVersionContentResponse struct {
	Location   string `json:"Location,omitempty"`
	CodeSha256 string `json:"CodeSha256,omitempty"`
	CodeSize   int64  `json:"CodeSize"`
}

// layerVersionResponse is the wire shape for a layer version.
// Used for PublishLayerVersion, GetLayerVersion, and list entries.
type layerVersionWireResponse struct {
	LayerVersionArn         string                      `json:"LayerVersionArn"`
	LayerArn                string                      `json:"LayerArn"`
	Version                 int64                       `json:"Version"`
	Description             string                      `json:"Description,omitempty"`
	CreatedDate             string                      `json:"CreatedDate"`
	CompatibleRuntimes      []string                    `json:"CompatibleRuntimes,omitempty"`
	CompatibleArchitectures []string                    `json:"CompatibleArchitectures,omitempty"`
	LicenseInfo             string                      `json:"LicenseInfo,omitempty"`
	Content                 layerVersionContentResponse `json:"Content"`
}

// listLayerVersionsWireResponse is the ListLayerVersions response envelope.
type listLayerVersionsWireResponse struct {
	LayerVersions []layerVersionWireResponse `json:"LayerVersions"`
	NextMarker    *string                    `json:"NextMarker,omitempty"`
}

// listLayersEntry is one item in the ListLayers response.
type listLayersEntry struct {
	LayerName             string                   `json:"LayerName"`
	LayerArn              string                   `json:"LayerArn"`
	LatestMatchingVersion layerVersionWireResponse `json:"LatestMatchingVersion"`
}

// listLayersWireResponse is the ListLayers response envelope.
type listLayersWireResponse struct {
	Layers     []listLayersEntry `json:"Layers"`
	NextMarker *string           `json:"NextMarker,omitempty"`
}

type layerVersionMetadataResponse struct {
	LayerName             string   `json:"layerName"`
	Version               int64    `json:"version"`
	HasExternalExtensions bool     `json:"hasExternalExtensions"`
	ExternalExtensions    []string `json:"externalExtensions"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func layerToWireResponse(lv *LayerVersion) layerVersionWireResponse {
	return layerVersionWireResponse{
		LayerVersionArn:         lv.LayerVersionARN,
		LayerArn:                lv.LayerARN,
		Version:                 lv.Version,
		Description:             lv.Description,
		CreatedDate:             lv.CreatedDate,
		CompatibleRuntimes:      lv.CompatibleRuntimes,
		CompatibleArchitectures: lv.CompatibleArchitectures,
		Content: layerVersionContentResponse{
			CodeSize: lv.CodeSize,
		},
	}
}

// ─── handlers ────────────────────────────────────────────────────────────────

// PublishLayerVersion handles POST /2015-03-31/layers/{layerName}/versions.
// https://docs.aws.amazon.com/lambda/latest/api/API_PublishLayerVersion.html
func (h *Handler) PublishLayerVersion(w http.ResponseWriter, r *http.Request) {
	layerName := chi.URLParam(r, "layerName")
	h.log.Debug("publish layer version", zap.String("layer", layerName))
	ctx := r.Context()

	var req publishLayerVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}

	version, err := h.ls.nextLayerVersion(ctx, layerName)
	if err != nil {
		h.log.Error("publish layer version: counter", zap.String("layer", layerName), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	region := middleware.RegionFromContext(ctx, h.cfg.Region)
	layerARN := protocol.LayerARN(region, h.cfg.AccountID, layerName)
	layerVersionARN := protocol.LayerVersionARN(region, h.cfg.AccountID, layerName, int(version))

	// Eagerly fetch from S3 when the caller passed only S3Bucket/S3Key (CDK
	// uploads layer zips before calling PublishLayerVersion). See CreateFunction
	// for the same rationale.
	content := req.Content.ZipFile
	if len(content) == 0 && req.Content.S3Bucket != "" && req.Content.S3Key != "" && h.s3Fetch != nil {
		if zip, err := h.s3Fetch(ctx, req.Content.S3Bucket, req.Content.S3Key); err == nil {
			content = zip
		} else {
			h.log.Warn("lambda: publish layer version: s3 fetch failed",
				zap.String("layer", layerName),
				zap.String("bucket", req.Content.S3Bucket),
				zap.String("key", req.Content.S3Key),
				zap.Error(err),
			)
		}
	}

	lv := &LayerVersion{
		LayerName:               layerName,
		LayerARN:                layerARN,
		LayerVersionARN:         layerVersionARN,
		Version:                 version,
		Description:             req.Description,
		CreatedDate:             h.clk.Now().UTC().Format(time.RFC3339),
		CompatibleRuntimes:      req.CompatibleRuntimes,
		CompatibleArchitectures: req.CompatibleArchitectures,
		Content:                 content,
		CodeSize:                int64(len(content)),
	}

	if aerr := h.ls.putLayerVersion(ctx, lv); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	h.log.Debug("layer version published", zap.String("layer", layerName), zap.Int64("version", version))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(layerToWireResponse(lv))
}

// GetLayerVersion handles GET /2015-03-31/layers/{layerName}/versions/{versionNumber}.
// https://docs.aws.amazon.com/lambda/latest/api/API_GetLayerVersion.html
func (h *Handler) GetLayerVersion(w http.ResponseWriter, r *http.Request) {
	layerName := chi.URLParam(r, "layerName")
	versionStr := chi.URLParam(r, "versionNumber")
	h.log.Debug("get layer version", zap.String("layer", layerName), zap.String("version", versionStr))

	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid version number"))
		return
	}

	lv, aerr := h.ls.getLayerVersion(r.Context(), layerName, version)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if lv == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Layer version not found: " + layerName + ":" + versionStr,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(layerToWireResponse(lv))
}

// GetLayerVersionMetadata handles the emulator-only layer metadata endpoint used by the web UI.
func (h *Handler) GetLayerVersionMetadata(w http.ResponseWriter, r *http.Request) {
	layerName := chi.URLParam(r, "layerName")
	versionStr := chi.URLParam(r, "versionNumber")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid version number"))
		return
	}
	lv, aerr := h.ls.getLayerVersion(r.Context(), layerName, version)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if lv == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Layer version not found: " + layerName + ":" + versionStr,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}
	extensions := discoverExternalExtensions(lv.Content)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(layerVersionMetadataResponse{
		LayerName:             layerName,
		Version:               version,
		HasExternalExtensions: len(extensions) > 0,
		ExternalExtensions:    extensions,
	})
}

// ListLayerVersions handles GET /2015-03-31/layers/{layerName}/versions.
// https://docs.aws.amazon.com/lambda/latest/api/API_ListLayerVersions.html
// AWS returns versions in descending order (newest first).
func (h *Handler) ListLayerVersions(w http.ResponseWriter, r *http.Request) {
	layerName := chi.URLParam(r, "layerName")
	h.log.Debug("list layer versions", zap.String("layer", layerName))

	versions, aerr := h.ls.listLayerVersions(r.Context(), layerName)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	// Sort descending (newest first) per AWS spec.
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Version > versions[j].Version
	})

	out := make([]layerVersionWireResponse, 0, len(versions))
	for _, lv := range versions {
		out = append(out, layerToWireResponse(lv))
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listLayerVersionsWireResponse{LayerVersions: out})
}

// ListLayers handles GET /2015-03-31/layers.
// https://docs.aws.amazon.com/lambda/latest/api/API_ListLayers.html
// Returns one entry per distinct layer name, with its latest version.
func (h *Handler) ListLayers(w http.ResponseWriter, r *http.Request) {
	h.log.Debug("list layers")
	ctx := r.Context()

	names, aerr := h.ls.listAllLayerNames(ctx)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	out := make([]listLayersEntry, 0, len(names))
	for _, name := range names {
		versions, aerr := h.ls.listLayerVersions(ctx, name)
		if aerr != nil || len(versions) == 0 {
			continue
		}
		// Pick the highest version number.
		latest := versions[0]
		for _, v := range versions[1:] {
			if v.Version > latest.Version {
				latest = v
			}
		}
		out = append(out, listLayersEntry{
			LayerName:             name,
			LayerArn:              latest.LayerARN,
			LatestMatchingVersion: layerToWireResponse(latest),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(listLayersWireResponse{Layers: out})
}

// DeleteLayerVersion handles DELETE /2015-03-31/layers/{layerName}/versions/{versionNumber}.
// https://docs.aws.amazon.com/lambda/latest/api/API_DeleteLayerVersion.html
func (h *Handler) DeleteLayerVersion(w http.ResponseWriter, r *http.Request) {
	layerName := chi.URLParam(r, "layerName")
	versionStr := chi.URLParam(r, "versionNumber")
	h.log.Debug("delete layer version", zap.String("layer", layerName), zap.String("version", versionStr))
	ctx := r.Context()

	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid version number"))
		return
	}

	lv, aerr := h.ls.getLayerVersion(ctx, layerName, version)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if lv == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Layer version not found: " + layerName + ":" + versionStr,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	if aerr := h.ls.deleteLayerVersion(ctx, layerName, version); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
