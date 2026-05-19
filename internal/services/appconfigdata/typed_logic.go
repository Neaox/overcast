package appconfigdata

import (
	"context"
	"strconv"

	"github.com/Neaox/overcast/internal/protocol"
)

type startConfigurationSessionRequest struct {
	ApplicationIdentifier          string `json:"ApplicationIdentifier" cbor:"ApplicationIdentifier"`
	EnvironmentIdentifier          string `json:"EnvironmentIdentifier" cbor:"EnvironmentIdentifier"`
	ConfigurationProfileIdentifier string `json:"ConfigurationProfileIdentifier" cbor:"ConfigurationProfileIdentifier"`
}

type startConfigurationSessionResponse struct {
	InitialConfigurationToken string `json:"InitialConfigurationToken" cbor:"InitialConfigurationToken"`
}

func (s *Service) startConfigurationSessionTyped(ctx context.Context, req *startConfigurationSessionRequest) (*startConfigurationSessionResponse, *protocol.AWSError) {
	app, found := s.ac.ResolveApplication(ctx, req.ApplicationIdentifier)
	if !found {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Application not found",
			HTTPStatus: 404,
		}
	}
	if _, found := s.ac.ResolveEnvironment(ctx, app.ID, req.EnvironmentIdentifier); !found {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Environment not found",
			HTTPStatus: 404,
		}
	}
	prof, found := s.ac.ResolveProfile(ctx, app.ID, req.ConfigurationProfileIdentifier)
	if !found {
		return nil, &protocol.AWSError{
			Code: "ResourceNotFoundException", Message: "Configuration profile not found",
			HTTPStatus: 404,
		}
	}

	token := newToken()
	sess := &session{
		Token:           token,
		AppID:           app.ID,
		EnvID:           req.EnvironmentIdentifier,
		ProfileID:       prof.ID,
		LastVersionSeen: 0,
	}
	if err := s.store.putSession(ctx, sess); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &startConfigurationSessionResponse{InitialConfigurationToken: token}, nil
}

type getLatestConfigurationRequest struct {
	ConfigurationToken string `json:"ConfigurationToken" cbor:"ConfigurationToken"`
}

type getLatestConfigurationResponse struct {
	Content                    string `json:"Content,omitempty" cbor:"Content,omitempty"`
	ContentType                string `json:"ContentType,omitempty" cbor:"ContentType,omitempty"`
	ConfigurationVersion       string `json:"ConfigurationVersion,omitempty" cbor:"ConfigurationVersion,omitempty"`
	NextPollConfigurationToken string `json:"NextPollConfigurationToken" cbor:"NextPollConfigurationToken"`
	NextPollIntervalInSeconds  int    `json:"NextPollIntervalInSeconds" cbor:"NextPollIntervalInSeconds"`
}

func (s *Service) getLatestConfigurationTyped(ctx context.Context, req *getLatestConfigurationRequest) (*getLatestConfigurationResponse, *protocol.AWSError) {
	sess, found := s.store.getSession(ctx, req.ConfigurationToken)
	if !found {
		return nil, &protocol.AWSError{
			Code: "BadRequestException", Message: "Invalid or expired configuration token",
			HTTPStatus: 400,
		}
	}

	latestVersion, err := s.ac.LatestVersionNumber(ctx, sess.AppID, sess.ProfileID)
	if err != nil {
		return nil, protocol.ErrInternalError
	}

	nextToken := newToken()
	updatedSess := &session{
		Token:           nextToken,
		AppID:           sess.AppID,
		EnvID:           sess.EnvID,
		ProfileID:       sess.ProfileID,
		LastVersionSeen: latestVersion,
	}
	if err := s.store.putSession(ctx, updatedSess); err != nil {
		return nil, protocol.ErrInternalError
	}
	_ = s.store.store.Delete(ctx, nsSessions, req.ConfigurationToken)

	if latestVersion == 0 || latestVersion == sess.LastVersionSeen {
		return &getLatestConfigurationResponse{
			NextPollConfigurationToken: nextToken,
			NextPollIntervalInSeconds:  60,
		}, nil
	}

	hcv, found := s.ac.GetHostedConfigVersionByNum(ctx, sess.AppID, sess.ProfileID, latestVersion)
	if !found {
		return &getLatestConfigurationResponse{
			NextPollConfigurationToken: nextToken,
			NextPollIntervalInSeconds:  60,
		}, nil
	}

	return &getLatestConfigurationResponse{
		Content:                    hcv.Content,
		ContentType:                hcv.ContentType,
		ConfigurationVersion:       strconv.Itoa(hcv.VersionNumber),
		NextPollConfigurationToken: nextToken,
		NextPollIntervalInSeconds:  60,
	}, nil
}
