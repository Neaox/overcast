package elbv2

import (
	"encoding/xml"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
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
	LoadBalancerArn  string    `json:"LoadBalancerArn"`
	LoadBalancerName string    `json:"LoadBalancerName"`
	DNSName          string    `json:"DNSName"`
	Type             string    `json:"Type"`
	Scheme           string    `json:"Scheme"`
	State            string    `json:"State"`
	VpcId            string    `json:"VpcId,omitempty"`
	CreatedTime      time.Time `json:"CreatedTime"`
	Region           string    `json:"Region"`
}

type TargetGroup struct {
	TargetGroupArn  string `json:"TargetGroupArn"`
	TargetGroupName string `json:"TargetGroupName"`
	Protocol        string `json:"Protocol"`
	Port            int    `json:"Port"`
	VpcId           string `json:"VpcId,omitempty"`
	TargetType      string `json:"TargetType"`
	HealthCheckPath string `json:"HealthCheckPath,omitempty"`
	Region          string `json:"Region"`
}

type Listener struct {
	ListenerArn     string `json:"ListenerArn"`
	LoadBalancerArn string `json:"LoadBalancerArn"`
	Protocol        string `json:"Protocol"`
	Port            int    `json:"Port"`
	Region          string `json:"Region"`
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

func (s *Service) Name() string                { return serviceName }
func (s *Service) RegisterRoutes(_ chi.Router) {}

func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionELBv2 }

func (s *Service) OwnsAction(action string) bool { return s.handler.ownsAction(action) }

func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
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
		c.WriteError(w, r, protocol.ErrNotImplemented)
		return
	}
	s.handler.dispatch(w, r)
}

type xmlLB struct {
	LoadBalancerArn  string `xml:"LoadBalancerArn"`
	LoadBalancerName string `xml:"LoadBalancerName"`
	DNSName          string `xml:"DNSName"`
	Type             string `xml:"Type"`
	Scheme           string `xml:"Scheme"`
	State            struct {
		Code string `xml:"Code"`
	} `xml:"State"`
	VpcId       string `xml:"VpcId,omitempty"`
	CreatedTime string `xml:"CreatedTime"`
}

type xmlTG struct {
	TargetGroupArn  string `xml:"TargetGroupArn"`
	TargetGroupName string `xml:"TargetGroupName"`
	Protocol        string `xml:"Protocol"`
	Port            int    `xml:"Port"`
	VpcId           string `xml:"VpcId,omitempty"`
	TargetType      string `xml:"TargetType"`
}

type xmlListener struct {
	ListenerArn     string `xml:"ListenerArn"`
	LoadBalancerArn string `xml:"LoadBalancerArn"`
	Protocol        string `xml:"Protocol"`
	Port            int    `xml:"Port"`
}

type xmlCreateLoadBalancerResponse struct {
	XMLName xml.Name `xml:"CreateLoadBalancerResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		LoadBalancers struct {
			Member []xmlLB `xml:"member"`
		} `xml:"LoadBalancers"`
	} `xml:"CreateLoadBalancerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeLoadBalancersResponse struct {
	XMLName xml.Name `xml:"DescribeLoadBalancersResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		LoadBalancers struct {
			Member []xmlLB `xml:"member"`
		} `xml:"LoadBalancers"`
	} `xml:"DescribeLoadBalancersResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeleteLoadBalancerResponse struct {
	XMLName          xml.Name                  `xml:"DeleteLoadBalancerResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteLoadBalancerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlCreateTargetGroupResponse struct {
	XMLName xml.Name `xml:"CreateTargetGroupResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		TargetGroups struct {
			Member []xmlTG `xml:"member"`
		} `xml:"TargetGroups"`
	} `xml:"CreateTargetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeTargetGroupsResponse struct {
	XMLName xml.Name `xml:"DescribeTargetGroupsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		TargetGroups struct {
			Member []xmlTG `xml:"member"`
		} `xml:"TargetGroups"`
	} `xml:"DescribeTargetGroupsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeleteTargetGroupResponse struct {
	XMLName          xml.Name                  `xml:"DeleteTargetGroupResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteTargetGroupResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlCreateListenerResponse struct {
	XMLName xml.Name `xml:"CreateListenerResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Listeners struct {
			Member []xmlListener `xml:"member"`
		} `xml:"Listeners"`
	} `xml:"CreateListenerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeListenersResponse struct {
	XMLName xml.Name `xml:"DescribeListenersResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Listeners struct {
			Member []xmlListener `xml:"member"`
		} `xml:"Listeners"`
	} `xml:"DescribeListenersResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeleteListenerResponse struct {
	XMLName          xml.Name                  `xml:"DeleteListenerResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeleteListenerResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlRegisterTargetsResponse struct {
	XMLName          xml.Name                  `xml:"RegisterTargetsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"RegisterTargetsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDeregisterTargetsResponse struct {
	XMLName          xml.Name                  `xml:"DeregisterTargetsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           struct{}                  `xml:"DeregisterTargetsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
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

type xmlDescribeTargetHealthResponse struct {
	XMLName xml.Name `xml:"DescribeTargetHealthResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		TargetHealthDescriptions struct {
			Member []xmlTargetHealthDescription `xml:"member"`
		} `xml:"TargetHealthDescriptions"`
	} `xml:"DescribeTargetHealthResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}
