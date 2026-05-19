package route53

import (
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (s *Service) typedOps() map[string]op.Operation {
	return map[string]op.Operation{
		"CreateHostedZone":       op.NewTyped[r53CreateHostedZoneReq, r53CreateHostedZoneResp]("CreateHostedZone", s.createHostedZoneTyped),
		"ListHostedZones":        op.NewTyped[r53ListHostedZonesReq, r53ListHostedZonesResp]("ListHostedZones", s.listHostedZonesTyped),
		"GetHostedZone":          op.NewTyped[r53GetHostedZoneReq, r53GetHostedZoneResp]("GetHostedZone", s.getHostedZoneTyped),
		"DeleteHostedZone":       op.NewTyped[r53DeleteHostedZoneReq, r53DeleteHostedZoneResp]("DeleteHostedZone", s.deleteHostedZoneTyped),
		"ListResourceRecordSets": op.NewTyped[r53ListResourceRecordSetsReq, r53ListResourceRecordSetsResp]("ListResourceRecordSets", s.listResourceRecordSetsTyped),
		"GetChange":              op.NewTyped[r53GetChangeReq, r53GetChangeResp]("GetChange", s.getChangeTyped),
	}
}

func (s *Service) Operations() []op.Operation {
	ops := s.typedOp
	out := make([]op.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	return out
}

func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.QueryXML}
}

