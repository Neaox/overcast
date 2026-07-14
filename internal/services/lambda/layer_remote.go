package lambda

// layer_remote.go — Fetches Lambda layer content from real AWS when not
// available locally. Opt-in via LAMBDA_FETCH_REMOTE_LAYERS=true.
//
// Flow:
//  1. Parse the layer ARN to extract region, layer name, and version.
//  2. Check the on-disk cache ({DataDir}/layers/{sha256-of-arn}.zip).
//  3. If not cached, call the Lambda GetLayerVersionByArn API (SigV4-signed).
//  4. Download the layer zip from the presigned URL in the response.
//  5. Cache to disk for subsequent invocations.
//  6. Return the zip bytes.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
)

// RemoteLayerFetcher downloads Lambda layers from real AWS and caches them.
type RemoteLayerFetcher struct {
	cfg    *config.Config
	client *http.Client
	logger *zap.Logger
	clk    clock.Clock
}

// NewRemoteLayerFetcher creates a fetcher configured from the app config.
func NewRemoteLayerFetcher(cfg *config.Config, logger *zap.Logger, clk clock.Clock) *RemoteLayerFetcher {
	return &RemoteLayerFetcher{
		cfg: cfg,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
		logger: logger,
		clk:    clk,
	}
}

// FetchLayer downloads the layer zip for the given ARN. It checks the disk
// cache first. Returns the raw zip bytes.
func (f *RemoteLayerFetcher) FetchLayer(ctx context.Context, layerVersionARN string) ([]byte, error) {
	// Parse the ARN — needed for friendly filename and remote fetch.
	parsed, err := parseLayerARN(layerVersionARN)
	if err != nil {
		return nil, err
	}

	cachePath, friendlyPath := f.layerCachePaths(layerVersionARN, parsed)
	if data, err := os.ReadFile(cachePath); err == nil {
		f.logger.Debug("layer cache hit", zap.String("arn", layerVersionARN), zap.String("path", cachePath))
		return data, nil
	}
	if data, err := os.ReadFile(friendlyPath); err == nil {
		f.logger.Debug("layer cache hit (friendly name)", zap.String("arn", layerVersionARN), zap.String("path", friendlyPath))
		return data, nil
	}

	// If remote fetching is not configured (cache-only mode), stop here.
	if !f.remoteFetchEnabled() {
		return nil, fmt.Errorf("layer not found in cache: %s (hint: download the layer zip and place it at %s)",
			layerVersionARN, friendlyPath)
	}

	f.logger.Info("fetching remote layer from AWS",
		zap.String("arn", layerVersionARN),
		zap.String("region", parsed.Region))

	// Call GetLayerVersionByArn to get the presigned download URL.
	downloadURL, err := f.getLayerDownloadURL(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("get layer download URL: %w", err)
	}

	// Download the zip from the presigned URL.
	data, err := f.downloadFromURL(ctx, downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download layer zip: %w", err)
	}

	// Cache to disk.
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		f.logger.Warn("failed to create layer cache dir", zap.Error(err))
	} else if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		f.logger.Warn("failed to cache layer", zap.Error(err))
	} else {
		f.logger.Info("layer cached",
			zap.String("arn", layerVersionARN),
			zap.String("path", cachePath),
			zap.Int("bytes", len(data)))
	}

	return data, nil
}

// ResolveLayerSize verifies a layer can be resolved from the documented cache
// paths or, when enabled, by remote fetch. It avoids reading cached zip bytes so
// invoke-time metadata checks stay cheap for pre-populated AWS-managed layers.
func (f *RemoteLayerFetcher) ResolveLayerSize(ctx context.Context, layerVersionARN string) (int64, error) {
	parsed, err := parseLayerARN(layerVersionARN)
	if err != nil {
		return 0, err
	}
	cachePath, friendlyPath := f.layerCachePaths(layerVersionARN, parsed)
	if size, ok := cachedFileSize(cachePath); ok {
		f.logger.Debug("layer cache metadata hit", zap.String("arn", layerVersionARN), zap.String("path", cachePath))
		return size, nil
	}
	if size, ok := cachedFileSize(friendlyPath); ok {
		f.logger.Debug("layer cache metadata hit (friendly name)", zap.String("arn", layerVersionARN), zap.String("path", friendlyPath))
		return size, nil
	}
	if !f.remoteFetchEnabled() {
		return 0, fmt.Errorf("layer not found in cache: %s (hint: download the layer zip and place it at %s)",
			layerVersionARN, friendlyPath)
	}
	data, err := f.FetchLayer(ctx, layerVersionARN)
	if err != nil {
		return 0, err
	}
	return int64(len(data)), nil
}

func (f *RemoteLayerFetcher) layerCachePaths(layerVersionARN string, parsed parsedLayerARN) (string, string) {
	cacheDir := f.cfg.LambdaLayerCacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(f.cfg.DataDir, "layers")
	}
	cachePath := filepath.Join(cacheDir, sha256Hex(layerVersionARN)+".zip")
	friendlyPath := filepath.Join(cacheDir, parsed.LayerName+"_"+parsed.Version+".zip")
	return cachePath, friendlyPath
}

func (f *RemoteLayerFetcher) remoteFetchEnabled() bool {
	return f.cfg.LambdaFetchRemoteLayers && f.cfg.LambdaRemoteAWSAccessKeyID != ""
}

func cachedFileSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	return info.Size(), true
}

// parsedLayerARN holds the components extracted from a Lambda layer version ARN.
type parsedLayerARN struct {
	Region    string
	Account   string
	LayerName string
	Version   string
	FullARN   string
}

// parseLayerARN extracts region, account, layer name, and version from an ARN.
// like arn:aws:lambda:us-east-1:123456789012:layer:MyLayer:3.
func parseLayerARN(arn string) (parsedLayerARN, error) {
	// arn:aws:lambda:<region>:<account>:layer:<name>:<version>
	parts := strings.Split(arn, ":")
	if len(parts) < 8 || parts[2] != "lambda" || parts[5] != "layer" {
		return parsedLayerARN{}, fmt.Errorf("invalid lambda layer ARN: %s", arn)
	}
	return parsedLayerARN{
		Region:    parts[3],
		Account:   parts[4],
		LayerName: parts[6],
		Version:   parts[7],
		FullARN:   arn,
	}, nil
}

// getLayerDownloadURL calls the Lambda GetLayerVersionByArn API and returns
// the presigned S3 URL from the response Content.Location field.
func (f *RemoteLayerFetcher) getLayerDownloadURL(ctx context.Context, parsed parsedLayerARN) (string, error) {
	// Use the GetLayerVersionByArn API:
	// GET /2018-10-31/layers?find=LayerVersion&Arn=<url-encoded-arn>
	endpoint := fmt.Sprintf("https://lambda.%s.amazonaws.com", parsed.Region)
	path := "/2018-10-31/layers"
	query := url.Values{
		"find": {"LayerVersion"},
		"Arn":  {parsed.FullARN},
	}

	reqURL := endpoint + path + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", err
	}

	// Sign the request with SigV4.
	now := f.clk.Now().UTC()
	f.signRequest(req, parsed.Region, "lambda", nil, now)

	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("AWS Lambda API returned %d: %s", resp.StatusCode, truncate(string(body), 500))
	}

	// Parse the response to extract Content.Location.
	var result struct {
		Content struct {
			Location string `json:"Location"`
		} `json:"Content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.Content.Location == "" {
		return "", fmt.Errorf("no download URL in response")
	}

	return result.Content.Location, nil
}

// downloadFromURL fetches the layer zip from the presigned S3 URL.
func (f *RemoteLayerFetcher) downloadFromURL(ctx context.Context, downloadURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// signRequest applies AWS SigV4 to an outgoing HTTP request.
func (f *RemoteLayerFetcher) signRequest(req *http.Request, region, service string, payload []byte, now time.Time) {
	accessKey := f.cfg.LambdaRemoteAWSAccessKeyID
	secretKey := f.cfg.LambdaRemoteAWSSecretAccessKey
	sessionToken := f.cfg.LambdaRemoteAWSSessionToken

	datestamp := now.Format("20060102")
	amzdate := now.Format("20060102T150405Z")

	req.Header.Set("x-amz-date", amzdate)
	req.Header.Set("host", req.URL.Host)
	if sessionToken != "" {
		req.Header.Set("x-amz-security-token", sessionToken)
	}

	// Payload hash.
	var payloadHash string
	if payload == nil {
		payloadHash = sha256Hex("")
	} else {
		payloadHash = sha256HexBytes(payload)
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// Canonical headers — must be sorted.
	signedHeaderKeys := make([]string, 0, len(req.Header)+1)
	canonicalHeaders := make(map[string]string)
	for key := range req.Header {
		lower := strings.ToLower(key)
		signedHeaderKeys = append(signedHeaderKeys, lower)
		canonicalHeaders[lower] = strings.TrimSpace(req.Header.Get(key))
	}
	// host is always included.
	if _, ok := canonicalHeaders["host"]; !ok {
		signedHeaderKeys = append(signedHeaderKeys, "host")
		canonicalHeaders["host"] = req.URL.Host
	}
	sort.Strings(signedHeaderKeys)

	var canonHeaderStr strings.Builder
	for _, k := range signedHeaderKeys {
		canonHeaderStr.WriteString(k)
		canonHeaderStr.WriteByte(':')
		canonHeaderStr.WriteString(canonicalHeaders[k])
		canonHeaderStr.WriteByte('\n')
	}

	signedHeaders := strings.Join(signedHeaderKeys, ";")

	// Canonical query string.
	canonicalQuery := canonicalQueryString(req.URL)

	// Canonical request.
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery,
		canonHeaderStr.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	// Credential scope.
	scope := datestamp + "/" + region + "/" + service + "/aws4_request"

	// String to sign.
	stringToSign := "AWS4-HMAC-SHA256\n" + amzdate + "\n" + scope + "\n" + sha256Hex(canonicalRequest)

	// Signing key.
	signingKey := deriveSigningKey(secretKey, datestamp, region, service)

	// Signature.
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization header.
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

// canonicalURI returns the URI-encoded path component.
func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	return path
}

// canonicalQueryString returns the sorted, encoded query string.
func canonicalQueryString(u *url.URL) string {
	params := u.Query()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		for _, v := range params[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// deriveSigningKey derives the SigV4 signing key.
func deriveSigningKey(secret, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func sha256HexBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
