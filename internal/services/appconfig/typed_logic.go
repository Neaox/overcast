package appconfig

import (
	"context"

	"github.com/Neaox/overcast/internal/protocol"
)

type createApplicationRequest struct {
	Name        string `json:"Name" cbor:"Name"`
	Description string `json:"Description" cbor:"Description"`
}

type createApplicationResponse struct {
	Id          string `json:"Id" cbor:"Id"`
	Name        string `json:"Name" cbor:"Name"`
	Description string `json:"Description,omitempty" cbor:"Description,omitempty"`
}

func (s *Service) createApplicationTyped(ctx context.Context, req *createApplicationRequest) (*createApplicationResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, &protocol.AWSError{
			Code:       "BadRequestException",
			Message:    "Name is required",
			HTTPStatus: 400,
		}
	}
	app := &Application{ID: shortID(), Name: req.Name, Description: req.Description}
	if err := s.store.putApp(ctx, app); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createApplicationResponse{
		Id:          app.ID,
		Name:        app.Name,
		Description: app.Description,
	}, nil
}

type getApplicationRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
}

type getApplicationResponse struct {
	Id          string `json:"Id" cbor:"Id"`
	Name        string `json:"Name" cbor:"Name"`
	Description string `json:"Description,omitempty" cbor:"Description,omitempty"`
}

func (s *Service) getApplicationTyped(ctx context.Context, req *getApplicationRequest) (*getApplicationResponse, *protocol.AWSError) {
	app, found := s.store.getApp(ctx, req.ApplicationId)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Application not found",
			HTTPStatus: 404,
		}
	}
	return &getApplicationResponse{
		Id:          app.ID,
		Name:        app.Name,
		Description: app.Description,
	}, nil
}

type listApplicationsRequest struct{}

type listApplicationsResponse struct {
	Items []*Application `json:"Items" cbor:"Items"`
}

func (s *Service) listApplicationsTyped(ctx context.Context, _ *listApplicationsRequest) (*listApplicationsResponse, *protocol.AWSError) {
	apps, err := s.store.listApps(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listApplicationsResponse{Items: apps}, nil
}

type deleteApplicationRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
}

func (s *Service) deleteApplicationTyped(ctx context.Context, req *deleteApplicationRequest) (any, *protocol.AWSError) {
	if _, found := s.store.getApp(ctx, req.ApplicationId); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Application not found",
			HTTPStatus: 404,
		}
	}
	_ = s.store.deleteApp(ctx, req.ApplicationId)
	return nil, nil
}

type createEnvironmentRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Name          string `json:"Name" cbor:"Name"`
	Description   string `json:"Description" cbor:"Description"`
}

type createEnvironmentResponse struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Id            string `json:"Id" cbor:"Id"`
	Name          string `json:"Name" cbor:"Name"`
	Description   string `json:"Description,omitempty" cbor:"Description,omitempty"`
	State         string `json:"State" cbor:"State"`
}

func (s *Service) createEnvironmentTyped(ctx context.Context, req *createEnvironmentRequest) (*createEnvironmentResponse, *protocol.AWSError) {
	env := &Environment{
		ApplicationId: req.ApplicationId,
		ID:            shortID(),
		Name:          req.Name,
		Description:   req.Description,
		State:         "READY_FOR_DEPLOYMENT",
	}
	if err := s.store.putEnv(ctx, env); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createEnvironmentResponse{
		ApplicationId: env.ApplicationId,
		Id:            env.ID,
		Name:          env.Name,
		Description:   env.Description,
		State:         env.State,
	}, nil
}

type getEnvironmentRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	EnvironmentId string `json:"EnvironmentId" cbor:"EnvironmentId"`
}

type getEnvironmentResponse struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Id            string `json:"Id" cbor:"Id"`
	Name          string `json:"Name" cbor:"Name"`
	Description   string `json:"Description,omitempty" cbor:"Description,omitempty"`
	State         string `json:"State" cbor:"State"`
}

func (s *Service) getEnvironmentTyped(ctx context.Context, req *getEnvironmentRequest) (*getEnvironmentResponse, *protocol.AWSError) {
	env, found := s.store.getEnv(ctx, req.ApplicationId, req.EnvironmentId)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Environment not found",
			HTTPStatus: 404,
		}
	}
	return &getEnvironmentResponse{
		ApplicationId: env.ApplicationId,
		Id:            env.ID,
		Name:          env.Name,
		Description:   env.Description,
		State:         env.State,
	}, nil
}

type listEnvironmentsRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
}

type listEnvironmentsResponse struct {
	Items []*Environment `json:"Items" cbor:"Items"`
}

func (s *Service) listEnvironmentsTyped(ctx context.Context, req *listEnvironmentsRequest) (*listEnvironmentsResponse, *protocol.AWSError) {
	envs, err := s.store.listEnvs(ctx, req.ApplicationId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listEnvironmentsResponse{Items: envs}, nil
}

type deleteEnvironmentRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	EnvironmentId string `json:"EnvironmentId" cbor:"EnvironmentId"`
}

func (s *Service) deleteEnvironmentTyped(ctx context.Context, req *deleteEnvironmentRequest) (any, *protocol.AWSError) {
	if _, found := s.store.getEnv(ctx, req.ApplicationId, req.EnvironmentId); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Environment not found",
			HTTPStatus: 404,
		}
	}
	_ = s.store.deleteEnv(ctx, req.ApplicationId, req.EnvironmentId)
	return nil, nil
}

type createConfigurationProfileRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Name          string `json:"Name" cbor:"Name"`
	LocationUri   string `json:"LocationUri" cbor:"LocationUri"`
	Type          string `json:"Type" cbor:"Type"`
}

type createConfigurationProfileResponse struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Id            string `json:"Id" cbor:"Id"`
	Name          string `json:"Name" cbor:"Name"`
	LocationUri   string `json:"LocationUri,omitempty" cbor:"LocationUri,omitempty"`
	Type          string `json:"Type,omitempty" cbor:"Type,omitempty"`
}

func (s *Service) createConfigurationProfileTyped(ctx context.Context, req *createConfigurationProfileRequest) (*createConfigurationProfileResponse, *protocol.AWSError) {
	prof := &ConfigurationProfile{
		ApplicationId: req.ApplicationId,
		ID:            shortID(),
		Name:          req.Name,
		LocationUri:   req.LocationUri,
		Type:          req.Type,
	}
	if err := s.store.putProfile(ctx, prof); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createConfigurationProfileResponse{
		ApplicationId: prof.ApplicationId,
		Id:            prof.ID,
		Name:          prof.Name,
		LocationUri:   prof.LocationUri,
		Type:          prof.Type,
	}, nil
}

type getConfigurationProfileRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
}

type getConfigurationProfileResponse struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
	Id            string `json:"Id" cbor:"Id"`
	Name          string `json:"Name" cbor:"Name"`
	LocationUri   string `json:"LocationUri,omitempty" cbor:"LocationUri,omitempty"`
	Type          string `json:"Type,omitempty" cbor:"Type,omitempty"`
}

func (s *Service) getConfigurationProfileTyped(ctx context.Context, req *getConfigurationProfileRequest) (*getConfigurationProfileResponse, *protocol.AWSError) {
	prof, found := s.store.getProfile(ctx, req.ApplicationId, req.ConfigurationProfileId)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Configuration profile not found",
			HTTPStatus: 404,
		}
	}
	return &getConfigurationProfileResponse{
		ApplicationId: prof.ApplicationId,
		Id:            prof.ID,
		Name:          prof.Name,
		LocationUri:   prof.LocationUri,
		Type:          prof.Type,
	}, nil
}

type listConfigurationProfilesRequest struct {
	ApplicationId string `json:"ApplicationId" cbor:"ApplicationId"`
}

type listConfigurationProfilesResponse struct {
	Items []*ConfigurationProfile `json:"Items" cbor:"Items"`
}

func (s *Service) listConfigurationProfilesTyped(ctx context.Context, req *listConfigurationProfilesRequest) (*listConfigurationProfilesResponse, *protocol.AWSError) {
	profiles, err := s.store.listProfiles(ctx, req.ApplicationId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	return &listConfigurationProfilesResponse{Items: profiles}, nil
}

type deleteConfigurationProfileRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
}

func (s *Service) deleteConfigurationProfileTyped(ctx context.Context, req *deleteConfigurationProfileRequest) (any, *protocol.AWSError) {
	if _, found := s.store.getProfile(ctx, req.ApplicationId, req.ConfigurationProfileId); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Configuration profile not found",
			HTTPStatus: 404,
		}
	}
	_ = s.store.deleteProfile(ctx, req.ApplicationId, req.ConfigurationProfileId)
	return nil, nil
}

type createHostedConfigurationVersionRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	Content                string `json:"Content" cbor:"Content"`
	ContentType            string `json:"ContentType" cbor:"ContentType"`
	Description            string `json:"Description" cbor:"Description"`
}

type createHostedConfigurationVersionResponse struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber" cbor:"VersionNumber"`
	ContentType            string `json:"ContentType" cbor:"ContentType"`
	Description            string `json:"Description,omitempty" cbor:"Description,omitempty"`
	Content                string `json:"Content" cbor:"Content"`
}

func (s *Service) createHostedConfigurationVersionTyped(ctx context.Context, req *createHostedConfigurationVersionRequest) (*createHostedConfigurationVersionResponse, *protocol.AWSError) {
	if _, found := s.store.getProfile(ctx, req.ApplicationId, req.ConfigurationProfileId); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Configuration profile not found",
			HTTPStatus: 404,
		}
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	versionNum, err := s.store.nextVersionNumber(ctx, req.ApplicationId, req.ConfigurationProfileId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	hcv := &HostedConfigurationVersion{
		ApplicationId:          req.ApplicationId,
		ConfigurationProfileId: req.ConfigurationProfileId,
		VersionNumber:          versionNum,
		ContentType:            contentType,
		Description:            req.Description,
		Content:                req.Content,
	}
	if err := s.store.putHCV(ctx, hcv); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createHostedConfigurationVersionResponse{
		ApplicationId:          hcv.ApplicationId,
		ConfigurationProfileId: hcv.ConfigurationProfileId,
		VersionNumber:          hcv.VersionNumber,
		ContentType:            hcv.ContentType,
		Description:            hcv.Description,
		Content:                hcv.Content,
	}, nil
}

type getHostedConfigurationVersionRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber" cbor:"VersionNumber"`
}

type getHostedConfigurationVersionResponse struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber" cbor:"VersionNumber"`
	ContentType            string `json:"ContentType" cbor:"ContentType"`
	Description            string `json:"Description,omitempty" cbor:"Description,omitempty"`
	Content                string `json:"Content" cbor:"Content"`
}

func (s *Service) getHostedConfigurationVersionTyped(ctx context.Context, req *getHostedConfigurationVersionRequest) (*getHostedConfigurationVersionResponse, *protocol.AWSError) {
	hcv, found := s.store.getHCV(ctx, req.ApplicationId, req.ConfigurationProfileId, req.VersionNumber)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Hosted configuration version not found",
			HTTPStatus: 404,
		}
	}
	return &getHostedConfigurationVersionResponse{
		ApplicationId:          hcv.ApplicationId,
		ConfigurationProfileId: hcv.ConfigurationProfileId,
		VersionNumber:          hcv.VersionNumber,
		ContentType:            hcv.ContentType,
		Description:            hcv.Description,
		Content:                hcv.Content,
	}, nil
}

type listHostedConfigurationVersionsRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
}

type hostedConfigurationVersionMeta struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber" cbor:"VersionNumber"`
	ContentType            string `json:"ContentType" cbor:"ContentType"`
	Description            string `json:"Description,omitempty" cbor:"Description,omitempty"`
}

type listHostedConfigurationVersionsResponse struct {
	Items []hostedConfigurationVersionMeta `json:"Items" cbor:"Items"`
}

func (s *Service) listHostedConfigurationVersionsTyped(ctx context.Context, req *listHostedConfigurationVersionsRequest) (*listHostedConfigurationVersionsResponse, *protocol.AWSError) {
	versions, err := s.store.listHCVs(ctx, req.ApplicationId, req.ConfigurationProfileId)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	items := make([]hostedConfigurationVersionMeta, 0, len(versions))
	for _, v := range versions {
		items = append(items, hostedConfigurationVersionMeta{
			ApplicationId:          v.ApplicationId,
			ConfigurationProfileId: v.ConfigurationProfileId,
			VersionNumber:          v.VersionNumber,
			ContentType:            v.ContentType,
			Description:            v.Description,
		})
	}
	return &listHostedConfigurationVersionsResponse{Items: items}, nil
}

type deleteHostedConfigurationVersionRequest struct {
	ApplicationId          string `json:"ApplicationId" cbor:"ApplicationId"`
	ConfigurationProfileId string `json:"ConfigurationProfileId" cbor:"ConfigurationProfileId"`
	VersionNumber          int    `json:"VersionNumber" cbor:"VersionNumber"`
}

func (s *Service) deleteHostedConfigurationVersionTyped(ctx context.Context, req *deleteHostedConfigurationVersionRequest) (any, *protocol.AWSError) {
	if _, found := s.store.getHCV(ctx, req.ApplicationId, req.ConfigurationProfileId, req.VersionNumber); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Hosted configuration version not found",
			HTTPStatus: 404,
		}
	}
	_ = s.store.deleteHCV(ctx, req.ApplicationId, req.ConfigurationProfileId, req.VersionNumber)
	return nil, nil
}
