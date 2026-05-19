package appconfig

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateApplication": op.NewTyped[createApplicationRequest, createApplicationResponse](
			"CreateApplication", s.createApplicationTyped,
		),
		"GetApplication": op.NewTyped[getApplicationRequest, getApplicationResponse](
			"GetApplication", s.getApplicationTyped,
		),
		"ListApplications": op.NewTyped[listApplicationsRequest, listApplicationsResponse](
			"ListApplications", s.listApplicationsTyped,
		),
		"DeleteApplication": op.NewTypedAny[deleteApplicationRequest](
			"DeleteApplication", s.deleteApplicationTyped,
		),
		"CreateEnvironment": op.NewTyped[createEnvironmentRequest, createEnvironmentResponse](
			"CreateEnvironment", s.createEnvironmentTyped,
		),
		"GetEnvironment": op.NewTyped[getEnvironmentRequest, getEnvironmentResponse](
			"GetEnvironment", s.getEnvironmentTyped,
		),
		"ListEnvironments": op.NewTyped[listEnvironmentsRequest, listEnvironmentsResponse](
			"ListEnvironments", s.listEnvironmentsTyped,
		),
		"DeleteEnvironment": op.NewTypedAny[deleteEnvironmentRequest](
			"DeleteEnvironment", s.deleteEnvironmentTyped,
		),
		"CreateConfigurationProfile": op.NewTyped[createConfigurationProfileRequest, createConfigurationProfileResponse](
			"CreateConfigurationProfile", s.createConfigurationProfileTyped,
		),
		"GetConfigurationProfile": op.NewTyped[getConfigurationProfileRequest, getConfigurationProfileResponse](
			"GetConfigurationProfile", s.getConfigurationProfileTyped,
		),
		"ListConfigurationProfiles": op.NewTyped[listConfigurationProfilesRequest, listConfigurationProfilesResponse](
			"ListConfigurationProfiles", s.listConfigurationProfilesTyped,
		),
		"DeleteConfigurationProfile": op.NewTypedAny[deleteConfigurationProfileRequest](
			"DeleteConfigurationProfile", s.deleteConfigurationProfileTyped,
		),
		"CreateHostedConfigurationVersion": op.NewTyped[createHostedConfigurationVersionRequest, createHostedConfigurationVersionResponse](
			"CreateHostedConfigurationVersion", s.createHostedConfigurationVersionTyped,
		),
		"GetHostedConfigurationVersion": op.NewTyped[getHostedConfigurationVersionRequest, getHostedConfigurationVersionResponse](
			"GetHostedConfigurationVersion", s.getHostedConfigurationVersionTyped,
		),
		"ListHostedConfigurationVersions": op.NewTyped[listHostedConfigurationVersionsRequest, listHostedConfigurationVersionsResponse](
			"ListHostedConfigurationVersions", s.listHostedConfigurationVersionsTyped,
		),
		"DeleteHostedConfigurationVersion": op.NewTypedAny[deleteHostedConfigurationVersionRequest](
			"DeleteHostedConfigurationVersion", s.deleteHostedConfigurationVersionTyped,
		),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, operation := range ops {
		out = append(out, operation)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}
