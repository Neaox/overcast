package ecr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- Request types ----

type createRepositoryRequest struct {
	RepositoryName     string `json:"repositoryName" cbor:"repositoryName"`
	ImageTagMutability string `json:"imageTagMutability" cbor:"imageTagMutability"`
	Tags               []Tag  `json:"tags" cbor:"tags"`
}

type describeRepositoriesRequest struct {
	RepositoryNames []string `json:"repositoryNames" cbor:"repositoryNames"`
}

type deleteRepositoryRequest struct {
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
	RegistryId     string `json:"registryId" cbor:"registryId"`
	Force          bool   `json:"force" cbor:"force"`
}

type repoRefRequest struct {
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
	RegistryId     string `json:"registryId" cbor:"registryId"`
}

type putImageRequest struct {
	RepositoryName         string `json:"repositoryName" cbor:"repositoryName"`
	RegistryId             string `json:"registryId" cbor:"registryId"`
	ImageManifest          string `json:"imageManifest" cbor:"imageManifest"`
	ImageManifestMediaType string `json:"imageManifestMediaType" cbor:"imageManifestMediaType"`
	ImageTag               string `json:"imageTag" cbor:"imageTag"`
	ImageDigest            string `json:"imageDigest" cbor:"imageDigest"`
}

type imageIDSetRequest struct {
	RepositoryName string            `json:"repositoryName" cbor:"repositoryName"`
	RegistryId     string            `json:"registryId" cbor:"registryId"`
	ImageIds       []ImageIdentifier `json:"imageIds" cbor:"imageIds"`
}

type describeImageScanFindingsRequest struct {
	RepositoryName string          `json:"repositoryName" cbor:"repositoryName"`
	RegistryId     string          `json:"registryId" cbor:"registryId"`
	ImageID        ImageIdentifier `json:"imageId" cbor:"imageId"`
}

type setRepositoryPolicyRequest struct {
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
	RegistryId     string `json:"registryId" cbor:"registryId"`
	PolicyText     string `json:"policyText" cbor:"policyText"`
	Force          bool   `json:"force" cbor:"force"`
}

type putLifecyclePolicyRequest struct {
	RepositoryName      string `json:"repositoryName" cbor:"repositoryName"`
	RegistryId          string `json:"registryId" cbor:"registryId"`
	LifecyclePolicyText string `json:"lifecyclePolicyText" cbor:"lifecyclePolicyText"`
}

type tagResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
	Tags        []Tag  `json:"tags" cbor:"tags"`
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

// ---- Response types ----

type createRepositoryResponse struct {
	Repository Repository `json:"repository" cbor:"repository"`
}

type describeRepositoriesResponse struct {
	Repositories []*Repository `json:"repositories" cbor:"repositories"`
}

type deleteRepositoryResponse struct {
	Repository Repository `json:"repository" cbor:"repository"`
}

type getAuthorizationTokenResponse struct {
	AuthorizationData []authDataItem `json:"authorizationData" cbor:"authorizationData"`
}

type authDataItem struct {
	AuthorizationToken string  `json:"authorizationToken" cbor:"authorizationToken"`
	ProxyEndpoint      string  `json:"proxyEndpoint" cbor:"proxyEndpoint"`
	ExpiresAt          float64 `json:"expiresAt" cbor:"expiresAt"`
}

type describeRegistryResponse struct {
	RegistryId               string            `json:"registryId" cbor:"registryId"`
	ReplicationConfiguration replicationConfig `json:"replicationConfiguration" cbor:"replicationConfiguration"`
}

type replicationConfig struct {
	Rules []any `json:"rules" cbor:"rules"`
}

type listImagesResponse struct {
	ImageIds []ImageIdentifier `json:"imageIds" cbor:"imageIds"`
}

type describeImagesResponse struct {
	ImageDetails []imageDetailWire `json:"imageDetails" cbor:"imageDetails"`
}

type imageDetailWire struct {
	RegistryId             string   `json:"registryId" cbor:"registryId"`
	RepositoryName         string   `json:"repositoryName" cbor:"repositoryName"`
	ImageDigest            string   `json:"imageDigest" cbor:"imageDigest"`
	ImageTags              []string `json:"imageTags,omitempty" cbor:"imageTags,omitempty"`
	ImageManifestMediaType string   `json:"imageManifestMediaType,omitempty" cbor:"imageManifestMediaType,omitempty"`
}

type putImageResponse struct {
	Image Image `json:"image" cbor:"image"`
}

type batchGetImageResponse struct {
	Images   []Image            `json:"images" cbor:"images"`
	Failures []imageFailureWire `json:"failures" cbor:"failures"`
}

type imageFailureWire struct {
	ImageID       ImageIdentifier `json:"imageId" cbor:"imageId"`
	FailureCode   string          `json:"failureCode" cbor:"failureCode"`
	FailureReason string          `json:"failureReason" cbor:"failureReason"`
}

type describeImageScanFindingsResponse struct {
	RegistryId        string           `json:"registryId" cbor:"registryId"`
	RepositoryName    string           `json:"repositoryName" cbor:"repositoryName"`
	ImageID           ImageIdentifier  `json:"imageId" cbor:"imageId"`
	ImageScanStatus   scanStatusWire   `json:"imageScanStatus" cbor:"imageScanStatus"`
	ImageScanFindings scanFindingsWire `json:"imageScanFindings" cbor:"imageScanFindings"`
}

type scanStatusWire struct {
	Status      string `json:"status" cbor:"status"`
	Description string `json:"description" cbor:"description"`
}

type scanFindingsWire struct {
	FindingSeverityCounts map[string]any `json:"findingSeverityCounts" cbor:"findingSeverityCounts"`
	Findings              []any          `json:"findings" cbor:"findings"`
	EnhancedFindings      []any          `json:"enhancedFindings" cbor:"enhancedFindings"`
}

type batchDeleteImageResponse struct {
	ImageIds []ImageIdentifier  `json:"imageIds" cbor:"imageIds"`
	Failures []imageFailureWire `json:"failures" cbor:"failures"`
}

type setRepositoryPolicyResponse struct {
	RegistryId     string `json:"registryId" cbor:"registryId"`
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
	PolicyText     string `json:"policyText" cbor:"policyText"`
	RepositoryArn  string `json:"repositoryArn" cbor:"repositoryArn"`
}

type getRepositoryPolicyResponse struct {
	RegistryId     string `json:"registryId" cbor:"registryId"`
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
	PolicyText     string `json:"policyText" cbor:"policyText"`
}

type deleteRepositoryPolicyResponse struct {
	RegistryId     string `json:"registryId" cbor:"registryId"`
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
}

type putLifecyclePolicyResponse struct {
	RegistryId          string `json:"registryId" cbor:"registryId"`
	RepositoryName      string `json:"repositoryName" cbor:"repositoryName"`
	LifecyclePolicyText string `json:"lifecyclePolicyText" cbor:"lifecyclePolicyText"`
}

type getLifecyclePolicyResponse struct {
	RegistryId          string `json:"registryId" cbor:"registryId"`
	RepositoryName      string `json:"repositoryName" cbor:"repositoryName"`
	LifecyclePolicyText string `json:"lifecyclePolicyText" cbor:"lifecyclePolicyText"`
}

type deleteLifecyclePolicyResponse struct {
	RegistryId     string `json:"registryId" cbor:"registryId"`
	RepositoryName string `json:"repositoryName" cbor:"repositoryName"`
}

type tagResourceResponse struct{}

type untagResourceResponse struct{}

type listTagsForResourceResponse struct {
	Tags []Tag `json:"tags" cbor:"tags"`
}

// ---- Typed handler functions ----

func (s *Service) regionCtx(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.cfg.Region)
}

func (s *Service) errRepoNotFoundTyped(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "RepositoryNotFoundException",
		Message:    fmt.Sprintf("The repository with name '%s' does not exist in the registry with id '%s'", name, s.accountID()),
		HTTPStatus: http.StatusBadRequest,
	}
}

func (s *Service) createRepositoryTyped(ctx context.Context, req *createRepositoryRequest) (*createRepositoryResponse, *protocol.AWSError) {
	if strings.TrimSpace(req.RepositoryName) == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidParameterException", Message: "repositoryName is required", HTTPStatus: http.StatusBadRequest,
		}
	}
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if found {
		return nil, &protocol.AWSError{
			Code: "RepositoryAlreadyExistsException", Message: fmt.Sprintf("The repository with name '%s' already exists in the registry with id '%s'", req.RepositoryName, s.accountID()), HTTPStatus: http.StatusBadRequest,
		}
	}
	_ = s.ensureRegistry(ctx)
	mutability := req.ImageTagMutability
	if mutability == "" {
		mutability = "MUTABLE"
	}
	repo := &Repository{
		RepositoryArn:      s.repoARN(region, req.RepositoryName),
		RegistryId:         s.accountID(),
		RepositoryName:     req.RepositoryName,
		RepositoryUri:      s.repoURI(region, req.RepositoryName),
		CreatedAt:          float64(s.clk.Now().Unix()),
		ImageTagMutability: mutability,
	}
	if err := s.saveRepo(ctx, region, repo); err != nil {
		return nil, protocol.ErrInternalError
	}
	if len(req.Tags) > 0 {
		_ = s.saveTags(ctx, repo.RepositoryArn, req.Tags)
	}
	s.publish(ctx, events.ECRRepositoryCreated, events.ResourcePayload{Name: req.RepositoryName})
	return &createRepositoryResponse{Repository: *repo}, nil
}

func (s *Service) describeRepositoriesTyped(ctx context.Context, req *describeRepositoriesRequest) (*describeRepositoriesResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	if len(req.RepositoryNames) > 0 {
		repos := make([]*Repository, 0, len(req.RepositoryNames))
		for _, name := range req.RepositoryNames {
			repo, found, err := s.getRepo(ctx, region, name)
			if err != nil {
				return nil, protocol.ErrInternalError
			}
			if !found {
				return nil, s.errRepoNotFoundTyped(name)
			}
			repos = append(repos, repo)
		}
		return &describeRepositoriesResponse{Repositories: repos}, nil
	}
	kvs, err := s.store.Scan(ctx, repoNamespace, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	repos := make([]*Repository, 0, len(kvs))
	for _, kv := range kvs {
		var repo Repository
		if err := json.Unmarshal([]byte(kv.Value), &repo); err != nil {
			continue
		}
		repos = append(repos, &repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].RepositoryName < repos[j].RepositoryName
	})
	return &describeRepositoriesResponse{Repositories: repos}, nil
}

func (s *Service) deleteRepositoryTyped(ctx context.Context, req *deleteRepositoryRequest) (*deleteRepositoryResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	repo, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Delete(ctx, repoNamespace, key); err != nil {
		return nil, protocol.ErrInternalError
	}
	imgKeys, _ := s.store.List(ctx, imageNamespace, serviceutil.RegionKey(region, req.RepositoryName+"/"))
	for _, k := range imgKeys {
		_ = s.store.Delete(ctx, imageNamespace, k)
	}
	_ = s.store.Delete(ctx, policyNamespace, key)
	_ = s.store.Delete(ctx, lifecycleNS, key)
	s.publish(ctx, events.ECRRepositoryDeleted, events.ResourcePayload{Name: req.RepositoryName})
	return &deleteRepositoryResponse{Repository: *repo}, nil
}

func (s *Service) getAuthorizationTokenTyped(ctx context.Context, _ *struct{}) (*getAuthorizationTokenResponse, *protocol.AWSError) {
	_ = s.ensureRegistry(ctx)
	password := "test"
	s.registryMu.Lock()
	if s.registryPassword != "" {
		password = s.registryPassword
	}
	s.registryMu.Unlock()
	token := base64.StdEncoding.EncodeToString([]byte("AWS:" + password))
	base := s.registryEndpoint()
	expires := float64(s.clk.Now().Add(12 * time.Hour).Unix())
	return &getAuthorizationTokenResponse{AuthorizationData: []authDataItem{{
		AuthorizationToken: token,
		ProxyEndpoint:      base,
		ExpiresAt:          expires,
	}}}, nil
}

func (s *Service) describeRegistryTyped(ctx context.Context, _ *struct{}) (*describeRegistryResponse, *protocol.AWSError) {
	return &describeRegistryResponse{
		RegistryId:               s.accountID(),
		ReplicationConfiguration: replicationConfig{Rules: []any{}},
	}, nil
}

func (s *Service) listImagesTyped(ctx context.Context, req *repoRefRequest) (*listImagesResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)
	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	ids := make([]ImageIdentifier, 0, len(kvs))
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		ids = append(ids, img.ImageId)
	}
	return &listImagesResponse{ImageIds: ids}, nil
}

func (s *Service) describeImagesTyped(ctx context.Context, req *imageIDSetRequest) (*describeImagesResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)
	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	images := make([]Image, 0, len(kvs))
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		images = append(images, img)
	}
	if len(req.ImageIds) > 0 {
		filtered := make([]Image, 0, len(req.ImageIds))
		for _, want := range req.ImageIds {
			for _, img := range images {
				if want.ImageDigest != "" && img.ImageId.ImageDigest == want.ImageDigest {
					filtered = append(filtered, img)
					break
				}
				if want.ImageTag != "" && img.ImageId.ImageTag == want.ImageTag {
					filtered = append(filtered, img)
					break
				}
			}
		}
		images = filtered
	}
	sort.Slice(images, func(i, j int) bool {
		return images[i].ImageId.ImageDigest < images[j].ImageId.ImageDigest
	})
	details := make([]imageDetailWire, 0, len(images))
	for _, img := range images {
		d := imageDetailWire{
			RegistryId:             img.RegistryId,
			RepositoryName:         img.RepositoryName,
			ImageDigest:            img.ImageId.ImageDigest,
			ImageManifestMediaType: img.ImageManifestMediaType,
		}
		if img.ImageId.ImageTag != "" {
			d.ImageTags = []string{img.ImageId.ImageTag}
		}
		details = append(details, d)
	}
	return &describeImagesResponse{ImageDetails: details}, nil
}

func (s *Service) putImageTyped(ctx context.Context, req *putImageRequest) (*putImageResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	digest := req.ImageDigest
	if digest == "" {
		digest = digestForManifest(req.ImageManifest)
	}
	img := Image{
		RegistryId:             s.accountID(),
		RepositoryName:         req.RepositoryName,
		ImageManifest:          req.ImageManifest,
		ImageManifestMediaType: req.ImageManifestMediaType,
		ImageId: ImageIdentifier{
			ImageDigest: digest,
			ImageTag:    req.ImageTag,
		},
	}
	raw, err := json.Marshal(img)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if err := s.store.Set(ctx, imageNamespace, imageKey(region, req.RepositoryName, digest), string(raw)); err != nil {
		return nil, protocol.ErrInternalError
	}
	s.publish(ctx, events.ECRImagePushed, events.ResourcePayload{Name: req.RepositoryName})
	return &putImageResponse{Image: img}, nil
}

func (s *Service) batchGetImageTyped(ctx context.Context, req *imageIDSetRequest) (*batchGetImageResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)
	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	byDigest := map[string]Image{}
	byTag := map[string]Image{}
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		byDigest[img.ImageId.ImageDigest] = img
		if img.ImageId.ImageTag != "" {
			byTag[img.ImageId.ImageTag] = img
		}
	}
	images := []Image{}
	failures := []imageFailureWire{}
	for _, id := range req.ImageIds {
		var img Image
		var ok bool
		if id.ImageDigest != "" {
			img, ok = byDigest[id.ImageDigest]
		} else if id.ImageTag != "" {
			img, ok = byTag[id.ImageTag]
		}
		if ok {
			images = append(images, img)
		} else {
			failures = append(failures, imageFailureWire{
				ImageID:       id,
				FailureCode:   "ImageNotFoundException",
				FailureReason: "Requested image not found",
			})
		}
	}
	return &batchGetImageResponse{Images: images, Failures: failures}, nil
}

func (s *Service) describeImageScanFindingsTyped(ctx context.Context, req *describeImageScanFindingsRequest) (*describeImageScanFindingsResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	var matched *Image
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		if req.ImageID.ImageDigest != "" && img.ImageId.ImageDigest == req.ImageID.ImageDigest {
			matched = &img
			break
		}
		if req.ImageID.ImageTag != "" && img.ImageId.ImageTag == req.ImageID.ImageTag {
			matched = &img
			break
		}
	}
	if matched == nil {
		return nil, &protocol.AWSError{
			Code: "ImageNotFoundException", Message: "Requested image not found", HTTPStatus: http.StatusBadRequest,
		}
	}
	return &describeImageScanFindingsResponse{
		RegistryId:     s.accountID(),
		RepositoryName: req.RepositoryName,
		ImageID:        matched.ImageId,
		ImageScanStatus: scanStatusWire{
			Status:      "UNSUPPORTED_IMAGE",
			Description: "Image scanning is not emulated",
		},
		ImageScanFindings: scanFindingsWire{
			FindingSeverityCounts: map[string]any{},
			Findings:              []any{},
			EnhancedFindings:      []any{},
		},
	}, nil
}

func (s *Service) batchDeleteImageTyped(ctx context.Context, req *imageIDSetRequest) (*batchDeleteImageResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	byDigest := map[string]string{}
	byTag := map[string]string{}
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		byDigest[img.ImageId.ImageDigest] = kv.Key
		if img.ImageId.ImageTag != "" {
			byTag[img.ImageId.ImageTag] = kv.Key
		}
	}
	deleted := []ImageIdentifier{}
	failures := []imageFailureWire{}
	for _, id := range req.ImageIds {
		var storeKey string
		var ok bool
		if id.ImageDigest != "" {
			storeKey, ok = byDigest[id.ImageDigest]
		} else if id.ImageTag != "" {
			storeKey, ok = byTag[id.ImageTag]
		}
		if ok {
			_ = s.store.Delete(ctx, imageNamespace, storeKey)
			deleted = append(deleted, id)
		} else {
			failures = append(failures, imageFailureWire{
				ImageID:       id,
				FailureCode:   "ImageNotFoundException",
				FailureReason: "Requested image not found",
			})
		}
	}
	return &batchDeleteImageResponse{ImageIds: deleted, Failures: failures}, nil
}

func (s *Service) setRepositoryPolicyTyped(ctx context.Context, req *setRepositoryPolicyRequest) (*setRepositoryPolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	repo, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Set(ctx, policyNamespace, key, req.PolicyText); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &setRepositoryPolicyResponse{
		RegistryId:     s.accountID(),
		RepositoryName: req.RepositoryName,
		PolicyText:     req.PolicyText,
		RepositoryArn:  repo.RepositoryArn,
	}, nil
}

func (s *Service) getRepositoryPolicyTyped(ctx context.Context, req *repoRefRequest) (*getRepositoryPolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	policyText, found, err := s.store.Get(ctx, policyNamespace, key)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{
			Code: "RepositoryPolicyNotFoundException", Message: fmt.Sprintf("Repository policy does not exist for the repository with name '%s'", req.RepositoryName), HTTPStatus: http.StatusBadRequest,
		}
	}
	return &getRepositoryPolicyResponse{
		RegistryId:     s.accountID(),
		RepositoryName: req.RepositoryName,
		PolicyText:     policyText,
	}, nil
}

func (s *Service) deleteRepositoryPolicyTyped(ctx context.Context, req *repoRefRequest) (*deleteRepositoryPolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	_, hasPolicy, err := s.store.Get(ctx, policyNamespace, key)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !hasPolicy {
		return nil, &protocol.AWSError{
			Code: "RepositoryPolicyNotFoundException", Message: fmt.Sprintf("Repository policy does not exist for the repository with name '%s'", req.RepositoryName), HTTPStatus: http.StatusBadRequest,
		}
	}
	_ = s.store.Delete(ctx, policyNamespace, key)
	return &deleteRepositoryPolicyResponse{
		RegistryId:     s.accountID(),
		RepositoryName: req.RepositoryName,
	}, nil
}

func (s *Service) putLifecyclePolicyTyped(ctx context.Context, req *putLifecyclePolicyRequest) (*putLifecyclePolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Set(ctx, lifecycleNS, key, req.LifecyclePolicyText); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &putLifecyclePolicyResponse{
		RegistryId:          s.accountID(),
		RepositoryName:      req.RepositoryName,
		LifecyclePolicyText: req.LifecyclePolicyText,
	}, nil
}

func (s *Service) getLifecyclePolicyTyped(ctx context.Context, req *repoRefRequest) (*getLifecyclePolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	policyText, found, err := s.store.Get(ctx, lifecycleNS, key)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, &protocol.AWSError{
			Code: "LifecyclePolicyNotFoundException", Message: fmt.Sprintf("Lifecycle policy does not exist for the repository with name '%s'", req.RepositoryName), HTTPStatus: http.StatusBadRequest,
		}
	}
	return &getLifecyclePolicyResponse{
		RegistryId:          s.accountID(),
		RepositoryName:      req.RepositoryName,
		LifecyclePolicyText: policyText,
	}, nil
}

func (s *Service) deleteLifecyclePolicyTyped(ctx context.Context, req *repoRefRequest) (*deleteLifecyclePolicyResponse, *protocol.AWSError) {
	region := s.regionCtx(ctx)
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !found {
		return nil, s.errRepoNotFoundTyped(req.RepositoryName)
	}
	key := serviceutil.RegionKey(region, req.RepositoryName)
	_, hasLP, err := s.store.Get(ctx, lifecycleNS, key)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if !hasLP {
		return nil, &protocol.AWSError{
			Code: "LifecyclePolicyNotFoundException", Message: fmt.Sprintf("Lifecycle policy does not exist for the repository with name '%s'", req.RepositoryName), HTTPStatus: http.StatusBadRequest,
		}
	}
	_ = s.store.Delete(ctx, lifecycleNS, key)
	return &deleteLifecyclePolicyResponse{
		RegistryId:     s.accountID(),
		RepositoryName: req.RepositoryName,
	}, nil
}

func (s *Service) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (*tagResourceResponse, *protocol.AWSError) {
	existing, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	tagMap := map[string]string{}
	for _, t := range existing {
		tagMap[t.Key] = t.Value
	}
	for _, t := range req.Tags {
		tagMap[t.Key] = t.Value
	}
	merged := make([]Tag, 0, len(tagMap))
	for k, v := range tagMap {
		merged = append(merged, Tag{Key: k, Value: v})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Key < merged[j].Key })
	if err := s.saveTags(ctx, req.ResourceArn, merged); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &tagResourceResponse{}, nil
}

func (s *Service) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (*untagResourceResponse, *protocol.AWSError) {
	existing, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	remove := map[string]bool{}
	for _, k := range req.TagKeys {
		remove[k] = true
	}
	filtered := make([]Tag, 0, len(existing))
	for _, t := range existing {
		if !remove[t.Key] {
			filtered = append(filtered, t)
		}
	}
	if err := s.saveTags(ctx, req.ResourceArn, filtered); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &untagResourceResponse{}, nil
}

func (s *Service) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	tags, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	if tags == nil {
		tags = []Tag{}
	}
	return &listTagsForResourceResponse{Tags: tags}, nil
}
