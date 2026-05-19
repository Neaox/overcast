package organizations

import (
	"context"

	"github.com/Neaox/overcast/internal/protocol"
)

type describeOrganizationRequest struct{}

type describeOrganizationResponse struct {
	Organization organizationDetails `json:"Organization" cbor:"Organization"`
}

type organizationDetails struct {
	Id                   string                   `json:"Id" cbor:"Id"`
	Arn                  string                   `json:"Arn" cbor:"Arn"`
	MasterAccountId      string                   `json:"MasterAccountId" cbor:"MasterAccountId"`
	MasterUserEmail      string                   `json:"MasterUserEmail" cbor:"MasterUserEmail"`
	FeatureSet           string                   `json:"FeatureSet" cbor:"FeatureSet"`
	AvailablePolicyTypes []availablePolicyTypeItem `json:"AvailablePolicyTypes" cbor:"AvailablePolicyTypes"`
}

type availablePolicyTypeItem struct {
	Type   string `json:"Type" cbor:"Type"`
	Status string `json:"Status" cbor:"Status"`
}

func (s *Service) describeOrganizationTyped(_ context.Context, _ *describeOrganizationRequest) (*describeOrganizationResponse, *protocol.AWSError) {
	return &describeOrganizationResponse{
		Organization: organizationDetails{
			Id:              "o-overcast",
			Arn:             "arn:aws:organizations::000000000000:organization/o-overcast",
			MasterAccountId: s.accountID(),
			MasterUserEmail: "admin@overcast.local",
			FeatureSet:      "ALL",
			AvailablePolicyTypes: []availablePolicyTypeItem{
				{Type: "SERVICE_CONTROL_POLICY", Status: "ENABLED"},
			},
		},
	}, nil
}
