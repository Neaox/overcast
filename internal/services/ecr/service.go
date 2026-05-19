// Package ecr provides emulation of Amazon Elastic Container Registry (ECR).
//
// Implemented operations: CreateRepository, DescribeRepositories,
// DeleteRepository, GetAuthorizationToken, DescribeRegistry, ListImages, PutImage,
// BatchGetImage, BatchDeleteImage, SetRepositoryPolicy, GetRepositoryPolicy,
// DeleteRepositoryPolicy, PutLifecyclePolicy, GetLifecyclePolicy,
// DeleteLifecyclePolicy, TagResource, UntagResource, ListTagsForResource.
//
// The control-plane operations (repository CRUD, image metadata, tags, policies)
// are fully implemented in-memory. When Docker is available, ECR also lazy-starts
// one shared local registry:2 container per process and returns matching auth
// credentials via GetAuthorizationToken.
package ecr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
	"golang.org/x/crypto/bcrypt"
)

const serviceName = "ecr"

const (
	ecrRegistryContainerName = "overcast-ecr-registry"
	ecrRegistryImage         = "registry:2"
	ecrRegistryPortKey       = "5000/tcp"
)

// ─── Store namespaces ─────────────────────────────────────────────────────────

const (
	repoNamespace   = "ecr:repositories" // key: region/name
	imageNamespace  = "ecr:images"       // key: region/repoName/digest
	tagNamespace    = "ecr:tags"         // key: region/arn → JSON tag list
	policyNamespace = "ecr:policies"     // key: region/name → policy text
	lifecycleNS     = "ecr:lifecycle"    // key: region/name → policy text
)

// ─── Types ────────────────────────────────────────────────────────────────────

// Repository represents an ECR repository.
type Repository struct {
	RepositoryArn      string  `json:"repositoryArn"`
	RegistryId         string  `json:"registryId"`
	RepositoryName     string  `json:"repositoryName"`
	RepositoryUri      string  `json:"repositoryUri"`
	CreatedAt          float64 `json:"createdAt"`
	ImageTagMutability string  `json:"imageTagMutability"`
}

// ImageIdentifier uniquely identifies an image by tag or digest.
type ImageIdentifier struct {
	ImageDigest string `json:"imageDigest,omitempty"`
	ImageTag    string `json:"imageTag,omitempty"`
}

// Image represents an ECR image record.
type Image struct {
	RegistryId             string          `json:"registryId"`
	RepositoryName         string          `json:"repositoryName"`
	ImageId                ImageIdentifier `json:"imageId"`
	ImageManifest          string          `json:"imageManifest"`
	ImageManifestMediaType string          `json:"imageManifestMediaType,omitempty"`
}

// Tag is a key/value tag.
type Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// ─── Service ──────────────────────────────────────────────────────────────────

// Service implements the ECR emulator.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
	bus     *events.Bus

	docker            *docker.Client
	puller            *docker.ImagePuller
	registryMu        sync.Mutex
	registryContainer string
	registryHostPort  int
	registryPassword  string
	registryInitOnce  sync.Once
	registryReady     chan struct{}
}

// InitBus wires the event bus for ECR lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.bus = bus
}

// publish emits an event if the bus is wired.
func (s *Service) publish(ctx context.Context, t events.Type, payload any) {
	if s.bus != nil {
		s.bus.Publish(ctx, events.Event{Type: t, Payload: payload})
	}
}

// New returns a configured ECR Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	s := &Service{
		cfg:   cfg,
		store: st,
		log:   serviceutil.NewServiceLogger(logger, serviceName),
		clk:   clk,
	}
	if cfg != nil && cfg.LambdaDockerSocket != "" {
		s.docker = docker.NewClient(cfg.LambdaDockerSocket, logger)
		s.puller = docker.NewImagePuller(s.docker)
	}
	s.ops = map[string]http.HandlerFunc{
		"CreateRepository":          s.createRepository,
		"DescribeRepositories":      s.describeRepositories,
		"DeleteRepository":          s.deleteRepository,
		"GetAuthorizationToken":     s.getAuthorizationToken,
		"DescribeRegistry":          s.describeRegistry,
		"ListImages":                s.listImages,
		"DescribeImages":            s.describeImages,
		"PutImage":                  s.putImage,
		"BatchGetImage":             s.batchGetImage,
		"DescribeImageScanFindings": s.describeImageScanFindings,
		"BatchDeleteImage":          s.batchDeleteImage,
		"SetRepositoryPolicy":       s.setRepositoryPolicy,
		"GetRepositoryPolicy":       s.getRepositoryPolicy,
		"DeleteRepositoryPolicy":    s.deleteRepositoryPolicy,
		"PutLifecyclePolicy":        s.putLifecyclePolicy,
		"GetLifecyclePolicy":        s.getLifecyclePolicy,
		"DeleteLifecyclePolicy":     s.deleteLifecyclePolicy,
		"TagResource":               s.tagResource,
		"UntagResource":             s.untagResource,
		"ListTagsForResource":       s.listTagsForResource,
	}
	s.typedOp = s.typedOps()
	return s
}

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}
func (s *Service) TargetPrefix() string        { return "AmazonEC2ContainerRegistry_V20150921." }

func (s *Service) Dispatch(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			w.Header().Set("x-emulator-unsupported-protocol", c.Name())
			c.WriteError(w, r, &protocol.AWSError{
				Code:       "UnsupportedProtocol",
				Message:    "ECR does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if c.Name() != codec.NameRPCv2CBOR {
			s.dispatchLegacy(w, r, opName)
			return
		}
		if typed, ok := s.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}

	target := r.Header.Get("X-Amz-Target")
	opName := target
	if idx := strings.LastIndex(target, "."); idx >= 0 {
		opName = target[idx+1:]
	}
	s.dispatchLegacy(w, r, opName)
}

func (s *Service) dispatchLegacy(w http.ResponseWriter, r *http.Request, opName string) {
	if fn, ok := s.ops[opName]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedJSON(w, r)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Service) region(r *http.Request) string {
	return middleware.RegionFromContext(r.Context(), s.cfg.Region)
}

func (s *Service) registryEndpoint() string {
	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.registryHostPort > 0 {
		return fmt.Sprintf("http://%s:%d", s.cfg.ExternalHostname(), s.registryHostPort)
	}
	return s.cfg.ExternalBaseURL()
}

func (s *Service) accountID() string {
	if s.cfg != nil && strings.TrimSpace(s.cfg.AccountID) != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) repoURI(region, name string) string {
	base := s.registryEndpoint()
	// Trim scheme for the registry hostname part.
	host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
	return fmt.Sprintf("%s/%s/%s", host, s.accountID(), name)
}

func (s *Service) repoARN(region, name string) string {
	return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", region, s.accountID(), name)
}

func (s *Service) getRepo(ctx context.Context, region, name string) (*Repository, bool, error) {
	key := serviceutil.RegionKey(region, name)
	raw, found, err := s.store.Get(ctx, repoNamespace, key)
	if err != nil || !found {
		return nil, found, err
	}
	var repo Repository
	if err := json.Unmarshal([]byte(raw), &repo); err != nil {
		return nil, false, err
	}
	return &repo, true, nil
}

func (s *Service) saveRepo(ctx context.Context, region string, repo *Repository) error {
	raw, err := json.Marshal(repo)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, repoNamespace, serviceutil.RegionKey(region, repo.RepositoryName), string(raw))
}

func (s *Service) errRepoNotFound(w http.ResponseWriter, r *http.Request, name string) {
	protocol.WriteJSONError(w, r, &protocol.AWSError{
		Code:       "RepositoryNotFoundException",
		Message:    fmt.Sprintf("The repository with name '%s' does not exist in the registry with id '%s'", name, s.accountID()),
		HTTPStatus: http.StatusBadRequest,
	})
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Service) createRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName     string `json:"repositoryName"`
		ImageTagMutability string `json:"imageTagMutability"`
		Tags               []Tag  `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RepositoryName) == "" {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterException",
			Message:    "repositoryName is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	region := s.region(r)
	existing, found, err := s.getRepo(r.Context(), region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "RepositoryAlreadyExistsException",
			Message:    fmt.Sprintf("The repository with name '%s' already exists in the registry with id '%s'", req.RepositoryName, s.accountID()),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	_ = existing

	// Ensure the shared local registry endpoint is ready when Docker is available.
	_ = s.ensureRegistry(r.Context())

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
	if err := s.saveRepo(r.Context(), region, repo); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Save initial tags if any.
	if len(req.Tags) > 0 {
		_ = s.saveTags(r.Context(), repo.RepositoryArn, req.Tags)
	}

	s.publish(r.Context(), events.ECRRepositoryCreated, events.ResourcePayload{Name: req.RepositoryName})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"repository": repo})
}

func (s *Service) describeRepositories(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryNames []string `json:"repositoryNames"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()

	// If names specified, fetch each individually.
	if len(req.RepositoryNames) > 0 {
		repos := make([]*Repository, 0, len(req.RepositoryNames))
		for _, name := range req.RepositoryNames {
			repo, found, err := s.getRepo(ctx, region, name)
			if err != nil {
				protocol.WriteJSONError(w, r, protocol.ErrInternalError)
				return
			}
			if !found {
				s.errRepoNotFound(w, r, name)
				return
			}
			repos = append(repos, repo)
		}
		protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"repositories": repos})
		return
	}

	// List all repos in region.
	kvs, err := s.store.Scan(ctx, repoNamespace, serviceutil.RegionKey(region, ""))
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
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
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"repositories": repos})
}

func (s *Service) deleteRepository(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
		Force          bool   `json:"force"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	repo, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Delete(ctx, repoNamespace, key); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	// Clean up images and policies.
	imgKeys, _ := s.store.List(ctx, imageNamespace, serviceutil.RegionKey(region, req.RepositoryName+"/"))
	for _, k := range imgKeys {
		_ = s.store.Delete(ctx, imageNamespace, k)
	}
	_ = s.store.Delete(ctx, policyNamespace, key)
	_ = s.store.Delete(ctx, lifecycleNS, key)

	s.publish(r.Context(), events.ECRRepositoryDeleted, events.ResourcePayload{Name: req.RepositoryName})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"repository": repo})
}

func (s *Service) getAuthorizationToken(w http.ResponseWriter, r *http.Request) {
	_ = s.ensureRegistry(r.Context())
	_ = s.waitRegistryReady(r.Context())

	password := "test"
	s.registryMu.Lock()
	if s.registryPassword != "" {
		password = s.registryPassword
	}
	s.registryMu.Unlock()

	// ECR token format is base64("AWS:<password>").
	token := base64.StdEncoding.EncodeToString([]byte("AWS:" + password))
	base := s.registryEndpoint()
	expires := s.clk.Now().Add(12 * time.Hour).Unix()
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"authorizationData": []map[string]any{{
			"authorizationToken": token,
			"proxyEndpoint":      base,
			"expiresAt":          float64(expires),
		}},
	})
}

func (s *Service) describeRegistry(w http.ResponseWriter, r *http.Request) {
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId": s.accountID(),
		"replicationConfiguration": map[string]any{
			"rules": []any{},
		},
	})
}

// imageKey returns the store key for an image.
func imageKey(region, repoName, digest string) string {
	return serviceutil.RegionKey(region, repoName+"/"+digest)
}

func digestForManifest(manifest string) string {
	sum := sha256.Sum256([]byte(manifest))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (s *Service) syncRepoImagesFromRegistry(ctx context.Context, region, repoName string) error {
	if s.docker == nil {
		return nil
	}
	if err := s.ensureRegistry(ctx); err != nil {
		return nil
	}

	s.registryMu.Lock()
	hostPort := s.registryHostPort
	password := s.registryPassword
	s.registryMu.Unlock()
	if hostPort <= 0 || strings.TrimSpace(password) == "" {
		return nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	repoPath := fmt.Sprintf("%s/%s", s.accountID(), repoName)
	tagsURL := fmt.Sprintf("http://127.0.0.1:%d/v2/%s/tags/list", hostPort, repoPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("AWS", password)
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}

	var tagsResp struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil
	}
	for _, tag := range tagsResp.Tags {
		if strings.TrimSpace(tag) == "" {
			continue
		}
		manifestURL := fmt.Sprintf("http://127.0.0.1:%d/v2/%s/manifests/%s", hostPort, repoPath, tag)
		manifestReq, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
		if err != nil {
			continue
		}
		manifestReq.SetBasicAuth("AWS", password)
		manifestReq.Header.Set("Accept", strings.Join([]string{
			"application/vnd.oci.image.manifest.v1+json",
			"application/vnd.docker.distribution.manifest.v2+json",
			"application/vnd.docker.distribution.manifest.list.v2+json",
			"application/vnd.oci.image.index.v1+json",
		}, ", "))
		manifestResp, err := client.Do(manifestReq)
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(manifestResp.Body)
		manifestResp.Body.Close()
		if readErr != nil || manifestResp.StatusCode < 200 || manifestResp.StatusCode >= 300 {
			continue
		}
		manifest := string(body)
		digest := strings.TrimSpace(manifestResp.Header.Get("Docker-Content-Digest"))
		if digest == "" {
			digest = digestForManifest(manifest)
		}
		img := Image{
			RegistryId:             s.accountID(),
			RepositoryName:         repoName,
			ImageManifest:          manifest,
			ImageManifestMediaType: manifestResp.Header.Get("Content-Type"),
			ImageId: ImageIdentifier{
				ImageDigest: digest,
				ImageTag:    tag,
			},
		}
		raw, err := json.Marshal(img)
		if err != nil {
			continue
		}
		if err := s.store.Set(ctx, imageNamespace, imageKey(region, repoName, digest), string(raw)); err != nil {
			continue
		}
	}
	return nil
}

func (s *Service) listImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)

	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	ids := make([]ImageIdentifier, 0, len(kvs))
	for _, kv := range kvs {
		var img Image
		if err := json.Unmarshal([]byte(kv.Value), &img); err != nil {
			continue
		}
		ids = append(ids, img.ImageId)
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"imageIds": ids})
}

func (s *Service) describeImages(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string            `json:"repositoryName"`
		RegistryId     string            `json:"registryId"`
		ImageIds       []ImageIdentifier `json:"imageIds"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)

	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
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

	type imageDetail struct {
		RegistryId             string   `json:"registryId"`
		RepositoryName         string   `json:"repositoryName"`
		ImageDigest            string   `json:"imageDigest"`
		ImageTags              []string `json:"imageTags,omitempty"`
		ImageManifestMediaType string   `json:"imageManifestMediaType,omitempty"`
	}
	details := make([]imageDetail, 0, len(images))
	for _, img := range images {
		d := imageDetail{
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

	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"imageDetails": details})
}

func (s *Service) putImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName         string `json:"repositoryName"`
		RegistryId             string `json:"registryId"`
		ImageManifest          string `json:"imageManifest"`
		ImageManifestMediaType string `json:"imageManifestMediaType"`
		ImageTag               string `json:"imageTag"`
		ImageDigest            string `json:"imageDigest"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	// Generate digest if not provided.
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
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if err := s.store.Set(ctx, imageNamespace, imageKey(region, req.RepositoryName, digest), string(raw)); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	s.publish(r.Context(), events.ECRImagePushed, events.ResourcePayload{Name: req.RepositoryName})
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"image": img})
}

func (s *Service) batchGetImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string            `json:"repositoryName"`
		RegistryId     string            `json:"registryId"`
		ImageIds       []ImageIdentifier `json:"imageIds"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}
	_ = s.syncRepoImagesFromRegistry(ctx, region, req.RepositoryName)

	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Index images by tag and digest.
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
	failures := []map[string]any{}
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
			failures = append(failures, map[string]any{
				"imageId":       id,
				"failureCode":   "ImageNotFoundException",
				"failureReason": "Requested image not found",
			})
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"images":   images,
		"failures": failures,
	})
}

func (s *Service) describeImageScanFindings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string          `json:"repositoryName"`
		RegistryId     string          `json:"registryId"`
		ImageID        ImageIdentifier `json:"imageId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
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
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ImageNotFoundException",
			Message:    "Requested image not found",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Overcast stores image metadata but does not emulate scanner engines.
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":     s.accountID(),
		"repositoryName": req.RepositoryName,
		"imageId":        matched.ImageId,
		"imageScanStatus": map[string]any{
			"status":      "UNSUPPORTED_IMAGE",
			"description": "Image scanning is not emulated",
		},
		"imageScanFindings": map[string]any{
			"findingSeverityCounts": map[string]any{},
			"findings":              []any{},
			"enhancedFindings":      []any{},
		},
	})
}

func (s *Service) batchDeleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string            `json:"repositoryName"`
		RegistryId     string            `json:"registryId"`
		ImageIds       []ImageIdentifier `json:"imageIds"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	prefix := serviceutil.RegionKey(region, req.RepositoryName+"/")
	kvs, err := s.store.Scan(ctx, imageNamespace, prefix)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	byDigest := map[string]string{}
	byTag := map[string]string{} // tag -> key
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
	failures := []map[string]any{}
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
			failures = append(failures, map[string]any{
				"imageId":       id,
				"failureCode":   "ImageNotFoundException",
				"failureReason": "Requested image not found",
			})
		}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"imageIds": deleted,
		"failures": failures,
	})
}

func (s *Service) setRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
		PolicyText     string `json:"policyText"`
		Force          bool   `json:"force"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	repo, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Set(ctx, policyNamespace, key, req.PolicyText); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":     s.accountID(),
		"repositoryName": req.RepositoryName,
		"policyText":     req.PolicyText,
		"repositoryArn":  repo.RepositoryArn,
	})
}

func (s *Service) getRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	policyText, found, err := s.store.Get(ctx, policyNamespace, key)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "RepositoryPolicyNotFoundException",
			Message:    fmt.Sprintf("Repository policy does not exist for the repository with name '%s'", req.RepositoryName),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":     s.accountID(),
		"repositoryName": req.RepositoryName,
		"policyText":     policyText,
	})
}

func (s *Service) deleteRepositoryPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	_, hasPolicy, err := s.store.Get(ctx, policyNamespace, key)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !hasPolicy {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "RepositoryPolicyNotFoundException",
			Message:    fmt.Sprintf("Repository policy does not exist for the repository with name '%s'", req.RepositoryName),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	_ = s.store.Delete(ctx, policyNamespace, key)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":     s.accountID(),
		"repositoryName": req.RepositoryName,
	})
}

func (s *Service) putLifecyclePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName      string `json:"repositoryName"`
		RegistryId          string `json:"registryId"`
		LifecyclePolicyText string `json:"lifecyclePolicyText"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	if err := s.store.Set(ctx, lifecycleNS, key, req.LifecyclePolicyText); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":          s.accountID(),
		"repositoryName":      req.RepositoryName,
		"lifecyclePolicyText": req.LifecyclePolicyText,
	})
}

func (s *Service) getLifecyclePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	policyText, found, err := s.store.Get(ctx, lifecycleNS, key)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "LifecyclePolicyNotFoundException",
			Message:    fmt.Sprintf("Lifecycle policy does not exist for the repository with name '%s'", req.RepositoryName),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":          s.accountID(),
		"repositoryName":      req.RepositoryName,
		"lifecyclePolicyText": policyText,
	})
}

func (s *Service) deleteLifecyclePolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RepositoryName string `json:"repositoryName"`
		RegistryId     string `json:"registryId"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	region := s.region(r)
	ctx := r.Context()
	_, found, err := s.getRepo(ctx, region, req.RepositoryName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		s.errRepoNotFound(w, r, req.RepositoryName)
		return
	}

	key := serviceutil.RegionKey(region, req.RepositoryName)
	_, found, err = s.store.Get(ctx, lifecycleNS, key)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if !found {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "LifecyclePolicyNotFoundException",
			Message:    fmt.Sprintf("Lifecycle policy does not exist for the repository with name '%s'", req.RepositoryName),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	_ = s.store.Delete(ctx, lifecycleNS, key)
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{
		"registryId":     s.accountID(),
		"repositoryName": req.RepositoryName,
	})
}

// ─── Tag operations ───────────────────────────────────────────────────────────

func (s *Service) saveTags(ctx context.Context, arn string, tags []Tag) error {
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	return s.store.Set(ctx, tagNamespace, arn, string(raw))
}

func (s *Service) loadTags(ctx context.Context, arn string) ([]Tag, error) {
	raw, found, err := s.store.Get(ctx, tagNamespace, arn)
	if err != nil || !found {
		return nil, err
	}
	var tags []Tag
	if err := json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
		Tags        []Tag  `json:"tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	existing, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}

	// Merge: existing tags overwritten by new ones.
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
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string   `json:"resourceArn"`
		TagKeys     []string `json:"tagKeys"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	existing, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
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
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{})
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ResourceArn string `json:"resourceArn"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	ctx := r.Context()
	tags, err := s.loadTags(ctx, req.ResourceArn)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInternalError)
		return
	}
	if tags == nil {
		tags = []Tag{}
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"tags": tags})
}

// ensureRegistry triggers lazy-start of the shared local registry container.
// When Docker is available it fires off the container setup in a background
// goroutine and returns immediately — callers that need the registry to be
// ready (e.g. GetAuthorizationToken) should call waitRegistryReady afterwards.
func (s *Service) ensureRegistry(ctx context.Context) error {
	if s.docker == nil {
		return nil
	}

	s.registryMu.Lock()
	if s.registryHostPort > 0 {
		s.registryMu.Unlock()
		return nil
	}
	if s.registryPassword == "" {
		password, err := generateRegistryPassword()
		if err != nil {
			s.registryMu.Unlock()
			return err
		}
		s.registryPassword = password
	}
	s.registryMu.Unlock()

	s.registryInitOnce.Do(func() {
		s.registryMu.Lock()
		s.registryReady = make(chan struct{})
		s.registryMu.Unlock()
		go s.initRegistryDocker()
	})
	return nil
}

// waitRegistryReady blocks until the registry container is fully started, or
// the context is cancelled. Returns immediately if Docker is not configured or
// the registry is already ready.
func (s *Service) waitRegistryReady(ctx context.Context) error {
	if s.docker == nil {
		return nil
	}
	_ = s.ensureRegistry(ctx)
	s.registryMu.Lock()
	ready := s.registryReady
	s.registryMu.Unlock()
	if ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// initRegistryDocker performs the blocking Docker setup for the shared local
// registry container. Runs in a background goroutine launched by registryInitOnce.
func (s *Service) initRegistryDocker() {
	defer close(s.registryReady)

	pingCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	err := s.docker.Ping(pingCtx)
	cancel()
	if err != nil {
		return
	}

	s.registryMu.Lock()
	defer s.registryMu.Unlock()
	if s.registryHostPort > 0 {
		return
	}

	// Password is already generated by ensureRegistry before launching this goroutine.
	if s.puller != nil {
		pullCtx, pullCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if perr := s.puller.Ensure(pullCtx, ecrRegistryImage); perr != nil {
			s.log.Warn("failed to pull ECR registry image", zap.Error(perr))
		}
		pullCancel()
	}

	info, err := s.docker.GetContainerByName(context.Background(), ecrRegistryContainerName)
	if err != nil {
		return
	}

	registryDir := filepath.Join(s.cfg.DataDir, "ecr-registry")
	htpasswdPath := filepath.Join(registryDir, "htpasswd")
	if err := writeHTPasswdFile(htpasswdPath, s.registryPassword); err != nil {
		return
	}

	var containerID string
	if info != nil {
		if !info.HasOvercastLabels(serviceName, "registry") {
			s.log.Warn("existing ecr registry container is not overcast-managed", zap.String("container", ecrRegistryContainerName))
			return
		}
		_ = s.docker.StopContainer(context.Background(), info.ID, 3)
		_ = s.docker.RemoveContainerForce(info.ID)
	}

	req := &docker.CreateContainerRequest{
		ContainerConfig: &docker.ContainerConfig{
			Image: ecrRegistryImage,
			ExposedPorts: map[string]struct{}{
				ecrRegistryPortKey: {},
			},
			Env: []string{
				"REGISTRY_AUTH=htpasswd",
				"REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm",
				"REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
			},
			Labels: docker.ManagedLabels(serviceName, "registry"),
		},
		HostConfig: &docker.HostConfig{AutoRemove: true,
			Binds: []string{htpasswdPath + ":/auth/htpasswd:ro"},
			PortBindings: map[string][]docker.PortBinding{
				ecrRegistryPortKey: {{HostIP: "0.0.0.0"}},
			},
		},
	}

	containerID, err = s.docker.CreateContainer(context.Background(), ecrRegistryContainerName, req)
	if err != nil {
		return
	}

	if err := s.docker.StartContainer(context.Background(), containerID); err != nil {
		return
	}
	inspect, err := s.docker.InspectContainer(context.Background(), containerID)
	if err != nil {
		return
	}

	bindings := inspect.NetworkSettings.Ports[ecrRegistryPortKey]
	if len(bindings) == 0 || bindings[0].HostPort == "" {
		return
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return
	}

	s.registryContainer = containerID
	s.registryHostPort = hostPort
}

func generateRegistryPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate registry password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeHTPasswdFile(path, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir ecr auth dir: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash ecr registry password: %w", err)
	}
	line := "AWS:" + string(hash) + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		return fmt.Errorf("write ecr htpasswd: %w", err)
	}
	return nil
}

// Stop tears down the shared registry container on server shutdown.
func (s *Service) Stop(ctx context.Context) {
	if s.docker == nil {
		return
	}

	s.registryMu.Lock()
	containerID := s.registryContainer
	s.registryMu.Unlock()
	if containerID == "" {
		return
	}

	_ = s.docker.StopContainer(ctx, containerID, 3)
	_ = s.docker.RemoveContainerForce(containerID)
}
