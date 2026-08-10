package docker

// image_resolver.go — how an image reference a caller wrote becomes one this
// daemon can actually fetch.
//
// Most images need no help: "alpine:latest" means the same thing everywhere.
// An image published to an emulated registry does not. ECR's clients address it
// as "{account}.dkr.ecr.{region}.amazonaws.com/{repo}", a name that belongs to
// real AWS, while the bytes live in a registry Overcast serves on this machine
// behind credentials it minted. Resolving is the step that turns the first into
// the second; without it the pull leaves the building.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// RegistryAuth is the credential set Docker sends to a registry, serialised
// into the X-Registry-Auth header. Field names are the daemon's AuthConfig.
type RegistryAuth struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	ServerAddress string `json:"serveraddress,omitempty"`
}

// Header encodes the credentials for X-Registry-Auth: base64url of the JSON
// AuthConfig, which is what the Docker CLI sends and what the daemon decodes.
func (a *RegistryAuth) Header() (string, error) {
	if a == nil {
		return "", nil
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("encode registry auth: %w", err)
	}
	return base64.URLEncoding.EncodeToString(raw), nil
}

// ImageReference is a pullable image reference and the credentials, if any,
// the registry serving it requires.
type ImageReference struct {
	Ref  string
	Auth *RegistryAuth
}

// ImageResolver maps an image reference as a caller wrote it onto the one this
// environment can pull. A resolver that does not serve an image returns it
// unchanged with no credentials, so an unrecognised reference keeps its normal
// meaning — and so a local registry's password is never offered to a registry
// that did not issue it.
//
// Implemented by the ECR service; wired into the pullers of the services that
// run user-chosen images.
type ImageResolver interface {
	ResolveImage(ctx context.Context, image string) ImageReference
}
