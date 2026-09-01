package elbv2

import (
	"context"
	"encoding/xml"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/awsapi"
	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

const serviceName = "elbv2"

const elbv2XMLNS = "https://elasticloadbalancing.amazonaws.com/doc/2015-12-01/"

const (
	nsLBs       = "elbv2:loadbalancers"
	nsTGs       = "elbv2:targetgroups"
	nsListeners = "elbv2:listeners"
	nsTargets   = "elbv2:targets"
)

type LoadBalancer struct {
	LoadBalancerArn  string            `json:"LoadBalancerArn"`
	LoadBalancerName string            `json:"LoadBalancerName"`
	DNSName          string            `json:"DNSName"`
	Type             string            `json:"Type"`
	Scheme           string            `json:"Scheme"`
	IpAddressType    string            `json:"IpAddressType,omitempty"`
	State            string            `json:"State"`
	VpcId            string            `json:"VpcId,omitempty"`
	CreatedTime      time.Time         `json:"CreatedTime"`
	Region           string            `json:"Region"`
	Tags             map[string]string `json:"Tags,omitempty"`
}

type TargetGroup struct {
	TargetGroupArn  string `json:"TargetGroupArn"`
	TargetGroupName string `json:"TargetGroupName"`
	Protocol        string `json:"Protocol"`
	ProtocolVersion string `json:"ProtocolVersion,omitempty"`
	Port            int    `json:"Port"`
	VpcId           string `json:"VpcId,omitempty"`
	TargetType      string `json:"TargetType"`
	IpAddressType   string `json:"IpAddressType,omitempty"`

	// Health check block. CDK/CloudFormation templates set this via the
	// HealthCheck* properties on AWS::ElasticLoadBalancingV2::TargetGroup;
	// it round-trips through DescribeTargetGroups but is not evaluated
	// against registered targets — DescribeTargetHealth always answers
	// "healthy" (handler.go). Storage is faithful; enforcement is not
	// implemented.
	HealthCheckEnabled         bool     `json:"HealthCheckEnabled"`
	HealthCheckProtocol        string   `json:"HealthCheckProtocol,omitempty"`
	HealthCheckPort            string   `json:"HealthCheckPort,omitempty"`
	HealthCheckPath            string   `json:"HealthCheckPath,omitempty"`
	HealthCheckIntervalSeconds int      `json:"HealthCheckIntervalSeconds,omitempty"`
	HealthCheckTimeoutSeconds  int      `json:"HealthCheckTimeoutSeconds,omitempty"`
	HealthyThresholdCount      int      `json:"HealthyThresholdCount,omitempty"`
	UnhealthyThresholdCount    int      `json:"UnhealthyThresholdCount,omitempty"`
	Matcher                    *Matcher `json:"Matcher,omitempty"`

	// Attributes holds ModifyTargetGroupAttributes settings (e.g.
	// deregistration_delay.timeout_seconds, stickiness.enabled) keyed by
	// their AWS attribute name. Stored and echoed by
	// DescribeTargetGroupAttributes; not enforced by the data plane.
	Attributes map[string]string `json:"Attributes,omitempty"`

	Region string            `json:"Region"`
	Tags   map[string]string `json:"Tags,omitempty"`
}

// Matcher is the target group health-check success matcher
// (HealthCheck's Matcher property).
type Matcher struct {
	HttpCode string `json:"HttpCode,omitempty" xml:"HttpCode,omitempty"`
	GrpcCode string `json:"GrpcCode,omitempty" xml:"GrpcCode,omitempty"`
}

type Listener struct {
	ListenerArn     string `json:"ListenerArn"`
	LoadBalancerArn string `json:"LoadBalancerArn"`
	Protocol        string `json:"Protocol"`
	Port            int    `json:"Port"`
	Region          string `json:"Region"`
	// DefaultActions is what the listener does with a request no rule matched.
	// It carries the listener's link to its target group, so without it a load
	// balancer cannot forward anywhere.
	DefaultActions []Action `json:"DefaultActions,omitempty"`
}

// Action is a listener action. Only "forward" (via TargetGroupArn) has a
// data-plane effect — pickTarget (handler_proxy.go) only knows how to reach
// a plain forward. RedirectConfig and FixedResponseConfig round-trip through
// DescribeListeners so a CDK-authored redirect or fixed-response listener
// keeps its shape, but Overcast does not act on either: a request against
// such a listener still resolves through forwardTargetGroups rather than
// actually redirecting or returning the fixed body. ForwardConfig's weighted
// multi-target-group form, Certificates/SslPolicy/AlpnPolicy/
// MutualAuthentication, and the Cognito/OIDC auth actions are not modelled
// at all — a template using them keeps whatever it set at the CFN layer
// only if the emulator's caller reads it back from the template, not from
// DescribeListeners.
//
// json tags double as both the storage encoding (putListener) and the
// Query-form decode key (query.go's decodeStruct reads the json tag); xml
// tags on the nested config types double as the DescribeListeners/
// CreateListener response encoding, so one type serves all three without a
// second "xmlAction" mirror.
type Action struct {
	Type                string               `json:"Type" xml:"Type"`
	TargetGroupArn      string               `json:"TargetGroupArn,omitempty" xml:"TargetGroupArn,omitempty"`
	Order               int                  `json:"Order,omitempty" xml:"Order,omitempty"`
	RedirectConfig      *RedirectConfig      `json:"RedirectConfig,omitempty" xml:"RedirectConfig,omitempty"`
	FixedResponseConfig *FixedResponseConfig `json:"FixedResponseConfig,omitempty" xml:"FixedResponseConfig,omitempty"`
}

// RedirectConfig is a listener action's RedirectConfig member — the shape a
// CDK HTTP→HTTPS redirect listener sends.
type RedirectConfig struct {
	Protocol   string `json:"Protocol,omitempty" xml:"Protocol,omitempty"`
	Port       string `json:"Port,omitempty" xml:"Port,omitempty"`
	Host       string `json:"Host,omitempty" xml:"Host,omitempty"`
	Path       string `json:"Path,omitempty" xml:"Path,omitempty"`
	Query      string `json:"Query,omitempty" xml:"Query,omitempty"`
	StatusCode string `json:"StatusCode,omitempty" xml:"StatusCode,omitempty"`
}

// FixedResponseConfig is a listener action's FixedResponseConfig member.
type FixedResponseConfig struct {
	MessageBody string `json:"MessageBody,omitempty" xml:"MessageBody,omitempty"`
	StatusCode  string `json:"StatusCode,omitempty" xml:"StatusCode,omitempty"`
	ContentType string `json:"ContentType,omitempty" xml:"ContentType,omitempty"`
}

type Target struct {
	TargetGroupArn string `json:"TargetGroupArn"`
	Id             string `json:"Id"`
	Port           int    `json:"Port,omitempty"`
}

type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
}

func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		handler: newHandler(cfg, store, log, clk),
		log:     log,
	}
}

func (s *Service) Name() string { return serviceName }

// RegisterRoutes mounts the load balancer data plane. Requests arriving on a
// load balancer's DNS name are rewritten onto /_overcast/elb by HostRouteRewrite and
// forwarded to a registered target from here.
func (s *Service) RegisterRoutes(r chi.Router) {
	r.HandleFunc("/_overcast/elb", s.handler.ProxyRequest)
	r.HandleFunc("/_overcast/elb/*", s.handler.ProxyRequest)
}

// RegisterTarget adds an address to a target group, and DeregisterTarget
// removes it. ECS calls these as it places and stops the tasks of a service
// with a loadBalancers configuration, which is what puts a Fargate service
// behind its load balancer.
func (s *Service) RegisterTarget(ctx context.Context, targetGroupArn, address string, port int) error {
	return s.handler.putTarget(ctx, s.handler.region(ctx), &Target{
		TargetGroupArn: targetGroupArn,
		Id:             address,
		Port:           port,
	})
}

func (s *Service) DeregisterTarget(ctx context.Context, targetGroupArn, address string) error {
	return s.handler.removeTarget(ctx, s.handler.region(ctx), targetGroupArn, address)
}

func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionELBv2 }

func (s *Service) OwnsAction(action string) bool { return s.handler.ownsAction(action) }

func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !serviceutil.AllowProtocolDrift(s.handler.cfg, s.log, opName, c, s.SupportedProtocols()) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "ELBv2 does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
		// No typed impl for this op — fall through to legacy dispatch below.
	}
	s.handler.dispatch(w, r)
}

type xmlLB struct {
	LoadBalancerArn  string `xml:"LoadBalancerArn"`
	LoadBalancerName string `xml:"LoadBalancerName"`
	DNSName          string `xml:"DNSName"`
	Type             string `xml:"Type"`
	Scheme           string `xml:"Scheme"`
	IpAddressType    string `xml:"IpAddressType,omitempty"`
	State            struct {
		Code string `xml:"Code"`
	} `xml:"State"`
	VpcId       string `xml:"VpcId,omitempty"`
	CreatedTime string `xml:"CreatedTime"`
}

type xmlTG struct {
	TargetGroupArn             string   `xml:"TargetGroupArn"`
	TargetGroupName            string   `xml:"TargetGroupName"`
	Protocol                   string   `xml:"Protocol,omitempty"`
	ProtocolVersion            string   `xml:"ProtocolVersion,omitempty"`
	Port                       int      `xml:"Port,omitempty"`
	VpcId                      string   `xml:"VpcId,omitempty"`
	TargetType                 string   `xml:"TargetType"`
	IpAddressType              string   `xml:"IpAddressType,omitempty"`
	HealthCheckEnabled         bool     `xml:"HealthCheckEnabled"`
	HealthCheckProtocol        string   `xml:"HealthCheckProtocol,omitempty"`
	HealthCheckPort            string   `xml:"HealthCheckPort,omitempty"`
	HealthCheckPath            string   `xml:"HealthCheckPath,omitempty"`
	HealthCheckIntervalSeconds int      `xml:"HealthCheckIntervalSeconds,omitempty"`
	HealthCheckTimeoutSeconds  int      `xml:"HealthCheckTimeoutSeconds,omitempty"`
	HealthyThresholdCount      int      `xml:"HealthyThresholdCount,omitempty"`
	UnhealthyThresholdCount    int      `xml:"UnhealthyThresholdCount,omitempty"`
	Matcher                    *Matcher `xml:"Matcher,omitempty"`
}

type xmlListener struct {
	ListenerArn     string            `xml:"ListenerArn"`
	LoadBalancerArn string            `xml:"LoadBalancerArn"`
	Protocol        string            `xml:"Protocol"`
	Port            int               `xml:"Port"`
	DefaultActions  *xmlActionMembers `xml:"DefaultActions,omitempty"`
}

type xmlActionMembers struct {
	Member []Action `xml:"member"`
}

// xmlAttribute is one Key/Value entry in a target group Attributes list
// (ModifyTargetGroupAttributes/DescribeTargetGroupAttributes).
type xmlAttribute struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value,omitempty"`
}

type xmlTargetHealthDescription struct {
	Target struct {
		Id   string `xml:"Id"`
		Port int    `xml:"Port,omitempty"`
	} `xml:"Target"`
	TargetHealth struct {
		State string `xml:"State"`
	} `xml:"TargetHealth"`
}

// Tag XML wire types.

type xmlTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value,omitempty"`
}

type xmlTagDescription struct {
	ResourceArn string  `xml:"ResourceArn"`
	Tags        xmlTags `xml:"Tags"`
}

type xmlTags struct {
	Member []xmlTag `xml:"member"`
}

// ── ModifyTargetGroup / TargetGroupAttributes wire types ───────────────────

type xmlModifyTargetGroupResponse struct {
	XMLName xml.Name `xml:"ModifyTargetGroupResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		TargetGroups struct {
			Member []xmlTG `xml:"member"`
		} `xml:"TargetGroups"`
	} `xml:"ModifyTargetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlModifyTargetGroupAttributesResponse struct {
	XMLName xml.Name `xml:"ModifyTargetGroupAttributesResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Attributes struct {
			Member []xmlAttribute `xml:"member"`
		} `xml:"Attributes"`
	} `xml:"ModifyTargetGroupAttributesResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeTargetGroupAttributesResponse struct {
	XMLName xml.Name `xml:"DescribeTargetGroupAttributesResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Attributes struct {
			Member []xmlAttribute `xml:"member"`
		} `xml:"Attributes"`
	} `xml:"DescribeTargetGroupAttributesResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
