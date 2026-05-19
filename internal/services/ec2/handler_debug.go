package ec2

import (
	"encoding/json"
	"net/http"
)

type debugVPCItem struct {
	VpcID           string `json:"vpcId"`
	CidrBlock       string `json:"cidrBlock"`
	DockerNetworkID string `json:"dockerNetworkId,omitempty"`
	NetworkStatus   string `json:"networkStatus"`
	DockerCidrBlock string `json:"dockerCidrBlock,omitempty"`
	CreateTime      int64  `json:"createTime"`
}

// DebugVPCsHandler returns an http.HandlerFunc that writes the current VPC
// state as JSON, including internal networking fields not visible in the
// standard DescribeVpcs response. Used at /_debug/ec2/vpcs.
func (s *Service) DebugVPCsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vpcs, aerr := s.handler.store.listVPCs(r.Context())
		if aerr != nil {
			http.Error(w, aerr.Message, http.StatusInternalServerError)
			return
		}
		items := make([]debugVPCItem, len(vpcs))
		for i, v := range vpcs {
			ns := v.NetworkStatus
			if ns == "" {
				ns = vpcNetworkStatusOK
			}
			items[i] = debugVPCItem{
				VpcID:           v.VpcID,
				CidrBlock:       v.CidrBlock,
				DockerNetworkID: v.DockerNetworkID,
				NetworkStatus:   ns,
				DockerCidrBlock: v.DockerCidrBlock,
				CreateTime:      v.CreateTime,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(items)
	}
}
