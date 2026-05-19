package elbv2

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

type Handler struct {
	cfg     *config.Config
	store   state.Store
	clk     clock.Clock
	log     *serviceutil.ServiceLogger
	ops     map[string]http.HandlerFunc
	typedOp map[string]op.Operation
}

func newHandler(cfg *config.Config, store state.Store, log *serviceutil.ServiceLogger, clk clock.Clock) *Handler {
	h := &Handler{
		cfg:   cfg,
		store: store,
		clk:   clk,
		log:   log,
	}
	h.initOps()
	return h
}

func (h *Handler) initOps() {
	h.ops = map[string]http.HandlerFunc{
		"CreateLoadBalancer":   h.CreateLoadBalancer,
		"DescribeLoadBalancers": h.DescribeLoadBalancers,
		"DeleteLoadBalancer":   h.DeleteLoadBalancer,
		"CreateTargetGroup":    h.CreateTargetGroup,
		"DescribeTargetGroups": h.DescribeTargetGroups,
		"DeleteTargetGroup":    h.DeleteTargetGroup,
		"CreateListener":       h.CreateListener,
		"DescribeListeners":    h.DescribeListeners,
		"DeleteListener":       h.DeleteListener,
		"RegisterTargets":      h.RegisterTargets,
		"DeregisterTargets":    h.DeregisterTargets,
		"DescribeTargetHealth": h.DescribeTargetHealth,
	}
	h.typedOp = h.typedOps()
}

func (h *Handler) ownsAction(action string) bool {
	_, ok := h.ops[action]
	return ok
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	action := r.FormValue("Action")
	if fn, ok := h.ops[action]; ok {
		fn(w, r)
		return
	}
	protocol.NotImplementedQueryXML(w, r)
}

func (h *Handler) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, h.cfg.Region)
}

func (h *Handler) accountID() string {
	if h.cfg != nil && strings.TrimSpace(h.cfg.AccountID) != "" {
		return h.cfg.AccountID
	}
	return "000000000000"
}

func lbKey(region, arn string) string {
	return serviceutil.RegionKey(region, arn)
}

func tgKey(region, arn string) string {
	return serviceutil.RegionKey(region, arn)
}

func listenerKey(region, arn string) string {
	return serviceutil.RegionKey(region, arn)
}

func targetKey(region, tgArn, targetID string) string {
	return serviceutil.RegionKey(region, tgArn+"/"+targetID)
}

func (h *Handler) putLB(ctx context.Context, region string, lb *LoadBalancer) error {
	raw, err := json.Marshal(lb)
	if err != nil {
		return err
	}
	return h.store.Set(ctx, nsLBs, lbKey(region, lb.LoadBalancerArn), string(raw))
}

func (h *Handler) getLB(ctx context.Context, region, arn string) (*LoadBalancer, bool, error) {
	raw, found, err := h.store.Get(ctx, nsLBs, lbKey(region, arn))
	if err != nil || !found {
		return nil, found, err
	}
	var lb LoadBalancer
	if err := json.Unmarshal([]byte(raw), &lb); err != nil {
		return nil, false, err
	}
	return &lb, true, nil
}

func (h *Handler) listLBs(ctx context.Context, region string) ([]*LoadBalancer, error) {
	pairs, err := h.store.Scan(ctx, nsLBs, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]*LoadBalancer, 0, len(pairs))
	for _, kv := range pairs {
		var lb LoadBalancer
		if err := json.Unmarshal([]byte(kv.Value), &lb); err != nil {
			continue
		}
		out = append(out, &lb)
	}
	return out, nil
}

func (h *Handler) deleteLB(ctx context.Context, region, arn string) error {
	return h.store.Delete(ctx, nsLBs, lbKey(region, arn))
}

func (h *Handler) putTG(ctx context.Context, region string, tg *TargetGroup) error {
	raw, err := json.Marshal(tg)
	if err != nil {
		return err
	}
	return h.store.Set(ctx, nsTGs, tgKey(region, tg.TargetGroupArn), string(raw))
}

func (h *Handler) getTG(ctx context.Context, region, arn string) (*TargetGroup, bool, error) {
	raw, found, err := h.store.Get(ctx, nsTGs, tgKey(region, arn))
	if err != nil || !found {
		return nil, found, err
	}
	var tg TargetGroup
	if err := json.Unmarshal([]byte(raw), &tg); err != nil {
		return nil, false, err
	}
	return &tg, true, nil
}

func (h *Handler) listTGs(ctx context.Context, region string) ([]*TargetGroup, error) {
	pairs, err := h.store.Scan(ctx, nsTGs, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]*TargetGroup, 0, len(pairs))
	for _, kv := range pairs {
		var tg TargetGroup
		if err := json.Unmarshal([]byte(kv.Value), &tg); err != nil {
			continue
		}
		out = append(out, &tg)
	}
	return out, nil
}

func (h *Handler) deleteTG(ctx context.Context, region, arn string) error {
	return h.store.Delete(ctx, nsTGs, tgKey(region, arn))
}

func (h *Handler) putListener(ctx context.Context, region string, l *Listener) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return h.store.Set(ctx, nsListeners, listenerKey(region, l.ListenerArn), string(raw))
}

func (h *Handler) getListener(ctx context.Context, region, arn string) (*Listener, bool, error) {
	raw, found, err := h.store.Get(ctx, nsListeners, listenerKey(region, arn))
	if err != nil || !found {
		return nil, found, err
	}
	var l Listener
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return nil, false, err
	}
	return &l, true, nil
}

func (h *Handler) listListenersByLB(ctx context.Context, region, lbArn string) ([]*Listener, error) {
	pairs, err := h.store.Scan(ctx, nsListeners, serviceutil.RegionKey(region, ""))
	if err != nil {
		return nil, err
	}
	out := make([]*Listener, 0)
	for _, kv := range pairs {
		var l Listener
		if err := json.Unmarshal([]byte(kv.Value), &l); err != nil {
			continue
		}
		if lbArn == "" || l.LoadBalancerArn == lbArn {
			out = append(out, &l)
		}
	}
	return out, nil
}

func (h *Handler) putTarget(ctx context.Context, region string, t *Target) error {
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return h.store.Set(ctx, nsTargets, targetKey(region, t.TargetGroupArn, t.Id), string(raw))
}

func (h *Handler) listTargets(ctx context.Context, region, tgArn string) ([]*Target, error) {
	prefix := serviceutil.RegionKey(region, tgArn+"/")
	pairs, err := h.store.Scan(ctx, nsTargets, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Target, 0, len(pairs))
	for _, kv := range pairs {
		var t Target
		if err := json.Unmarshal([]byte(kv.Value), &t); err != nil {
			continue
		}
		out = append(out, &t)
	}
	return out, nil
}

func (h *Handler) removeTarget(ctx context.Context, region, tgArn, targetID string) error {
	return h.store.Delete(ctx, nsTargets, targetKey(region, tgArn, targetID))
}

func toLBXML(lb *LoadBalancer) xmlLB {
	x := xmlLB{
		LoadBalancerArn:  lb.LoadBalancerArn,
		LoadBalancerName: lb.LoadBalancerName,
		DNSName:          lb.DNSName,
		Type:             lb.Type,
		Scheme:           lb.Scheme,
		VpcId:            lb.VpcId,
		CreatedTime:      lb.CreatedTime.UTC().Format(time.RFC3339),
	}
	x.State.Code = lb.State
	return x
}

func toTGXML(tg *TargetGroup) xmlTG {
	return xmlTG{
		TargetGroupArn:  tg.TargetGroupArn,
		TargetGroupName: tg.TargetGroupName,
		Protocol:        tg.Protocol,
		Port:            tg.Port,
		VpcId:           tg.VpcId,
		TargetType:      tg.TargetType,
	}
}

func toListenerXML(l *Listener) xmlListener {
	return xmlListener{
		ListenerArn:     l.ListenerArn,
		LoadBalancerArn: l.LoadBalancerArn,
		Protocol:        l.Protocol,
		Port:            l.Port,
	}
}

func formInt(r *http.Request, key string, defaultVal int) int {
	v := r.FormValue(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

func collectMemberParams(r *http.Request, prefix string) []string {
	var out []string
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.member.%d", prefix, i))
		if v == "" {
			break
		}
		out = append(out, v)
	}
	return out
}

func errMissingParam(param string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationError",
		Message:    param + " is required",
		HTTPStatus: http.StatusBadRequest,
	}
}

func errNotFound(resourceType, arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "LoadBalancerNotFound",
		Message:    fmt.Sprintf("%s not found: %s", resourceType, arn),
		HTTPStatus: http.StatusNotFound,
	}
}

func errTGNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "TargetGroupNotFound",
		Message:    "Target group not found: " + arn,
		HTTPStatus: http.StatusNotFound,
	}
}

var _ = chi.NewRouter
var _ = uuid.NewString
var _ = zap.NewNop
var _ = xml.Unmarshal

func (h *Handler) CreateLoadBalancer(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Name")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("Name"))
		return
	}
	lbType := r.FormValue("Type")
	if lbType == "" {
		lbType = "application"
	}
	scheme := r.FormValue("Scheme")
	if scheme == "" {
		scheme = "internet-facing"
	}
	vpcID := r.FormValue("VpcId")
	region := h.region(r.Context())
	account := h.accountID()

	lbID := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s/%s/%s",
		region, account, lbType, name, lbID[:8])
	dnsName := fmt.Sprintf("%s-%s.%s.elb.localhost", name, lbID[:8], region)

	lb := &LoadBalancer{
		LoadBalancerArn:  arn,
		LoadBalancerName: name,
		DNSName:          dnsName,
		Type:             lbType,
		Scheme:           scheme,
		State:            "active",
		VpcId:            vpcID,
		CreatedTime:      h.clk.Now(),
		Region:           region,
	}
	if err := h.putLB(r.Context(), region, lb); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	resp := &xmlCreateLoadBalancerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.LoadBalancers.Member = []xmlLB{toLBXML(lb)}
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DescribeLoadBalancers(w http.ResponseWriter, r *http.Request) {
	region := h.region(r.Context())
	lbs, err := h.listLBs(r.Context(), region)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}
	arns := collectMemberParams(r, "LoadBalancerArns")
	if len(arns) > 0 {
		arnSet := make(map[string]bool, len(arns))
		for _, a := range arns {
			arnSet[a] = true
		}
		filtered := lbs[:0]
		for _, lb := range lbs {
			if arnSet[lb.LoadBalancerArn] {
				filtered = append(filtered, lb)
			}
		}
		lbs = filtered
	}
	sort.Slice(lbs, func(i, j int) bool { return lbs[i].LoadBalancerName < lbs[j].LoadBalancerName })

	xmlLBs := make([]xmlLB, 0, len(lbs))
	for _, lb := range lbs {
		xmlLBs = append(xmlLBs, toLBXML(lb))
	}
	resp := &xmlDescribeLoadBalancersResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.LoadBalancers.Member = xmlLBs
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DeleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("LoadBalancerArn")
	region := h.region(r.Context())
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("LoadBalancerArn"))
		return
	}
	if _, found, err := h.getLB(r.Context(), region, arn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteQueryXMLError(w, r, errNotFound("LoadBalancer", arn))
		return
	}
	if err := h.deleteLB(r.Context(), region, arn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteLoadBalancerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) CreateTargetGroup(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("Name")
	if name == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("Name"))
		return
	}
	proto := r.FormValue("Protocol")
	port := formInt(r, "Port", 80)
	vpcID := r.FormValue("VpcId")
	tgType := r.FormValue("TargetType")
	if tgType == "" {
		tgType = "instance"
	}
	region := h.region(r.Context())
	account := h.accountID()

	tgID := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/%s",
		region, account, name, tgID[:12])

	tg := &TargetGroup{
		TargetGroupArn:  arn,
		TargetGroupName: name,
		Protocol:        proto,
		Port:            port,
		VpcId:           vpcID,
		TargetType:      tgType,
		Region:          region,
	}
	if err := h.putTG(r.Context(), region, tg); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	resp := &xmlCreateTargetGroupResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.TargetGroups.Member = []xmlTG{toTGXML(tg)}
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DescribeTargetGroups(w http.ResponseWriter, r *http.Request) {
	region := h.region(r.Context())
	tgs, err := h.listTGs(r.Context(), region)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}
	sort.Slice(tgs, func(i, j int) bool { return tgs[i].TargetGroupName < tgs[j].TargetGroupName })

	xmlTGs := make([]xmlTG, 0, len(tgs))
	for _, tg := range tgs {
		xmlTGs = append(xmlTGs, toTGXML(tg))
	}
	resp := &xmlDescribeTargetGroupsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.TargetGroups.Member = xmlTGs
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DeleteTargetGroup(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("TargetGroupArn")
	region := h.region(r.Context())
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("TargetGroupArn"))
		return
	}
	if _, found, err := h.getTG(r.Context(), region, arn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteQueryXMLError(w, r, errTGNotFound(arn))
		return
	}
	if err := h.deleteTG(r.Context(), region, arn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteTargetGroupResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) CreateListener(w http.ResponseWriter, r *http.Request) {
	lbArn := r.FormValue("LoadBalancerArn")
	if lbArn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("LoadBalancerArn"))
		return
	}
	proto := r.FormValue("Protocol")
	port := formInt(r, "Port", 80)
	region := h.region(r.Context())
	account := h.accountID()

	if _, found, err := h.getLB(r.Context(), region, lbArn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteQueryXMLError(w, r, errNotFound("LoadBalancer", lbArn))
		return
	}

	lID := uuid.NewString()
	arn := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/app/listener/%s",
		region, account, lID[:12])

	l := &Listener{
		ListenerArn:     arn,
		LoadBalancerArn: lbArn,
		Protocol:        proto,
		Port:            port,
		Region:          region,
	}
	if err := h.putListener(r.Context(), region, l); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	resp := &xmlCreateListenerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.Listeners.Member = []xmlListener{toListenerXML(l)}
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DescribeListeners(w http.ResponseWriter, r *http.Request) {
	lbArn := r.FormValue("LoadBalancerArn")
	region := h.region(r.Context())

	listeners, err := h.listListenersByLB(r.Context(), region, lbArn)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	xmlLs := make([]xmlListener, 0, len(listeners))
	for _, l := range listeners {
		xmlLs = append(xmlLs, toListenerXML(l))
	}
	resp := &xmlDescribeListenersResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.Listeners.Member = xmlLs
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

func (h *Handler) DeleteListener(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ListenerArn")
	region := h.region(r.Context())
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("ListenerArn"))
		return
	}
	if _, found, err := h.getListener(r.Context(), region, arn); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	} else if !found {
		protocol.WriteQueryXMLError(w, r, errNotFound("Listener", arn))
		return
	}
	if err := h.store.Delete(r.Context(), nsListeners, listenerKey(region, arn)); err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteListenerResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) RegisterTargets(w http.ResponseWriter, r *http.Request) {
	tgArn := r.FormValue("TargetGroupArn")
	if tgArn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("TargetGroupArn"))
		return
	}
	region := h.region(r.Context())

	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("Targets.member.%d.Id", i))
		if id == "" {
			break
		}
		port := formInt(r, fmt.Sprintf("Targets.member.%d.Port", i), 0)
		t := &Target{TargetGroupArn: tgArn, Id: id, Port: port}
		if err := h.putTarget(r.Context(), region, t); err != nil {
			protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
			return
		}
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlRegisterTargetsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) DeregisterTargets(w http.ResponseWriter, r *http.Request) {
	tgArn := r.FormValue("TargetGroupArn")
	if tgArn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("TargetGroupArn"))
		return
	}
	region := h.region(r.Context())

	for i := 1; ; i++ {
		id := r.FormValue(fmt.Sprintf("Targets.member.%d.Id", i))
		if id == "" {
			break
		}
		_ = h.removeTarget(r.Context(), region, tgArn, id)
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeregisterTargetsResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) DescribeTargetHealth(w http.ResponseWriter, r *http.Request) {
	tgArn := r.FormValue("TargetGroupArn")
	if tgArn == "" {
		protocol.WriteQueryXMLError(w, r, errMissingParam("TargetGroupArn"))
		return
	}
	region := h.region(r.Context())

	targets, err := h.listTargets(r.Context(), region, tgArn)
	if err != nil {
		protocol.WriteQueryXMLError(w, r, protocol.ErrInternalError)
		return
	}

	members := make([]xmlTargetHealthDescription, 0, len(targets))
	for _, t := range targets {
		var desc xmlTargetHealthDescription
		desc.Target.Id = t.Id
		desc.Target.Port = t.Port
		desc.TargetHealth.State = "healthy"
		members = append(members, desc)
	}
	resp := &xmlDescribeTargetHealthResponse{
		Xmlns:            elbv2XMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	}
	resp.Result.TargetHealthDescriptions.Member = members
	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}
