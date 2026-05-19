package cloudformation

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/config"
)

// ── AWS::ElasticLoadBalancingV2::LoadBalancer ──────────────────────────────

type elbv2LoadBalancerHandler struct{}

func (h *elbv2LoadBalancerHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-lb", rCtx.StackName)
	}

	params := map[string]string{
		"Action":  "CreateLoadBalancer",
		"Version": "2015-12-01",
		"Name":    name,
	}
	if v, _ := props["Type"].(string); v != "" {
		params["Type"] = v
	} else {
		params["Type"] = "application"
	}
	if subnets, ok := props["Subnets"].([]any); ok {
		for i, sn := range subnets {
			if s, _ := sn.(string); s != "" {
				params[fmt.Sprintf("Subnets.member.%d", i+1)] = s
			}
		}
	}
	if sgs, ok := props["SecurityGroups"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("SecurityGroups.member.%d", i+1)] = s
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateLoadBalancer: %w", err)
	}

	body := rec.Body.String()
	arn := extractXMLValue(body, "LoadBalancerArn")
	dnsName := extractXMLValue(body, "DNSName")
	canonicalHostedZoneID := extractXMLValue(body, "CanonicalHostedZoneId")

	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", rCtx.Region, rCtx.AccountID, name)
	}

	attrs := map[string]string{
		"LoadBalancerArn":       arn,
		"DNSName":               dnsName,
		"CanonicalHostedZoneID": canonicalHostedZoneID,
	}
	return arn, attrs, nil
}

func (h *elbv2LoadBalancerHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":          "DeleteLoadBalancer",
		"Version":         "2015-12-01",
		"LoadBalancerArn": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elbv2LoadBalancerHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newName, _ := props["Name"].(string); newName != "" {
			if oldName, _ := oldProps["Name"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":          "ModifyLoadBalancerAttributes",
		"Version":         "2015-12-01",
		"LoadBalancerArn": physicalID,
	}
	if v, ok := props["LoadBalancerAttributes"]; ok {
		if attrs, ok := v.([]any); ok {
			for _, a := range attrs {
				if am, ok := a.(map[string]any); ok {
					if key, _ := am["Key"].(string); key != "" {
						if val, _ := am["Value"].(string); val != "" {
							params[fmt.Sprintf("Attributes.member.%d.Key", len(params))] = key
							params[fmt.Sprintf("Attributes.member.%d.Value", len(params))] = val
						}
					}
				}
			}
		}
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyLoadBalancerAttributes: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ElasticLoadBalancingV2::TargetGroup ───────────────────────────────

type elbv2TargetGroupHandler struct{}

func (h *elbv2TargetGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-tg", rCtx.StackName)
	}

	params := map[string]string{
		"Action":  "CreateTargetGroup",
		"Version": "2015-12-01",
		"Name":    name,
	}
	if v, _ := props["Protocol"].(string); v != "" {
		params["Protocol"] = v
	} else {
		params["Protocol"] = "HTTP"
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	} else {
		params["Port"] = "80"
	}
	if v, _ := props["VpcId"].(string); v != "" {
		params["VpcId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateTargetGroup: %w", err)
	}

	body := rec.Body.String()
	arn := extractXMLValue(body, "TargetGroupArn")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s", rCtx.Region, rCtx.AccountID, name)
	}

	return arn, map[string]string{"TargetGroupArn": arn}, nil
}

func (h *elbv2TargetGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":         "DeleteTargetGroup",
		"Version":        "2015-12-01",
		"TargetGroupArn": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elbv2TargetGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newName, _ := props["Name"].(string); newName != "" {
			if oldName, _ := oldProps["Name"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
	}
	return "", nil, errReplacementRequired
}

// ── AWS::ElasticLoadBalancingV2::Listener ──────────────────────────────────

type elbv2ListenerHandler struct{}

func (h *elbv2ListenerHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	params := map[string]string{
		"Action":  "CreateListener",
		"Version": "2015-12-01",
	}
	if v, _ := props["LoadBalancerArn"].(string); v != "" {
		params["LoadBalancerArn"] = v
	}
	if v, _ := props["Protocol"].(string); v != "" {
		params["Protocol"] = v
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	}
	if actions, ok := props["DefaultActions"].([]any); ok {
		for i, a := range actions {
			if am, ok := a.(map[string]any); ok {
				if t, _ := am["Type"].(string); t != "" {
					params[fmt.Sprintf("DefaultActions.member.%d.Type", i+1)] = t
				}
				if targetGroupArn, _ := am["TargetGroupArn"].(string); targetGroupArn != "" {
					params[fmt.Sprintf("DefaultActions.member.%d.TargetGroupArn", i+1)] = targetGroupArn
				}
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateListener: %w", err)
	}

	body := rec.Body.String()
	arn := extractXMLValue(body, "ListenerArn")
	if arn == "" {
		arn = fmt.Sprintf("%s-listener", rCtx.StackName)
	}

	return arn, map[string]string{"ListenerArn": arn}, nil
}

func (h *elbv2ListenerHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":      "DeleteListener",
		"Version":     "2015-12-01",
		"ListenerArn": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elbv2ListenerHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newLBArn, _ := props["LoadBalancerArn"].(string); newLBArn != "" {
			if oldLBArn, _ := oldProps["LoadBalancerArn"].(string); oldLBArn != "" && newLBArn != oldLBArn {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":      "ModifyListener",
		"Version":     "2015-12-01",
		"ListenerArn": physicalID,
	}
	if v, _ := props["Protocol"].(string); v != "" {
		params["Protocol"] = v
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	}
	if actions, ok := props["DefaultActions"].([]any); ok {
		for i, a := range actions {
			if am, ok := a.(map[string]any); ok {
				if t, _ := am["Type"].(string); t != "" {
					params[fmt.Sprintf("DefaultActions.member.%d.Type", i+1)] = t
				}
				if targetGroupArn, _ := am["TargetGroupArn"].(string); targetGroupArn != "" {
					params[fmt.Sprintf("DefaultActions.member.%d.TargetGroupArn", i+1)] = targetGroupArn
				}
			}
		}
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyListener: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::AutoScaling::AutoScalingGroup ─────────────────────────────────────

type autoscalingASGHandler struct{}

func (h *autoscalingASGHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["AutoScalingGroupName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-asg", rCtx.StackName)
	}

	params := map[string]string{
		"Action":               "CreateAutoScalingGroup",
		"Version":              "2011-01-01",
		"AutoScalingGroupName": name,
	}
	if v := fmtPropString(props, "MinSize"); v != "" {
		params["MinSize"] = v
	} else {
		params["MinSize"] = "0"
	}
	if v := fmtPropString(props, "MaxSize"); v != "" {
		params["MaxSize"] = v
	} else {
		params["MaxSize"] = "1"
	}
	if v := fmtPropString(props, "DesiredCapacity"); v != "" {
		params["DesiredCapacity"] = v
	}
	if azs, ok := props["AvailabilityZones"].([]any); ok {
		for i, az := range azs {
			if s, _ := az.(string); s != "" {
				params[fmt.Sprintf("AvailabilityZones.member.%d", i+1)] = s
			}
		}
	}
	if v, _ := props["VPCZoneIdentifier"].(string); v != "" {
		params["VPCZoneIdentifier"] = v
	}
	if v, _ := props["LaunchConfigurationName"].(string); v != "" {
		params["LaunchConfigurationName"] = v
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAutoScalingGroup: %w", err)
	}

	return name, nil, nil
}

func (h *autoscalingASGHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":               "DeleteAutoScalingGroup",
		"Version":              "2011-01-01",
		"AutoScalingGroupName": physicalID,
		"ForceDelete":          "true",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *autoscalingASGHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newName, _ := props["AutoScalingGroupName"].(string); newName != "" {
			if oldName, _ := oldProps["AutoScalingGroupName"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":               "UpdateAutoScalingGroup",
		"Version":              "2011-01-01",
		"AutoScalingGroupName": physicalID,
	}
	if v := fmtPropString(props, "MinSize"); v != "" {
		params["MinSize"] = v
	}
	if v := fmtPropString(props, "MaxSize"); v != "" {
		params["MaxSize"] = v
	}
	if v := fmtPropString(props, "DesiredCapacity"); v != "" {
		params["DesiredCapacity"] = v
	}
	if v, _ := props["VPCZoneIdentifier"].(string); v != "" {
		params["VPCZoneIdentifier"] = v
	}
	if azs, ok := props["AvailabilityZones"].([]any); ok {
		for i, az := range azs {
			if s, _ := az.(string); s != "" {
				params[fmt.Sprintf("AvailabilityZones.member.%d", i+1)] = s
			}
		}
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("UpdateAutoScalingGroup: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::AutoScaling::LaunchConfiguration ──────────────────────────────────

type autoscalingLaunchConfigHandler struct{}

func (h *autoscalingLaunchConfigHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["LaunchConfigurationName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-lc", rCtx.StackName)
	}

	params := map[string]string{
		"Action":                  "CreateLaunchConfiguration",
		"Version":                 "2011-01-01",
		"LaunchConfigurationName": name,
	}
	if v, _ := props["ImageId"].(string); v != "" {
		params["ImageId"] = v
	} else {
		params["ImageId"] = "ami-dummy"
	}
	if v, _ := props["InstanceType"].(string); v != "" {
		params["InstanceType"] = v
	} else {
		params["InstanceType"] = "t2.micro"
	}
	if sgs, ok := props["SecurityGroups"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("SecurityGroups.member.%d", i+1)] = s
			}
		}
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateLaunchConfiguration: %w", err)
	}

	return name, nil, nil
}

func (h *autoscalingLaunchConfigHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":                  "DeleteLaunchConfiguration",
		"Version":                 "2011-01-01",
		"LaunchConfigurationName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *autoscalingLaunchConfigHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Route53::HostedZone ───────────────────────────────────────────────

type route53HostedZoneHandler struct{}

func (h *route53HostedZoneHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s.example.com", rCtx.StackName)
	}
	if !strings.HasSuffix(name, ".") {
		name += "."
	}

	callerRef := fmt.Sprintf("%s-%d", rCtx.StackName, len(rCtx.Resources))

	type createHostedZoneRequest struct {
		XMLName         xml.Name `xml:"CreateHostedZoneRequest"`
		Xmlns           string   `xml:"xmlns,attr"`
		Name            string   `xml:"Name"`
		CallerReference string   `xml:"CallerReference"`
	}
	req := createHostedZoneRequest{
		Xmlns:           "https://route53.amazonaws.com/doc/2013-04-01/",
		Name:            name,
		CallerReference: callerRef,
	}
	xmlBytes, err := xml.Marshal(req)
	if err != nil {
		return "", nil, fmt.Errorf("Route53: marshal request: %w", err)
	}

	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/2013-04-01/hostedzone", "application/xml", xmlBytes)
	if err != nil {
		return "", nil, fmt.Errorf("CreateHostedZone: %w", err)
	}

	body := rec.Body.String()
	id := extractXMLValue(body, "Id")
	zoneID := id
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		zoneID = id[idx+1:]
	}

	attrs := map[string]string{
		"Id":   id,
		"Name": name,
	}
	return zoneID, attrs, nil
}

func (h *route53HostedZoneHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/2013-04-01/hostedzone/"+physicalID, "", nil)
	if err != nil {
		return fmt.Errorf("DeleteHostedZone: %w", err)
	}
	return nil
}

func (h *route53HostedZoneHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Route53::RecordSet ────────────────────────────────────────────────

type route53RecordSetHandler struct{}

func (h *route53RecordSetHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	hostedZoneID, _ := props["HostedZoneId"].(string)
	recordName, _ := props["Name"].(string)
	recordType, _ := props["Type"].(string)

	if recordName == "" {
		recordName = fmt.Sprintf("%s.example.com", rCtx.StackName)
	}
	if !strings.HasSuffix(recordName, ".") {
		recordName += "."
	}
	if recordType == "" {
		recordType = "A"
	}

	ttl := int64(300)
	if v := fmtPropInt(props, "TTL"); v != 0 {
		ttl = v
	}

	var resourceRecords []string
	if records, ok := props["ResourceRecords"].([]any); ok {
		for _, rr := range records {
			if s, _ := rr.(string); s != "" {
				resourceRecords = append(resourceRecords, s)
			}
		}
	}

	type rrXML struct {
		Value string `xml:"Value"`
	}
	type rrsXML struct {
		Name            string  `xml:"Name"`
		Type            string  `xml:"Type"`
		TTL             int64   `xml:"TTL"`
		ResourceRecords []rrXML `xml:"ResourceRecords>ResourceRecord"`
	}
	type changeXML struct {
		Action            string `xml:"Action"`
		ResourceRecordSet rrsXML `xml:"ResourceRecordSet"`
	}
	type batchXML struct {
		XMLName xml.Name    `xml:"ChangeResourceRecordSetsRequest"`
		Xmlns   string      `xml:"xmlns,attr"`
		Changes []changeXML `xml:"ChangeBatch>Changes>Change"`
	}

	rrlist := make([]rrXML, len(resourceRecords))
	for i, v := range resourceRecords {
		rrlist[i] = rrXML{Value: v}
	}

	batch := batchXML{
		Xmlns: "https://route53.amazonaws.com/doc/2013-04-01/",
		Changes: []changeXML{
			{
				Action: "CREATE",
				ResourceRecordSet: rrsXML{
					Name:            recordName,
					Type:            recordType,
					TTL:             ttl,
					ResourceRecords: rrlist,
				},
			},
		},
	}
	xmlBytes, err := xml.Marshal(batch)
	if err != nil {
		return "", nil, fmt.Errorf("Route53: marshal ChangeResourceRecordSets: %w", err)
	}

	path := fmt.Sprintf("/2013-04-01/hostedzone/%s/rrset", hostedZoneID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/xml", xmlBytes); err != nil {
		return "", nil, fmt.Errorf("ChangeResourceRecordSets: %w", err)
	}

	physicalID := fmt.Sprintf("%s/%s/%s", hostedZoneID, recordName, recordType)
	return physicalID, nil, nil
}

func (h *route53RecordSetHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 3)
	if len(parts) < 3 {
		return nil
	}
	hostedZoneID, recordName, recordType := parts[0], parts[1], parts[2]

	type rrXML struct {
		Value string `xml:"Value"`
	}
	type rrsXML struct {
		Name            string  `xml:"Name"`
		Type            string  `xml:"Type"`
		TTL             int64   `xml:"TTL"`
		ResourceRecords []rrXML `xml:"ResourceRecords>ResourceRecord"`
	}
	type changeXML struct {
		Action            string `xml:"Action"`
		ResourceRecordSet rrsXML `xml:"ResourceRecordSet"`
	}
	type batchXML struct {
		XMLName xml.Name    `xml:"ChangeResourceRecordSetsRequest"`
		Xmlns   string      `xml:"xmlns,attr"`
		Changes []changeXML `xml:"ChangeBatch>Changes>Change"`
	}

	batch := batchXML{
		Xmlns: "https://route53.amazonaws.com/doc/2013-04-01/",
		Changes: []changeXML{
			{
				Action: "DELETE",
				ResourceRecordSet: rrsXML{
					Name:            recordName,
					Type:            recordType,
					TTL:             300,
					ResourceRecords: nil,
				},
			},
		},
	}
	xmlBytes, err := xml.Marshal(batch)
	if err != nil {
		return fmt.Errorf("Route53: marshal ChangeResourceRecordSets (delete): %w", err)
	}

	path := fmt.Sprintf("/2013-04-01/hostedzone/%s/rrset", hostedZoneID)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/xml", xmlBytes); err != nil {
		return fmt.Errorf("ChangeResourceRecordSets (delete): %w", err)
	}
	return nil
}

func (h *route53RecordSetHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::Cluster ──────────────────────────────────────────────────────

type eksClusterHandler struct{}

func (h *eksClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-eks", rCtx.StackName)
	}

	body := map[string]any{
		"name": name,
	}
	if v, ok := props["RoleArn"].(string); ok && v != "" {
		body["roleArn"] = v
	}
	if v, ok := props["Version"].(string); ok && v != "" {
		body["version"] = v
	}
	if v, ok := props["ResourcesVpcConfig"].(map[string]any); ok && v != nil {
		body["resourcesVpcConfig"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreateCluster", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateCluster: %w", err)
	}

	var resp struct {
		Cluster struct {
			Arn string `json:"arn"`
		} `json:"cluster"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateCluster: parse response: %w", err)
	}

	arn := resp.Cluster.Arn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", rCtx.Region, rCtx.AccountID, name)
	}

	return arn, map[string]string{"Arn": arn}, nil
}

func (h *eksClusterHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"name": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeleteCluster", body)
	if err != nil {
		return fmt.Errorf("DeleteCluster: %w", err)
	}
	return nil
}

func (h *eksClusterHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::Nodegroup ────────────────────────────────────────────────────

type eksNodegroupHandler struct{}

func (h *eksNodegroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	nodegroupName, _ := props["NodegroupName"].(string)
	if nodegroupName == "" {
		nodegroupName = fmt.Sprintf("%s-ng", rCtx.StackName)
	}

	body := map[string]any{
		"clusterName":   clusterName,
		"nodegroupName": nodegroupName,
	}
	if v, ok := props["NodeRole"].(string); ok && v != "" {
		body["nodeRole"] = v
	}
	if v, ok := props["Subnets"].([]any); ok {
		body["subnets"] = v
	}
	if v, ok := props["ScalingConfig"].(map[string]any); ok && v != nil {
		body["scalingConfig"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreateNodegroup", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateNodegroup: %w", err)
	}

	var resp struct {
		Nodegroup struct {
			NodegroupArn string `json:"nodegroupArn"`
		} `json:"nodegroup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateNodegroup: parse response: %w", err)
	}

	arn := resp.Nodegroup.NodegroupArn
	if arn == "" {
		arn = fmt.Sprintf("%s/%s", clusterName, nodegroupName)
	}

	physicalID := fmt.Sprintf("%s/%s", clusterName, nodegroupName)
	return physicalID, map[string]string{"Arn": arn}, nil
}

func (h *eksNodegroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	clusterName, nodegroupName := parts[0], parts[1]

	body := map[string]any{
		"clusterName":   clusterName,
		"nodegroupName": nodegroupName,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeleteNodegroup", body)
	if err != nil {
		return fmt.Errorf("DeleteNodegroup: %w", err)
	}
	return nil
}

func (h *eksNodegroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::FargateProfile ───────────────────────────────────────────────

type eksFargateProfileHandler struct{}

func (h *eksFargateProfileHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	fargateProfileName, _ := props["FargateProfileName"].(string)
	if fargateProfileName == "" {
		fargateProfileName = fmt.Sprintf("%s-fp", rCtx.StackName)
	}

	body := map[string]any{
		"clusterName":        clusterName,
		"fargateProfileName": fargateProfileName,
	}
	if v, ok := props["PodExecutionRoleArn"].(string); ok && v != "" {
		body["podExecutionRoleArn"] = v
	}
	if v, ok := props["Selectors"].([]any); ok {
		body["selectors"] = v
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreateFargateProfile", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateFargateProfile: %w", err)
	}

	physicalID := fmt.Sprintf("%s/%s", clusterName, fargateProfileName)
	return physicalID, nil, nil
}

func (h *eksFargateProfileHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	clusterName, fargateProfileName := parts[0], parts[1]

	body := map[string]any{
		"clusterName":        clusterName,
		"fargateProfileName": fargateProfileName,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeleteFargateProfile", body)
	if err != nil {
		return fmt.Errorf("DeleteFargateProfile: %w", err)
	}
	return nil
}

func (h *eksFargateProfileHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::Addon ────────────────────────────────────────────────────────

type eksAddonHandler struct{}

func (h *eksAddonHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	addonName, _ := props["AddonName"].(string)
	if addonName == "" {
		addonName = fmt.Sprintf("%s-addon", rCtx.StackName)
	}

	body := map[string]any{
		"clusterName": clusterName,
		"addonName":   addonName,
	}
	if v, ok := props["AddonVersion"].(string); ok && v != "" {
		body["addonVersion"] = v
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreateAddon", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAddon: %w", err)
	}

	physicalID := fmt.Sprintf("%s/%s", clusterName, addonName)
	return physicalID, nil, nil
}

func (h *eksAddonHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	clusterName, addonName := parts[0], parts[1]

	body := map[string]any{
		"clusterName": clusterName,
		"addonName":   addonName,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeleteAddon", body)
	if err != nil {
		return fmt.Errorf("DeleteAddon: %w", err)
	}
	return nil
}

func (h *eksAddonHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::AccessEntry ──────────────────────────────────────────────────

type eksAccessEntryHandler struct{}

func (h *eksAccessEntryHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	principalArn, _ := props["PrincipalArn"].(string)

	body := map[string]any{
		"clusterName":  clusterName,
		"principalArn": principalArn,
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreateAccessEntry", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAccessEntry: %w", err)
	}

	physicalID := fmt.Sprintf("%s/%s", clusterName, principalArn)
	return physicalID, nil, nil
}

func (h *eksAccessEntryHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) < 2 {
		return nil
	}
	clusterName, principalArn := parts[0], parts[1]

	body := map[string]any{
		"clusterName":  clusterName,
		"principalArn": principalArn,
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeleteAccessEntry", body)
	if err != nil {
		return fmt.Errorf("DeleteAccessEntry: %w", err)
	}
	return nil
}

func (h *eksAccessEntryHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::EKS::PodIdentityAssociation ───────────────────────────────────────

type eksPodIdentityAssociationHandler struct{}

func (h *eksPodIdentityAssociationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	namespace, _ := props["Namespace"].(string)
	serviceAccount, _ := props["ServiceAccount"].(string)
	roleArn, _ := props["RoleArn"].(string)

	body := map[string]any{
		"clusterName":    clusterName,
		"namespace":      namespace,
		"serviceAccount": serviceAccount,
		"roleArn":        roleArn,
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "EKS.CreatePodIdentityAssociation", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreatePodIdentityAssociation: %w", err)
	}

	var resp struct {
		AssociationId string `json:"associationId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreatePodIdentityAssociation: parse response: %w", err)
	}

	physicalID := resp.AssociationId
	if physicalID == "" {
		physicalID = fmt.Sprintf("%s/%s/%s", clusterName, namespace, serviceAccount)
	}

	return physicalID, nil, nil
}

func (h *eksPodIdentityAssociationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"associationId": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "EKS.DeletePodIdentityAssociation", body)
	if err != nil {
		return fmt.Errorf("DeletePodIdentityAssociation: %w", err)
	}
	return nil
}

func (h *eksPodIdentityAssociationHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::MSK::Cluster ──────────────────────────────────────────────────────

type mskClusterHandler struct{}

func (h *mskClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	clusterName, _ := props["ClusterName"].(string)
	if clusterName == "" {
		clusterName = fmt.Sprintf("%s-msk", rCtx.StackName)
	}

	body := map[string]any{
		"clusterName": clusterName,
	}
	if v, ok := props["KafkaVersion"].(string); ok && v != "" {
		body["kafkaVersion"] = v
	} else {
		body["kafkaVersion"] = "2.8.1"
	}
	if v := fmtPropInt(props, "NumberOfBrokerNodes"); v != 0 {
		body["numberOfBrokerNodes"] = v
	} else {
		body["numberOfBrokerNodes"] = 3
	}
	if v, ok := props["BrokerNodeGroupInfo"].(map[string]any); ok && v != nil {
		body["brokerNodeGroupInfo"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "Kafka.CreateCluster", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateCluster: %w", err)
	}

	var resp struct {
		ClusterArn string `json:"clusterArn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateCluster: parse response: %w", err)
	}

	arn := resp.ClusterArn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/%s", rCtx.Region, rCtx.AccountID, clusterName)
	}

	return arn, map[string]string{"Arn": arn}, nil
}

func (h *mskClusterHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"clusterArn": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "Kafka.DeleteCluster", body)
	if err != nil {
		return fmt.Errorf("DeleteCluster: %w", err)
	}
	return nil
}

func (h *mskClusterHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::MSK::Configuration ────────────────────────────────────────────────

type mskConfigurationHandler struct{}

func (h *mskConfigurationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-mskcfg", rCtx.StackName)
	}

	body := map[string]any{
		"name": name,
	}
	if v, ok := props["KafkaVersions"].([]any); ok {
		body["kafkaVersions"] = v
	}
	if v, ok := props["ServerProperties"].(string); ok && v != "" {
		body["serverProperties"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "Kafka.CreateConfiguration", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateConfiguration: %w", err)
	}

	var resp struct {
		Arn string `json:"arn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateConfiguration: parse response: %w", err)
	}

	arn := resp.Arn
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:kafka:%s:%s:configuration/%s", rCtx.Region, rCtx.AccountID, name)
	}

	return arn, map[string]string{"Arn": arn}, nil
}

func (h *mskConfigurationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"arn": physicalID}
	_, err := internalJSON(ctx, router, rCtx.Region, "Kafka.DeleteConfiguration", body)
	if err != nil {
		return fmt.Errorf("DeleteConfiguration: %w", err)
	}
	return nil
}

func (h *mskConfigurationHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::Pipes::Pipe ───────────────────────────────────────────────────────

type pipesPipeHandler struct{}

func (h *pipesPipeHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-pipe", rCtx.StackName)
	}

	body := map[string]any{}
	if v, ok := props["Source"].(string); ok && v != "" {
		body["source"] = v
	}
	if v, ok := props["Target"].(string); ok && v != "" {
		body["target"] = v
	}
	if v, ok := props["RoleArn"].(string); ok && v != "" {
		body["roleArn"] = v
	}

	jsonBytes, err := json.Marshal(body)
	if err != nil {
		return "", nil, fmt.Errorf("Pipes: marshal request: %w", err)
	}

	path := "/v1/pipes/" + name
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", jsonBytes)
	if err != nil {
		return "", nil, fmt.Errorf("CreatePipe: %w", err)
	}

	var resp struct {
		Arn string `json:"Arn"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreatePipe: parse response: %w", err)
	}

	physicalID := resp.Arn
	if physicalID == "" {
		physicalID = name
	}

	return physicalID, nil, nil
}

func (h *pipesPipeHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v1/pipes/"+physicalID, "", nil)
	if err != nil {
		return fmt.Errorf("DeletePipe: %w", err)
	}
	return nil
}

func (h *pipesPipeHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::IAM::User ─────────────────────────────────────────────────────────

type iamUserHandler struct{}

func (h *iamUserHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	userName, _ := props["UserName"].(string)
	if userName == "" {
		userName = fmt.Sprintf("%s-user", rCtx.StackName)
	}

	params := map[string]string{
		"Action":   "CreateUser",
		"Version":  "2010-05-08",
		"UserName": userName,
	}
	if v, _ := props["Path"].(string); v != "" {
		params["Path"] = v
	}
	if v, _ := props["PermissionsBoundary"].(string); v != "" {
		params["PermissionsBoundary"] = v
	}
	if tags, ok := props["Tags"].([]any); ok {
		for i, t := range tags {
			if tm, ok := t.(map[string]any); ok {
				if k, _ := tm["Key"].(string); k != "" {
					if v, _ := tm["Value"].(string); v != "" {
						params[fmt.Sprintf("Tags.member.%d.Key", i+1)] = k
						params[fmt.Sprintf("Tags.member.%d.Value", i+1)] = v
					}
				}
			}
		}
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateUser: %w", err)
	}

	return userName, nil, nil
}

func (h *iamUserHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":   "DeleteUser",
		"Version":  "2010-05-08",
		"UserName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *iamUserHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newName, _ := props["UserName"].(string); newName != "" {
			if oldName, _ := oldProps["UserName"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":   "UpdateUser",
		"Version":  "2010-05-08",
		"UserName": physicalID,
	}
	if v, _ := props["NewPath"].(string); v != "" {
		params["NewPath"] = v
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("UpdateUser: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::IAM::AccessKey ────────────────────────────────────────────────────

type iamAccessKeyHandler struct{}

func (h *iamAccessKeyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	userName, _ := props["UserName"].(string)

	params := map[string]string{
		"Action":   "CreateAccessKey",
		"Version":  "2010-05-08",
		"UserName": userName,
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateAccessKey: %w", err)
	}

	body := rec.Body.String()
	accessKeyID := extractXMLValue(body, "AccessKeyId")

	physicalID := fmt.Sprintf("%s/%s", userName, accessKeyID)
	return physicalID, nil, nil
}

func (h *iamAccessKeyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	userName := ""
	accessKeyID := physicalID
	if len(parts) == 2 {
		userName = parts[0]
		accessKeyID = parts[1]
	}

	params := map[string]string{
		"Action":      "DeleteAccessKey",
		"Version":     "2010-05-08",
		"AccessKeyId": accessKeyID,
	}
	if userName != "" {
		params["UserName"] = userName
	}

	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *iamAccessKeyHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::WAFv2::WebACL ─────────────────────────────────────────────────────

type wafv2WebACLHandler struct{}

func (h *wafv2WebACLHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-waf", rCtx.StackName)
	}

	scope, _ := props["Scope"].(string)
	if scope == "" {
		scope = "REGIONAL"
	}

	body := map[string]any{
		"Name":  name,
		"Scope": scope,
	}
	if v, ok := props["DefaultAction"].(map[string]any); ok && v != nil {
		body["DefaultAction"] = v
	}
	if v, ok := props["VisibilityConfig"].(map[string]any); ok && v != nil {
		body["VisibilityConfig"] = v
	}
	if v, ok := props["Rules"].([]any); ok {
		body["Rules"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSWAF_20190729.CreateWebACL", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateWebACL: %w", err)
	}

	var resp struct {
		Summary struct {
			ARN string `json:"ARN"`
			Id  string `json:"Id"`
		} `json:"Summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateWebACL: parse response: %w", err)
	}

	physicalID := fmt.Sprintf("%s/%s", scope, resp.Summary.Id)
	return physicalID, nil, nil
}

func (h *wafv2WebACLHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	scope := "REGIONAL"
	id := physicalID
	if len(parts) == 2 {
		scope = parts[0]
		id = parts[1]
	}

	body := map[string]any{
		"Id":        id,
		"Scope":     scope,
		"LockToken": fmt.Sprintf("%s-%d", rCtx.StackName, len(rCtx.Resources)),
	}
	_, err := internalJSON(ctx, router, rCtx.Region, "AWSWAF_20190729.DeleteWebACL", body)
	if err != nil {
		return fmt.Errorf("DeleteWebACL: %w", err)
	}
	return nil
}

func (h *wafv2WebACLHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── Helpers ────────────────────────────────────────────────────────────────

func fmtPropInt(props map[string]any, key string) int64 {
	v, ok := props[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int:
		return int64(x)
	case int64:
		return x
	case string:
		if x != "" {
			var i int64
			fmt.Sscanf(x, "%d", &i)
			return i
		}
	}
	return 0
}
