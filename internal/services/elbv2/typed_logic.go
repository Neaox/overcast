package elbv2

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

type createLoadBalancerReq struct {
	Name   string `json:"Name"`
	Type   string `json:"Type"`
	Scheme string `json:"Scheme"`
	VpcId  string `json:"VpcId"`
}

type describeLoadBalancersReq struct {
	LoadBalancerArns []string `json:"LoadBalancerArns"`
	Names            []string `json:"Names"`
}

type deleteLoadBalancerReq struct {
	LoadBalancerArn string `json:"LoadBalancerArn"`
}

type createTargetGroupReq struct {
	Name       string `json:"Name"`
	Protocol   string `json:"Protocol"`
	Port       int    `json:"Port"`
	VpcId      string `json:"VpcId"`
	TargetType string `json:"TargetType"`
}

type describeTargetGroupsReq struct {
	TargetGroupArns []string `json:"TargetGroupArns"`
	Names           []string `json:"Names"`
}

type deleteTargetGroupReq struct {
	TargetGroupArn string `json:"TargetGroupArn"`
}

type createListenerReq struct {
	LoadBalancerArn string   `json:"LoadBalancerArn"`
	Protocol        string   `json:"Protocol"`
	Port            int      `json:"Port"`
	DefaultActions  []Action `json:"DefaultActions"`
}

type describeListenersReq struct {
	LoadBalancerArn string   `json:"LoadBalancerArn"`
	ListenerArns    []string `json:"ListenerArns"`
}

type deleteListenerReq struct {
	ListenerArn string `json:"ListenerArn"`
}

type registerTargetsReq struct {
	TargetGroupArn string        `json:"TargetGroupArn"`
	Targets        []elbv2Target `json:"Targets"`
}

type deregisterTargetsReq struct {
	TargetGroupArn string        `json:"TargetGroupArn"`
	Targets        []elbv2Target `json:"Targets"`
}

type describeTargetHealthReq struct {
	TargetGroupArn string `json:"TargetGroupArn"`
}

type elbv2Target struct {
	Id   string `json:"Id"`
	Port int    `json:"Port"`
}

type xmlTypedCreateLoadBalancerResponse struct {
	XMLName          struct{}                         `xml:"CreateLoadBalancerResponse"`
	Xmlns            string                           `xml:"xmlns,attr"`
	Result           xmlTypedCreateLoadBalancerResult `xml:"CreateLoadBalancerResult"`
	ResponseMetadata protocol.ResponseMetadata        `xml:"ResponseMetadata"`
}

type xmlTypedCreateLoadBalancerResult struct {
	LoadBalancers xmlTypedLBs `xml:"LoadBalancers"`
}

type xmlTypedLBs struct {
	Items []xmlLB `xml:"member"`
}

type xmlTypedDescribeLoadBalancersResponse struct {
	XMLName          struct{}                            `xml:"DescribeLoadBalancersResponse"`
	Xmlns            string                              `xml:"xmlns,attr"`
	Result           xmlTypedDescribeLoadBalancersResult `xml:"DescribeLoadBalancersResult"`
	ResponseMetadata protocol.ResponseMetadata           `xml:"ResponseMetadata"`
}

type xmlTypedDescribeLoadBalancersResult struct {
	LoadBalancers xmlTypedLBs `xml:"LoadBalancers"`
}

type xmlTypedDeleteLoadBalancerResponse struct {
	XMLName          struct{}                  `xml:"DeleteLoadBalancerResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteLoadBalancerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedCreateTargetGroupResponse struct {
	XMLName          struct{}                        `xml:"CreateTargetGroupResponse"`
	Xmlns            string                          `xml:"xmlns,attr"`
	Result           xmlTypedCreateTargetGroupResult `xml:"CreateTargetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata       `xml:"ResponseMetadata"`
}

type xmlTypedCreateTargetGroupResult struct {
	TargetGroups xmlTypedTGs `xml:"TargetGroups"`
}

type xmlTypedTGs struct {
	Items []xmlTG `xml:"member"`
}

type xmlTypedDescribeTargetGroupsResponse struct {
	XMLName          struct{}                           `xml:"DescribeTargetGroupsResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlTypedDescribeTargetGroupsResult `xml:"DescribeTargetGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}

type xmlTypedDescribeTargetGroupsResult struct {
	TargetGroups xmlTypedTGs `xml:"TargetGroups"`
}

type xmlTypedDeleteTargetGroupResponse struct {
	XMLName          struct{}                  `xml:"DeleteTargetGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteTargetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedCreateListenerResponse struct {
	XMLName          struct{}                     `xml:"CreateListenerResponse"`
	Xmlns            string                       `xml:"xmlns,attr"`
	Result           xmlTypedCreateListenerResult `xml:"CreateListenerResult"`
	ResponseMetadata protocol.ResponseMetadata    `xml:"ResponseMetadata"`
}

type xmlTypedCreateListenerResult struct {
	Listeners xmlTypedListeners `xml:"Listeners"`
}

type xmlTypedListeners struct {
	Items []xmlListener `xml:"member"`
}

type xmlTypedDescribeListenersResponse struct {
	XMLName          struct{}                        `xml:"DescribeListenersResponse"`
	Xmlns            string                          `xml:"xmlns,attr"`
	Result           xmlTypedDescribeListenersResult `xml:"DescribeListenersResult"`
	ResponseMetadata protocol.ResponseMetadata       `xml:"ResponseMetadata"`
}

type xmlTypedDescribeListenersResult struct {
	Listeners xmlTypedListeners `xml:"Listeners"`
}

type xmlTypedDeleteListenerResponse struct {
	XMLName          struct{}                  `xml:"DeleteListenerResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteListenerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedRegisterTargetsResponse struct {
	XMLName          struct{}                  `xml:"RegisterTargetsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"RegisterTargetsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedDeregisterTargetsResponse struct {
	XMLName          struct{}                  `xml:"DeregisterTargetsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeregisterTargetsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedDescribeTargetHealthResponse struct {
	XMLName          struct{}                           `xml:"DescribeTargetHealthResponse"`
	Xmlns            string                             `xml:"xmlns,attr"`
	Result           xmlTypedDescribeTargetHealthResult `xml:"DescribeTargetHealthResult"`
	ResponseMetadata protocol.ResponseMetadata          `xml:"ResponseMetadata"`
}

type xmlTypedDescribeTargetHealthResult struct {
	TargetHealthDescriptions xmlTypedTargetHealthDescriptions `xml:"TargetHealthDescriptions"`
}

type xmlTypedTargetHealthDescriptions struct {
	Items []xmlTargetHealthDescription `xml:"member"`
}

func (h *Handler) createLoadBalancerTyped(ctx context.Context, req *createLoadBalancerReq) (*xmlTypedCreateLoadBalancerResponse, *protocol.AWSError) {
	name := req.Name
	if name == "" {
		return nil, errMissingParam("Name")
	}
	lbType := req.Type
	if lbType == "" {
		lbType = "application"
	}
	scheme := req.Scheme
	if scheme == "" {
		scheme = "internet-facing"
	}
	region := h.region(ctx)
	account := h.accountID()

	lbID := uuid.NewString()
	arn := loadBalancerARN(region, account, lbType, name, lbID[:8])
	dnsName := h.loadBalancerDNSName(name, lbID, region)

	lb := &LoadBalancer{
		LoadBalancerArn:  arn,
		LoadBalancerName: name,
		DNSName:          dnsName,
		Type:             lbType,
		Scheme:           scheme,
		State:            "active",
		VpcId:            req.VpcId,
		CreatedTime:      h.clk.Now(),
		Region:           region,
	}
	if err := h.putLB(ctx, region, lb); err != nil {
		return nil, protocol.ErrInternalError
	}

	return &xmlTypedCreateLoadBalancerResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedCreateLoadBalancerResult{LoadBalancers: xmlTypedLBs{Items: []xmlLB{toLBXML(lb)}}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) describeLoadBalancersTyped(ctx context.Context, req *describeLoadBalancersReq) (*xmlTypedDescribeLoadBalancersResponse, *protocol.AWSError) {
	region := h.region(ctx)
	lbs, err := h.listLBs(ctx, region)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	lbs = selectLBs(lbs, req.LoadBalancerArns, req.Names)
	sort.Slice(lbs, func(i, j int) bool { return lbs[i].LoadBalancerName < lbs[j].LoadBalancerName })

	xmlLBs := make([]xmlLB, 0, len(lbs))
	for _, lb := range lbs {
		xmlLBs = append(xmlLBs, toLBXML(lb))
	}
	return &xmlTypedDescribeLoadBalancersResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedDescribeLoadBalancersResult{LoadBalancers: xmlTypedLBs{Items: xmlLBs}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) deleteLoadBalancerTyped(ctx context.Context, req *deleteLoadBalancerReq) (*xmlTypedDeleteLoadBalancerResponse, *protocol.AWSError) {
	arn := req.LoadBalancerArn
	if arn == "" {
		return nil, errMissingParam("LoadBalancerArn")
	}
	region := h.region(ctx)
	if _, found, err := h.getLB(ctx, region, arn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, errNotFound("LoadBalancer", arn)
	}
	if err := h.deleteLB(ctx, region, arn); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &xmlTypedDeleteLoadBalancerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) createTargetGroupTyped(ctx context.Context, req *createTargetGroupReq) (*xmlTypedCreateTargetGroupResponse, *protocol.AWSError) {
	name := req.Name
	if name == "" {
		return nil, errMissingParam("Name")
	}
	tgType := req.TargetType
	if tgType == "" {
		tgType = "instance"
	}
	region := h.region(ctx)
	account := h.accountID()

	tgID := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/%s",
		region, account, name, tgID[:12])

	tg := &TargetGroup{
		TargetGroupArn:  arn,
		TargetGroupName: name,
		Protocol:        req.Protocol,
		Port:            req.Port,
		VpcId:           req.VpcId,
		TargetType:      tgType,
		Region:          region,
	}
	if err := h.putTG(ctx, region, tg); err != nil {
		return nil, protocol.ErrInternalError
	}

	return &xmlTypedCreateTargetGroupResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedCreateTargetGroupResult{TargetGroups: xmlTypedTGs{Items: []xmlTG{toTGXML(tg)}}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) describeTargetGroupsTyped(ctx context.Context, req *describeTargetGroupsReq) (*xmlTypedDescribeTargetGroupsResponse, *protocol.AWSError) {
	region := h.region(ctx)
	tgs, err := h.listTGs(ctx, region)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	tgs = selectTGs(tgs, req.TargetGroupArns, req.Names)
	sort.Slice(tgs, func(i, j int) bool { return tgs[i].TargetGroupName < tgs[j].TargetGroupName })

	xmlTGs := make([]xmlTG, 0, len(tgs))
	for _, tg := range tgs {
		xmlTGs = append(xmlTGs, toTGXML(tg))
	}
	return &xmlTypedDescribeTargetGroupsResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedDescribeTargetGroupsResult{TargetGroups: xmlTypedTGs{Items: xmlTGs}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) deleteTargetGroupTyped(ctx context.Context, req *deleteTargetGroupReq) (*xmlTypedDeleteTargetGroupResponse, *protocol.AWSError) {
	arn := req.TargetGroupArn
	if arn == "" {
		return nil, errMissingParam("TargetGroupArn")
	}
	region := h.region(ctx)
	if _, found, err := h.getTG(ctx, region, arn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, errTGNotFound(arn)
	}
	if err := h.deleteTG(ctx, region, arn); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &xmlTypedDeleteTargetGroupResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) createListenerTyped(ctx context.Context, req *createListenerReq) (*xmlTypedCreateListenerResponse, *protocol.AWSError) {
	lbArn := req.LoadBalancerArn
	if lbArn == "" {
		return nil, errMissingParam("LoadBalancerArn")
	}
	region := h.region(ctx)

	if _, found, err := h.getLB(ctx, region, lbArn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, errNotFound("LoadBalancer", lbArn)
	}

	lID := uuid.NewString()

	l := &Listener{
		ListenerArn:     listenerARN(lbArn, lID[:12]),
		LoadBalancerArn: lbArn,
		Protocol:        req.Protocol,
		Port:            req.Port,
		Region:          region,
		DefaultActions:  req.DefaultActions,
	}
	if err := h.putListener(ctx, region, l); err != nil {
		return nil, protocol.ErrInternalError
	}

	return &xmlTypedCreateListenerResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedCreateListenerResult{Listeners: xmlTypedListeners{Items: []xmlListener{toListenerXML(l)}}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) describeListenersTyped(ctx context.Context, req *describeListenersReq) (*xmlTypedDescribeListenersResponse, *protocol.AWSError) {
	region := h.region(ctx)
	listeners, err := h.listListenersByLB(ctx, region, req.LoadBalancerArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}

	f := newIdentifierFilter(req.ListenerArns)
	xmlLs := make([]xmlListener, 0, len(listeners))
	for _, l := range listeners {
		if !f.matches(l.ListenerArn) {
			continue
		}
		xmlLs = append(xmlLs, toListenerXML(l))
	}
	return &xmlTypedDescribeListenersResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedDescribeListenersResult{Listeners: xmlTypedListeners{Items: xmlLs}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) deleteListenerTyped(ctx context.Context, req *deleteListenerReq) (*xmlTypedDeleteListenerResponse, *protocol.AWSError) {
	arn := req.ListenerArn
	if arn == "" {
		return nil, errMissingParam("ListenerArn")
	}
	region := h.region(ctx)
	if _, found, err := h.getListener(ctx, region, arn); err != nil {
		return nil, protocol.ErrInternalError
	} else if !found {
		return nil, errNotFound("Listener", arn)
	}
	if err := h.store.Delete(ctx, nsListeners, listenerKey(region, arn)); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &xmlTypedDeleteListenerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) registerTargetsTyped(ctx context.Context, req *registerTargetsReq) (*xmlTypedRegisterTargetsResponse, *protocol.AWSError) {
	tgArn := req.TargetGroupArn
	if tgArn == "" {
		return nil, errMissingParam("TargetGroupArn")
	}
	region := h.region(ctx)

	for _, t := range req.Targets {
		target := &Target{TargetGroupArn: tgArn, Id: t.Id, Port: t.Port}
		if err := h.putTarget(ctx, region, target); err != nil {
			return nil, protocol.ErrInternalError
		}
	}
	return &xmlTypedRegisterTargetsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) deregisterTargetsTyped(ctx context.Context, req *deregisterTargetsReq) (*xmlTypedDeregisterTargetsResponse, *protocol.AWSError) {
	tgArn := req.TargetGroupArn
	if tgArn == "" {
		return nil, errMissingParam("TargetGroupArn")
	}
	region := h.region(ctx)

	for _, t := range req.Targets {
		_ = h.removeTarget(ctx, region, tgArn, t.Id)
	}
	return &xmlTypedDeregisterTargetsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) describeTargetHealthTyped(ctx context.Context, req *describeTargetHealthReq) (*xmlTypedDescribeTargetHealthResponse, *protocol.AWSError) {
	tgArn := req.TargetGroupArn
	if tgArn == "" {
		return nil, errMissingParam("TargetGroupArn")
	}
	region := h.region(ctx)

	targets, err := h.listTargets(ctx, region, tgArn)
	if err != nil {
		return nil, protocol.ErrInternalError
	}

	members := make([]xmlTargetHealthDescription, 0, len(targets))
	for _, t := range targets {
		var desc xmlTargetHealthDescription
		desc.Target.Id = t.Id
		desc.Target.Port = t.Port
		desc.TargetHealth.State = "healthy"
		members = append(members, desc)
	}
	return &xmlTypedDescribeTargetHealthResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedDescribeTargetHealthResult{TargetHealthDescriptions: xmlTypedTargetHealthDescriptions{Items: members}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// Tag operation request types.

type elbv2Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type addTagsReq struct {
	ResourceArns []string   `json:"ResourceArns"`
	Tags         []elbv2Tag `json:"Tags"`
}

type removeTagsReq struct {
	ResourceArns []string `json:"ResourceArns"`
	TagKeys      []string `json:"TagKeys"`
}

type describeTagsReq struct {
	ResourceArns []string `json:"ResourceArns"`
}

type xmlTypedAddTagsResponse struct {
	XMLName          struct{}                  `xml:"AddTagsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"AddTagsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedRemoveTagsResponse struct {
	XMLName          struct{}                  `xml:"RemoveTagsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"RemoveTagsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlTypedDescribeTagsResponse struct {
	XMLName          struct{}                   `xml:"DescribeTagsResponse"`
	Xmlns            string                     `xml:"xmlns,attr"`
	Result           xmlTypedDescribeTagsResult `xml:"DescribeTagsResult"`
	ResponseMetadata protocol.ResponseMetadata  `xml:"ResponseMetadata"`
}

type xmlTypedDescribeTagsResult struct {
	TagDescriptions xmlTypedTagDescriptions `xml:"TagDescriptions"`
}

type xmlTypedTagDescriptions struct {
	Items []xmlTagDescription `xml:"member"`
}

func (h *Handler) addTagsTyped(ctx context.Context, req *addTagsReq) (*xmlTypedAddTagsResponse, *protocol.AWSError) {
	if len(req.ResourceArns) == 0 {
		return nil, errMissingParam("ResourceArns")
	}
	region := h.region(ctx)

	tagMap := map[string]string{}
	for _, t := range req.Tags {
		if _, exists := tagMap[t.Key]; exists {
			return nil, &protocol.AWSError{
				Code: "DuplicateTagKeys", Message: "Duplicate tag keys: " + t.Key,
				HTTPStatus: http.StatusBadRequest,
			}
		}
		tagMap[t.Key] = t.Value
	}

	for _, arn := range req.ResourceArns {
		if lb, found, err := h.getLB(ctx, region, arn); err != nil {
			return nil, protocol.ErrInternalError
		} else if found {
			if lb.Tags == nil {
				lb.Tags = map[string]string{}
			}
			for k, v := range tagMap {
				lb.Tags[k] = v
			}
			if err := h.putLB(ctx, region, lb); err != nil {
				return nil, protocol.ErrInternalError
			}
			continue
		}
		if tg, found, err := h.getTG(ctx, region, arn); err != nil {
			return nil, protocol.ErrInternalError
		} else if found {
			if tg.Tags == nil {
				tg.Tags = map[string]string{}
			}
			for k, v := range tagMap {
				tg.Tags[k] = v
			}
			if err := h.putTG(ctx, region, tg); err != nil {
				return nil, protocol.ErrInternalError
			}
			continue
		}
		return nil, errNotFound("Resource", arn)
	}

	return &xmlTypedAddTagsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) removeTagsTyped(ctx context.Context, req *removeTagsReq) (*xmlTypedRemoveTagsResponse, *protocol.AWSError) {
	if len(req.ResourceArns) == 0 {
		return nil, errMissingParam("ResourceArns")
	}
	region := h.region(ctx)

	for _, arn := range req.ResourceArns {
		if lb, found, err := h.getLB(ctx, region, arn); err != nil {
			return nil, protocol.ErrInternalError
		} else if found {
			if lb.Tags != nil {
				for _, k := range req.TagKeys {
					delete(lb.Tags, k)
				}
			}
			if err := h.putLB(ctx, region, lb); err != nil {
				return nil, protocol.ErrInternalError
			}
			continue
		}
		if tg, found, err := h.getTG(ctx, region, arn); err != nil {
			return nil, protocol.ErrInternalError
		} else if found {
			if tg.Tags != nil {
				for _, k := range req.TagKeys {
					delete(tg.Tags, k)
				}
			}
			if err := h.putTG(ctx, region, tg); err != nil {
				return nil, protocol.ErrInternalError
			}
			continue
		}
		return nil, errNotFound("Resource", arn)
	}

	return &xmlTypedRemoveTagsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

func (h *Handler) describeTagsTyped(ctx context.Context, req *describeTagsReq) (*xmlTypedDescribeTagsResponse, *protocol.AWSError) {
	region := h.region(ctx)

	var descs []xmlTagDescription

	if len(req.ResourceArns) == 0 {
		lbs, err := h.listLBs(ctx, region)
		if err != nil {
			return nil, protocol.ErrInternalError
		}
		tgs, err := h.listTGs(ctx, region)
		if err != nil {
			return nil, protocol.ErrInternalError
		}
		for _, lb := range lbs {
			descs = append(descs, tagDescXML(lb.LoadBalancerArn, lb.Tags))
		}
		for _, tg := range tgs {
			descs = append(descs, tagDescXML(tg.TargetGroupArn, tg.Tags))
		}
	} else {
		for _, arn := range req.ResourceArns {
			if lb, found, err := h.getLB(ctx, region, arn); err != nil {
				return nil, protocol.ErrInternalError
			} else if found {
				descs = append(descs, tagDescXML(lb.LoadBalancerArn, lb.Tags))
				continue
			}
			if tg, found, err := h.getTG(ctx, region, arn); err != nil {
				return nil, protocol.ErrInternalError
			} else if found {
				descs = append(descs, tagDescXML(tg.TargetGroupArn, tg.Tags))
				continue
			}
			return nil, errNotFound("Resource", arn)
		}
	}

	if descs == nil {
		descs = []xmlTagDescription{}
	}

	return &xmlTypedDescribeTagsResponse{
		Xmlns:            elbv2XMLNS,
		Result:           xmlTypedDescribeTagsResult{TagDescriptions: xmlTypedTagDescriptions{Items: descs}},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}
