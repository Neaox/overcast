// Package ec2 provides emulation of Amazon EC2 and VPC operations.
//
// Implemented: DescribeRegions, DescribeAvailabilityZones, DescribeInstances,
// DescribeInstanceTypes, RunInstances, TerminateInstances, StartInstances,
// StopInstances, CreateVpc, DescribeVpcs, DeleteVpc,
// CreateSubnet, DeleteSubnet, DescribeSubnets,
// CreateSecurityGroup, DeleteSecurityGroup, DescribeSecurityGroups,
// AuthorizeSecurityGroupIngress, AuthorizeSecurityGroupEgress,
// RevokeSecurityGroupIngress, RevokeSecurityGroupEgress,
// DescribeImages, CreateKeyPair, DescribeKeyPairs, DeleteKeyPair,
// CreateRouteTable, DescribeRouteTables, DeleteRouteTable, CreateRoute,
// AssociateRouteTable, DisassociateRouteTable,
// CreateInternetGateway, DescribeInternetGateways, DeleteInternetGateway,
// AttachInternetGateway, DetachInternetGateway,
// CreateVpcPeeringConnection, AcceptVpcPeeringConnection,
// DescribeVpcPeeringConnections, DeleteVpcPeeringConnection.
// DescribeVpnGateways returns an empty result for local CDK VPC lookups.
// All other operations return HTTP 501 Not Implemented.
package ec2

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/docker"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "ec2"

// Service implements router.Service and router.QueryDispatcher for EC2 and VPC.
// Uses the AWS Query protocol (form-encoded POST, XML responses) and
// identifies itself by the API version "2016-11-15".
type Service struct {
	handler *Handler
	log     *serviceutil.ServiceLogger
}

type ec2ErrorCodec struct {
	codec.Codec
}

func (c ec2ErrorCodec) WriteError(w http.ResponseWriter, r *http.Request, aerr *protocol.AWSError) {
	protocol.WriteEC2QueryXMLError(w, r, aerr)
}

// New returns a configured EC2 Service.
func New(cfg *config.Config, store state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		handler: newHandler(cfg, store, log, clk),
		log:     log,
	}
}

// InitBus wires the event bus for VPC/subnet/security group lifecycle events.
func (s *Service) InitBus(bus *events.Bus) {
	s.handler.bus = bus
}

// SetDocker wires a Docker client for VPC network management. EC2 manages
// Docker networks (one per VPC) rather than containers.
func (s *Service) SetDocker(dc *docker.Client) {
	s.handler.docker = dc
	s.handler.dockerReady.Store(true)
}

// ReconcileNetworks satisfies router.NetworkReconciler. Called once after
// Docker becomes available to sync stored VPC state against actual Docker
// networks (recreate missing networks, update stale Docker network IDs).
func (s *Service) ReconcileNetworks(ctx context.Context, networks []docker.NetworkSummary) {
	s.handler.reconcileNetworks(ctx, networks)
}

// VpcIDForSubnet returns the VPC ID that owns the given subnet.
// Returns empty string if the subnet is not found.
func (s *Service) VpcIDForSubnet(ctx context.Context, subnetID string) string {
	sub, aerr := s.handler.store.getSubnet(ctx, subnetID)
	if aerr != nil {
		return ""
	}
	return sub.VpcID
}

// DockerNetworkForVpc returns the Docker network ID for the given VPC.
// Returns empty string if the VPC is not found or has no Docker network.
func (s *Service) DockerNetworkForVpc(ctx context.Context, vpcID string) string {
	vpc, aerr := s.handler.store.getVPC(ctx, vpcID)
	if aerr != nil {
		return ""
	}
	return vpc.DockerNetworkID
}

// VPCNetworkStatus returns the current network status for the given VPC.
// Empty string means the VPC was not found.
func (s *Service) VPCNetworkStatus(ctx context.Context, vpcID string) string {
	vpc, aerr := s.handler.store.getVPC(ctx, vpcID)
	if aerr != nil {
		return ""
	}
	if vpc.NetworkStatus == "" {
		return vpcNetworkStatusOK
	}
	return vpc.NetworkStatus
}

// AllocatePrivateIPForSubnet returns the API-visible private IP for the given
// subnet, applying remapped translation when needed. Empty string means the
// subnet could not be resolved.
func (s *Service) AllocatePrivateIPForSubnet(ctx context.Context, subnetID string) string {
	apiIP, _, _ := s.handler.allocatePrivateIPForSubnet(ctx, subnetID)
	return apiIP
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. EC2 has no path-routed endpoints.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// OwnsVersion satisfies router.QueryVersionOwner.
func (s *Service) OwnsVersion(version string) bool { return version == awsapi.VersionEC2 }

// Stop cancels pending lifecycle transitions (e.g. instance state changes).
func (s *Service) Stop(ctx context.Context) {
	s.handler.scheduler.Stop(ctx)
}

// DispatchQuery satisfies router.QueryDispatcher; routes to the correct handler.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
		if !codec.Supports(s.SupportedProtocols(), c) {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "EC2 does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, ec2ErrorCodec{Codec: c})
			return
		}
		protocol.WriteEC2QueryXMLError(w, r, protocol.ErrNotImplemented)
		return
	}

	if err := r.ParseForm(); err != nil {
		protocol.WriteEC2QueryXMLError(w, r, protocol.ErrInvalidArgument("invalid request form encoding"))
		return
	}
	action := r.FormValue("Action")
	if handler, ok := s.handler.ops[action]; ok {
		handler(w, r)
		return
	}
	protocol.NotImplementedEC2QueryXML(w, r)
}
