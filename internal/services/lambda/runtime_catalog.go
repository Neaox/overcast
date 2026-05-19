package lambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// runtimeFamily defines a Lambda runtime image family on public ECR
// and the rules for converting image tags into runtime IDs.
type runtimeFamily struct {
	// Image is the ECR Public repository path (e.g. "lambda/nodejs").
	Image string
	// Family is the human-readable family name (e.g. "Node.js").
	Family string
	// VersionRegex extracts the version number from an image tag.
	// Must have one capture group returning the version string.
	VersionRegex *regexp.Regexp
	// RuntimeID converts a version string (from VersionRegex) into a Lambda
	// runtime ID (e.g. "20" -> "nodejs20.x").
	RuntimeID func(version string) string
	// DisplayName builds the human-readable name (e.g. "20" -> "Node.js 20").
	DisplayName func(version string) string
	// DefaultHandler is the handler string for new functions of this family.
	DefaultHandler string
	// VersionSort returns a sort key — higher is newer.
	VersionSort func(version string) int
}

var runtimeFamilies = []runtimeFamily{
	{
		Image:          "lambda/nodejs",
		Family:         "Node.js",
		VersionRegex:   regexp.MustCompile(`^(\d+)[\.\-]`),
		RuntimeID:      func(v string) string { return "nodejs" + v + ".x" },
		DisplayName:    func(v string) string { return "Node.js " + v },
		DefaultHandler: "index.handler",
		VersionSort:    atoi,
	},
	{
		Image:          "lambda/python",
		Family:         "Python",
		VersionRegex:   regexp.MustCompile(`^(\d+\.\d+)[\.\-]`),
		RuntimeID:      func(v string) string { return "python" + v },
		DisplayName:    func(v string) string { return "Python " + v },
		DefaultHandler: "lambda_function.handler",
		VersionSort: func(v string) int {
			parts := strings.SplitN(v, ".", 2)
			major := atoiOr(parts[0], 0)
			minor := 0
			if len(parts) > 1 {
				minor = atoiOr(parts[1], 0)
			}
			return major*1000 + minor
		},
	},
	{
		Image:          "lambda/java",
		Family:         "Java",
		VersionRegex:   regexp.MustCompile(`^(\d+)[\.\-]`),
		RuntimeID:      func(v string) string { return "java" + v },
		DisplayName:    func(v string) string { return "Java " + v },
		DefaultHandler: "example.Handler::handleRequest",
		VersionSort:    atoi,
	},
	{
		Image:          "lambda/dotnet",
		Family:         ".NET",
		VersionRegex:   regexp.MustCompile(`^(\d+)[\.\-]`),
		RuntimeID:      func(v string) string { return "dotnet" + v },
		DisplayName:    func(v string) string { return ".NET " + v },
		DefaultHandler: "Function::FunctionHandler",
		VersionSort:    atoi,
	},
}

// staticCustomRuntimes are not on ECR — always included.
var staticCustomRuntimes = []RuntimeInfo{
	{ID: "provided.al2023", Name: "Custom (AL2023)", Family: "Custom runtime", DefaultHandler: "bootstrap"},
	{ID: "provided.al2", Name: "Custom (AL2)", Family: "Custom runtime", DefaultHandler: "bootstrap"},
}

// knownDeprecated lists runtime IDs that should be marked deprecated.
// Runtimes end-of-life'd by AWS but whose tags may still appear on ECR.
var knownDeprecated = map[string]bool{
	"nodejs18.x": true, "nodejs16.x": true, "nodejs14.x": true,
	"nodejs12.x": true, "nodejs10.x": true, "nodejs8.10": true,
	"nodejs6.10": true, "nodejs4.3": true,
	"python3.8": true, "python3.7": true, "python3.6": true, "python2.7": true,
	"java8": true, "java8.al2": true,
	"dotnet5": true, "dotnet5.0": true,
	"ruby2.5": true, "ruby2.7": true,
	"go1.x":        true,
	"provided.al1": true,
}

// runtimeCache holds the lazily-fetched runtime catalog.
type runtimeCache struct {
	mu       sync.RWMutex
	runtimes []RuntimeInfo
	fetched  bool
	log      *zap.Logger
}

func newRuntimeCache(log *zap.Logger) *runtimeCache {
	return &runtimeCache{log: log}
}

// get returns the cached runtime list, fetching from ECR Public on first call.
// If the ECR fetch fails, falls back to the static catalog.
func (rc *runtimeCache) get(runtimes []Runtime) []RuntimeInfo {
	rc.mu.RLock()
	if rc.fetched {
		result := rc.runtimes
		rc.mu.RUnlock()
		return result
	}
	rc.mu.RUnlock()

	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Double-check after acquiring write lock.
	if rc.fetched {
		return rc.runtimes
	}

	catalog := rc.fetchFromECR()
	if len(catalog) == 0 {
		rc.log.Warn("ECR runtime discovery returned no results, using static fallback")
		catalog = staticFallbackCatalog()
	}

	// Set the Supported flag based on registered Runtime strategies.
	for i := range catalog {
		for _, rt := range runtimes {
			if rt.CanHandle(catalog[i].ID) {
				catalog[i].Supported = true
				break
			}
		}
	}

	rc.runtimes = catalog
	rc.fetched = true
	return rc.runtimes
}

// fetchFromECR queries ECR Public for each runtime family's image tags and
// extracts available runtime versions.
func (rc *runtimeCache) fetchFromECR() []RuntimeInfo {
	var all []RuntimeInfo

	client := &http.Client{Timeout: 10 * time.Second}

	for _, fam := range runtimeFamilies {
		tags, err := fetchECRTags(client, fam.Image)
		if err != nil {
			rc.log.Warn("failed to fetch ECR tags",
				zap.String("image", fam.Image),
				zap.Error(err))
			continue
		}

		versions := extractVersions(tags, fam.VersionRegex)
		sort.Slice(versions, func(i, j int) bool {
			return fam.VersionSort(versions[i]) > fam.VersionSort(versions[j])
		})

		for _, v := range versions {
			id := fam.RuntimeID(v)
			all = append(all, RuntimeInfo{
				ID:             id,
				Name:           fam.DisplayName(v),
				Family:         fam.Family,
				DefaultHandler: fam.DefaultHandler,
				ImageURI:       "public.ecr.aws/" + fam.Image + ":" + v,
				Deprecated:     knownDeprecated[id],
			})
		}
	}

	// Append custom runtimes (not from ECR).
	all = append(all, staticCustomRuntimes...)

	return all
}

// ecrTokenResponse is the JSON shape returned by the ECR Public auth endpoint.
type ecrTokenResponse struct {
	Token string `json:"token"`
}

// ecrTagsResponse is the OCI distribution tag list.
type ecrTagsResponse struct {
	Tags []string `json:"tags"`
}

// fetchECRTags gets an anonymous token and lists image tags for a public
// ECR repository. The token endpoint requires a trailing slash.
func fetchECRTags(client *http.Client, repo string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Step 1: get anonymous auth token.
	tokenURL := fmt.Sprintf(
		"https://public.ecr.aws/token/?service=public.ecr.aws&scope=repository:%s:pull",
		repo,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d", resp.StatusCode)
	}
	var tok ecrTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}

	// Step 2: list tags using the OCI distribution API.
	tagsURL := fmt.Sprintf("https://public.ecr.aws/v2/%s/tags/list", repo)
	req2, err := http.NewRequestWithContext(ctx, http.MethodGet, tagsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build tags request: %w", err)
	}
	req2.Header.Set("Authorization", "Bearer "+tok.Token)

	resp2, err := client.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("fetch tags: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tags endpoint returned %d", resp2.StatusCode)
	}
	var tags ecrTagsResponse
	if err := json.NewDecoder(resp2.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}

	return tags.Tags, nil
}

// extractVersions de-duplicates version strings from image tag names.
func extractVersions(tags []string, re *regexp.Regexp) []string {
	seen := make(map[string]bool)
	var out []string
	for _, tag := range tags {
		m := re.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		v := m[1]
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// staticFallbackCatalog returns the hardcoded catalog used when ECR is unreachable.
func staticFallbackCatalog() []RuntimeInfo {
	return []RuntimeInfo{
		// Node.js
		{ID: "nodejs24.x", Name: "Node.js 24", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:24"},
		{ID: "nodejs22.x", Name: "Node.js 22", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:22"},
		{ID: "nodejs20.x", Name: "Node.js 20", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:20"},
		{ID: "nodejs18.x", Name: "Node.js 18", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:18", Deprecated: true},
		{ID: "nodejs16.x", Name: "Node.js 16", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:16", Deprecated: true},
		{ID: "nodejs14.x", Name: "Node.js 14", Family: "Node.js", DefaultHandler: "index.handler", ImageURI: "public.ecr.aws/lambda/nodejs:14", Deprecated: true},
		// Python
		{ID: "python3.13", Name: "Python 3.13", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.13"},
		{ID: "python3.12", Name: "Python 3.12", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.12"},
		{ID: "python3.11", Name: "Python 3.11", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.11"},
		{ID: "python3.10", Name: "Python 3.10", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.10"},
		{ID: "python3.9", Name: "Python 3.9", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.9"},
		{ID: "python3.8", Name: "Python 3.8", Family: "Python", DefaultHandler: "lambda_function.handler", ImageURI: "public.ecr.aws/lambda/python:3.8", Deprecated: true},
		// Java
		{ID: "java21", Name: "Java 21", Family: "Java", DefaultHandler: "example.Handler::handleRequest", ImageURI: "public.ecr.aws/lambda/java:21"},
		{ID: "java17", Name: "Java 17", Family: "Java", DefaultHandler: "example.Handler::handleRequest", ImageURI: "public.ecr.aws/lambda/java:17"},
		{ID: "java11", Name: "Java 11", Family: "Java", DefaultHandler: "example.Handler::handleRequest", ImageURI: "public.ecr.aws/lambda/java:11"},
		{ID: "java8", Name: "Java 8", Family: "Java", DefaultHandler: "example.Handler::handleRequest", ImageURI: "public.ecr.aws/lambda/java:8", Deprecated: true},
		// .NET
		{ID: "dotnet8", Name: ".NET 8", Family: ".NET", DefaultHandler: "Function::FunctionHandler", ImageURI: "public.ecr.aws/lambda/dotnet:8"},
		{ID: "dotnet6", Name: ".NET 6", Family: ".NET", DefaultHandler: "Function::FunctionHandler", ImageURI: "public.ecr.aws/lambda/dotnet:6"},
		// Custom
		{ID: "provided.al2023", Name: "Custom (AL2023)", Family: "Custom runtime", DefaultHandler: "bootstrap"},
		{ID: "provided.al2", Name: "Custom (AL2)", Family: "Custom runtime", DefaultHandler: "bootstrap"},
	}
}
