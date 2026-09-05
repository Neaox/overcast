package organizations

import (
	"context"

	"github.com/overcast-sh/overcast/internal/protocol"
)

type describeOrganizationRequest struct{}

type describeOrganizationResponse struct {
	Organization organizationDetails `json:"Organization" cbor:"Organization"`
}

type organizationDetails struct {
	Id                   string                    `json:"Id" cbor:"Id"`
	Arn                  string                    `json:"Arn" cbor:"Arn"`
	MasterAccountId      string                    `json:"MasterAccountId" cbor:"MasterAccountId"`
	MasterAccountArn     string                    `json:"MasterAccountArn" cbor:"MasterAccountArn"`
	MasterAccountEmail   string                    `json:"MasterAccountEmail" cbor:"MasterAccountEmail"`
	FeatureSet           string                    `json:"FeatureSet" cbor:"FeatureSet"`
	AvailablePolicyTypes []availablePolicyTypeItem `json:"AvailablePolicyTypes" cbor:"AvailablePolicyTypes"`
}

type availablePolicyTypeItem struct {
	Type   string `json:"Type" cbor:"Type"`
	Status string `json:"Status" cbor:"Status"`
}

func (s *Service) describeOrganizationTyped(_ context.Context, _ *describeOrganizationRequest) (*describeOrganizationResponse, *protocol.AWSError) {
	return &describeOrganizationResponse{
		Organization: organizationDetails{
			Id:                 s.organizationID(),
			Arn:                "arn:aws:organizations::" + s.accountID() + ":organization/" + s.organizationID(),
			MasterAccountId:    s.accountID(),
			MasterAccountArn:   s.masterAccountARN(),
			MasterAccountEmail: "admin@overcast.local",
			FeatureSet:         "ALL",
			AvailablePolicyTypes: []availablePolicyTypeItem{
				{Type: "SERVICE_CONTROL_POLICY", Status: "ENABLED"},
			},
		},
	}, nil
}
