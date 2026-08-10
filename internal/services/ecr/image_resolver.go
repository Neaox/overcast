package ecr

// image_resolver.go — ECR as a docker.ImageResolver.
//
// A client pushes to the address ECR hands it: repositoryUri from
// CreateRepository / DescribeRepositories, and proxyEndpoint from
// GetAuthorizationToken, both of which name the registry container this
// service runs. What later *runs* the image is addressed differently. CDK
// synthesises an ECS task definition's image as
// "{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}" from AWS::AccountId
// and AWS::Region — a name it builds rather than reads back — and the ECR
// endpoint is a real wildcard domain, so a pull of it reaches AWS and is
// refused: "no basic auth credentials".
//
// This is where the two addresses are reconciled. The registry is the only
// party that knows where it is listening and what it will accept, so it is the
// party that answers.

import (
	"context"
	"strings"

	"github.com/Neaox/overcast/internal/docker"
)

// ecrRegistryUser is the username in every ECR authorization token; the
// registry's htpasswd entry is written for the same name.
const ecrRegistryUser = "AWS"

// ResolveImage implements docker.ImageResolver: an image in this account's ECR
// registry resolves to the local registry serving it, with the credentials
// GetAuthorizationToken would hand a client. Anything else — a public image,
// another account's registry, a registry that merely has an ECR-shaped path —
// is returned unchanged and unauthenticated, so its reference keeps its normal
// meaning and this registry's password is never offered to a stranger.
func (s *Service) ResolveImage(ctx context.Context, image string) docker.ImageReference {
	unresolved := docker.ImageReference{Ref: image}

	host, path, ok := strings.Cut(image, "/")
	if !ok {
		return unresolved
	}
	region, ok := s.ownRegistryRegion(host)
	if !ok {
		return unresolved
	}

	// The registry starts lazily, and a task can be placed by a reconciler
	// rather than by the deploy that pushed the image — so start it here too
	// rather than assuming an earlier push already did. Without a registry
	// (Docker unavailable) there is nowhere the image could have been pushed,
	// and claiming it would only trade one failure for a less clear one.
	_ = s.waitRegistryReady(ctx)
	s.registryMu.Lock()
	password := s.registryPassword
	ready := s.registryHostPort > 0
	s.registryMu.Unlock()
	if !ready || password == "" {
		return unresolved
	}

	// Resolve against repositoryUri, the same address the push was sent to, so
	// the two cannot drift apart.
	repo, version := splitImageVersion(path)
	repoURI := s.repoURI(region, repo)
	registryHost, _, _ := strings.Cut(repoURI, "/")
	return docker.ImageReference{
		Ref: repoURI + version,
		Auth: &docker.RegistryAuth{
			Username:      ecrRegistryUser,
			Password:      password,
			ServerAddress: registryHost,
		},
	}
}

// ownRegistryRegion reports whether host is the ECR registry endpoint for the
// account this emulator answers as — "{account}.dkr.ecr.{region}.amazonaws.com",
// including the China (.com.cn) and FIPS (ecr-fips) forms AWS also serves —
// and returns the region named in it.
//
// The region is read but not required to match the configured one: Overcast
// serves a single registry for every region, so an image pushed by a us-east-1
// stack and one pushed by an ap-southeast-2 stack are both in it, and refusing
// the region the service happens not to be configured for would break a
// multi-region deploy for no reason.
func (s *Service) ownRegistryRegion(host string) (region string, ok bool) {
	account, rest, ok := strings.Cut(host, ".")
	if !ok || account != s.accountID() {
		return "", false
	}
	service, rest, ok := strings.Cut(rest, ".")
	if !ok || service != "dkr" {
		return "", false
	}
	registry, rest, ok := strings.Cut(rest, ".")
	if !ok || (registry != "ecr" && registry != "ecr-fips") {
		return "", false
	}
	region, domain, ok := strings.Cut(rest, ".")
	if !ok || (domain != "amazonaws.com" && domain != "amazonaws.com.cn") {
		return "", false
	}
	return region, true
}

// splitImageVersion separates a repository path from its tag or digest,
// keeping the separator so the two can be rejoined around a new host. The
// version is preserved verbatim: a digest-pinned image resolved to its tag
// would run something other than what was asked for.
func splitImageVersion(path string) (repo, version string) {
	if at := strings.IndexByte(path, '@'); at >= 0 {
		return path[:at], path[at:]
	}
	if colon := strings.IndexByte(path, ':'); colon >= 0 {
		return path[:colon], path[colon:]
	}
	return path, ""
}
