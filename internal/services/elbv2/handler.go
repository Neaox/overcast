package elbv2

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/middleware"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/protocol/op"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
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
		"CreateLoadBalancer":             h.CreateLoadBalancer,
		"DescribeLoadBalancers":          h.DescribeLoadBalancers,
		"DeleteLoadBalancer":             h.DeleteLoadBalancer,
		"CreateTargetGroup":              h.CreateTargetGroup,
		"DescribeTargetGroups":           h.DescribeTargetGroups,
		"DeleteTargetGroup":              h.DeleteTargetGroup,
		"ModifyTargetGroup":              h.ModifyTargetGroup,
		"ModifyTargetGroupAttributes":    h.ModifyTargetGroupAttributes,
		"DescribeTargetGroupAttributes":  h.DescribeTargetGroupAttributes,
		"ModifyLoadBalancerAttributes":   h.ModifyLoadBalancerAttributes,
		"DescribeLoadBalancerAttributes": h.DescribeLoadBalancerAttributes,
		"CreateListener":                 h.CreateListener,
		"DescribeListeners":              h.DescribeListeners,
		"DeleteListener":                 h.DeleteListener,
		"RegisterTargets":                h.RegisterTargets,
		"DeregisterTargets":              h.DeregisterTargets,
		"DescribeTargetHealth":           h.DescribeTargetHealth,
		"AddTags":                        h.AddTags,
		"RemoveTags":                     h.RemoveTags,
		"DescribeTags":                   h.DescribeTags,
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

// invokeTypedAsQuery runs a typed operation on the raw Query dispatch path.
//
// Service.DispatchQuery prefers the typed operation whenever a codec is in
// context and otherwise falls back to h.dispatch, so every operation
// reachable both ways needs one implementation, not two — see the identical
// adapter in internal/services/rds/handler.go for the drift bug this
// pattern exists to prevent. Registering each h.ops entry as this adapter
// keeps ownsAction working — the router asks h.ops what ELBv2 claims —
// while leaving exactly one place where the behaviour lives:
// typed_logic.go.
func (h *Handler) invokeTypedAsQuery(action string, w http.ResponseWriter, r *http.Request) {
	typed, ok := h.typedOp[action]
	if !ok {
		protocol.NotImplementedQueryXML(w, r)
		return
	}
	typed.Invoke(w, r, codec.QueryXML)
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

// loadBalancerDNSName builds the client-facing name for a load balancer.
//
// The base is Overcast's external hostname, as every other service that hands
// out an endpoint uses (RDS, ElastiCache, MSK, OpenSearch, ECR). That is what
// puts the name inside the split-horizon zone, so setting OVERCAST_HOSTNAME to
// a resolvable base makes it resolvable rather than being a dead
// `.elb.localhost` label. It defaults to "localhost", which is the name this
// built before.
//
// Resolving is not reaching: nothing listens on this name yet, because the
// load balancer does not forward to its target groups.
func (h *Handler) loadBalancerDNSName(name, lbID, region string) string {
	host := "localhost"
	if h.cfg != nil {
		host = h.cfg.ExternalHostname()
	}
	return fmt.Sprintf("%s-%s.%s.elb.%s", name, lbID[:8], region, host)
}

// lbARNTypeSegment is the abbreviation AWS puts in a load balancer's ARN. The
// ARN never carries the full type name the API takes and returns: an
// application load balancer is "app", not "application", and code that parses
// an ARN — CDK's Arn.split, IAM resource patterns, the console's own links —
// is written against the abbreviation.
func lbARNTypeSegment(lbType string) string {
	switch lbType {
	case "network":
		return "net"
	case "gateway":
		return "gwy"
	default:
		return "app"
	}
}

// arnResourceIDLength is how many hex characters AWS puts at the end of a load
// balancer, target group or listener ARN.
const arnResourceIDLength = 16

// arnResourceID mints that id.
//
// AWS's form is sixteen lowercase hex characters —
// `.../targetgroup/web/73e2d6bc24d8a067`, `.../loadbalancer/app/web/50dc6c495c0c9188`
// — and this minted a truncated UUID instead, which for target groups and
// listeners came out twelve characters long with a "-" in the middle (#1718).
// ARNs are compared literally and parsed by segment all over IaC, so the shape
// is part of the wire contract even though the value is arbitrary.
//
// The digits come from a UUID rather than crypto/rand directly: uuid.NewString
// is already this package's source of randomness, and dropping its dashes
// leaves thirty-two hex characters to take the first sixteen of.
func arnResourceID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")[:arnResourceIDLength]
}

// loadBalancerARN builds a load balancer's ARN:
// arn:aws:elasticloadbalancing:{region}:{account}:loadbalancer/{app|net|gwy}/{name}/{id}
func loadBalancerARN(region, account, lbType, name, lbID string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s/%s/%s",
		region, account, lbARNTypeSegment(lbType), name, lbID)
}

// targetGroupARN builds a target group's ARN:
// arn:aws:elasticloadbalancing:{region}:{account}:targetgroup/{name}/{id}
func targetGroupARN(region, account, name, tgID string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/%s",
		region, account, name, tgID)
}

// listenerARN builds a listener's ARN from the load balancer it belongs to:
// arn:aws:elasticloadbalancing:{region}:{account}:listener/{app}/{name}/{lb-id}/{listener-id}
//
// The load balancer's own name and id are what make the ARN identify a listener
// on *that* load balancer. Minting it with a literal "listener" in their place
// produced ARNs that told a reader nothing and could not be traced back.
func listenerARN(lbArn, listenerID string) string {
	// A load balancer ARN ends "loadbalancer/{type}/{name}/{id}"; everything
	// after that marker is exactly the suffix a listener ARN repeats.
	const marker = ":loadbalancer/"
	i := strings.Index(lbArn, marker)
	if i < 0 {
		return lbArn + "/" + listenerID
	}
	return lbArn[:i] + ":listener/" + lbArn[i+len(marker):] + "/" + listenerID
}

// identifierFilter is the set of identifiers a Describe call named. ELBv2's
// Describe operations each accept more than one way to name a resource —
// LoadBalancerArns or Names, TargetGroupArns or Names — and ignoring them makes
// "describe the one I asked for" return the whole account, so a caller reading
// the first result gets an unrelated resource and every downstream ARN is
// wrong. An empty filter matches everything, as an unfiltered Describe does.
type identifierFilter map[string]bool

func newIdentifierFilter(lists ...[]string) identifierFilter {
	f := identifierFilter{}
	for _, list := range lists {
		for _, v := range list {
			if v != "" {
				f[v] = true
			}
		}
	}
	return f
}

// matches reports whether any of a resource's identifiers was asked for.
func (f identifierFilter) matches(identifiers ...string) bool {
	if len(f) == 0 {
		return true
	}
	for _, id := range identifiers {
		if f[id] {
			return true
		}
	}
	return false
}

// selectLBs narrows load balancers to those named by ARN or by name.
func selectLBs(lbs []*LoadBalancer, arns, names []string) []*LoadBalancer {
	f := newIdentifierFilter(arns, names)
	out := lbs[:0]
	for _, lb := range lbs {
		if f.matches(lb.LoadBalancerArn, lb.LoadBalancerName) {
			out = append(out, lb)
		}
	}
	return out
}

// selectTGs narrows target groups to those named by ARN or by name.
func selectTGs(tgs []*TargetGroup, arns, names []string) []*TargetGroup {
	f := newIdentifierFilter(arns, names)
	out := tgs[:0]
	for _, tg := range tgs {
		if f.matches(tg.TargetGroupArn, tg.TargetGroupName) {
			out = append(out, tg)
		}
	}
	return out
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

// forwardTargetGroups returns the target groups a listener forwards to, in
// action order.
func (l *Listener) forwardTargetGroups() []string {
	arns := make([]string, 0, len(l.DefaultActions))
	for _, a := range l.DefaultActions {
		if a.TargetGroupArn != "" && (a.Type == "" || a.Type == "forward") {
			arns = append(arns, a.TargetGroupArn)
		}
	}
	return arns
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
		IpAddressType:    lb.IpAddressType,
		VpcId:            lb.VpcId,
		CreatedTime:      lb.CreatedTime.UTC().Format(time.RFC3339),
	}
	x.State.Code = lb.State
	return x
}

func toTGXML(tg *TargetGroup) xmlTG {
	return xmlTG{
		TargetGroupArn:             tg.TargetGroupArn,
		TargetGroupName:            tg.TargetGroupName,
		Protocol:                   tg.Protocol,
		ProtocolVersion:            tg.ProtocolVersion,
		Port:                       tg.Port,
		VpcId:                      tg.VpcId,
		TargetType:                 tg.TargetType,
		IpAddressType:              tg.IpAddressType,
		HealthCheckEnabled:         tg.HealthCheckEnabled,
		HealthCheckProtocol:        tg.HealthCheckProtocol,
		HealthCheckPort:            tg.HealthCheckPort,
		HealthCheckPath:            tg.HealthCheckPath,
		HealthCheckIntervalSeconds: tg.HealthCheckIntervalSeconds,
		HealthCheckTimeoutSeconds:  tg.HealthCheckTimeoutSeconds,
		HealthyThresholdCount:      tg.HealthyThresholdCount,
		UnhealthyThresholdCount:    tg.UnhealthyThresholdCount,
		Matcher:                    tg.Matcher,
	}
}

func toListenerXML(l *Listener) xmlListener {
	out := xmlListener{
		ListenerArn:     l.ListenerArn,
		LoadBalancerArn: l.LoadBalancerArn,
		Protocol:        l.Protocol,
		Port:            l.Port,
	}
	if len(l.DefaultActions) > 0 {
		out.DefaultActions = &xmlActionMembers{Member: l.DefaultActions}
	}
	return out
}

func errMissingParam(param string) *protocol.AWSError {
	return errValidation(param + " is required")
}

// errValidation is ELBv2's ValidationError — the code it answers for a request
// it rejects before looking anything up: a missing required parameter, a value
// outside an enum or a range, an identifier that is not an ARN.
func errValidation(message string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationError",
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
	}
}

// The not-found errors this package answers with live on identifierScope
// (describe_identifiers.go), so a resource's code, message and status are
// written once and every operation that resolves an identifier — Describe,
// Delete, Modify and the tag calls alike — answers the same thing.

var _ = chi.NewRouter
var _ = uuid.NewString
var _ = zap.NewNop
var _ = xml.Unmarshal

// The raw Query entry points below used to be full second implementations
// of every operation, reachable whenever Service.DispatchQuery found no
// codec in context and fell back to h.dispatch. Two implementations of the
// same operation is two chances to be wrong; they are adapters now, so the
// behaviour lives once, in typed_logic.go. See invokeTypedAsQuery.

// CreateLoadBalancer creates a load balancer.
func (h *Handler) CreateLoadBalancer(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("CreateLoadBalancer", w, r)
}

// DescribeLoadBalancers describes load balancers.
func (h *Handler) DescribeLoadBalancers(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeLoadBalancers", w, r)
}

// DeleteLoadBalancer deletes a load balancer.
func (h *Handler) DeleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DeleteLoadBalancer", w, r)
}

// CreateTargetGroup creates a target group.
func (h *Handler) CreateTargetGroup(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("CreateTargetGroup", w, r)
}

// DescribeTargetGroups describes target groups.
func (h *Handler) DescribeTargetGroups(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeTargetGroups", w, r)
}

// DeleteTargetGroup deletes a target group.
func (h *Handler) DeleteTargetGroup(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DeleteTargetGroup", w, r)
}

// ModifyTargetGroup updates a target group's health-check configuration.
func (h *Handler) ModifyTargetGroup(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("ModifyTargetGroup", w, r)
}

// ModifyTargetGroupAttributes updates a target group's Attributes map.
func (h *Handler) ModifyTargetGroupAttributes(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("ModifyTargetGroupAttributes", w, r)
}

// DescribeTargetGroupAttributes describes a target group's Attributes map.
func (h *Handler) DescribeTargetGroupAttributes(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeTargetGroupAttributes", w, r)
}

// ModifyLoadBalancerAttributes updates a load balancer's Attributes map.
func (h *Handler) ModifyLoadBalancerAttributes(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("ModifyLoadBalancerAttributes", w, r)
}

// DescribeLoadBalancerAttributes describes a load balancer's Attributes map.
func (h *Handler) DescribeLoadBalancerAttributes(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeLoadBalancerAttributes", w, r)
}

// CreateListener creates a listener.
func (h *Handler) CreateListener(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("CreateListener", w, r)
}

// DescribeListeners describes listeners.
func (h *Handler) DescribeListeners(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeListeners", w, r)
}

// DeleteListener deletes a listener.
func (h *Handler) DeleteListener(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DeleteListener", w, r)
}

// RegisterTargets registers targets with a target group.
func (h *Handler) RegisterTargets(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("RegisterTargets", w, r)
}

// DeregisterTargets deregisters targets from a target group.
func (h *Handler) DeregisterTargets(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DeregisterTargets", w, r)
}

// DescribeTargetHealth describes the health of a target group's targets.
func (h *Handler) DescribeTargetHealth(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeTargetHealth", w, r)
}

var elbv2TagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "TooManyTags",
	InvalidCode:     "InvalidParameterException",
	ExceededMessage: "Tag count exceeded the maximum of 50 tags per resource.",
}

// AddTags adds tags to load balancers and target groups.
func (h *Handler) AddTags(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("AddTags", w, r)
}

// RemoveTags removes tags from load balancers and target groups.
func (h *Handler) RemoveTags(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("RemoveTags", w, r)
}

// DescribeTags describes tags on load balancers and target groups.
func (h *Handler) DescribeTags(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeTags", w, r)
}

func tagDescXML(arn string, tags map[string]string) xmlTagDescription {
	desc := xmlTagDescription{ResourceArn: arn}
	if len(tags) > 0 {
		desc.Tags.Member = serviceutil.TagElements(tags, func(k, v string) xmlTag {
			return xmlTag{Key: k, Value: v}
		})
	} else {
		desc.Tags.Member = []xmlTag{}
	}
	return desc
}
