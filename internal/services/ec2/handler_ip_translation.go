package ec2

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"

	"go.uber.org/zap"
)

func (h *Handler) allocatePrivateIPForSubnet(ctx context.Context, subnetID string) (apiIP, realIP, vpcID string) {
	if subnetID == "" {
		legacy := fmt.Sprintf("10.0.0.%d", syntheticIPCounter.Add(1)%254+1)
		return legacy, legacy, ""
	}
	sub, aerr := h.store.getSubnet(ctx, subnetID)
	if aerr != nil {
		legacy := fmt.Sprintf("10.0.0.%d", syntheticIPCounter.Add(1)%254+1)
		return legacy, legacy, ""
	}
	vpc, aerr := h.store.getVPC(ctx, sub.VpcID)
	if aerr != nil {
		legacy := fmt.Sprintf("10.0.0.%d", syntheticIPCounter.Add(1)%254+1)
		h.log.Warn("allocatePrivateIPForSubnet: failed to load VPC, using synthetic IP",
			zap.String("subnet", subnetID),
			zap.Error(aerr))
		return legacy, legacy, sub.VpcID
	}
	api, err := h.vpcStrategy.AllocatePrivateIP(ctx, vpc)
	if err != nil || api == "" {
		legacy := fmt.Sprintf("10.0.0.%d", syntheticIPCounter.Add(1)%254+1)
		h.log.Warn("allocatePrivateIPForSubnet: strategy allocation failed, using synthetic IP",
			zap.String("vpc", vpc.VpcID),
			zap.Error(err))
		return legacy, legacy, vpc.VpcID
	}
	real := api
	if vpc.DockerCidrBlock != "" {
		tr, terr := h.store.getVPCIPTranslationByFake(ctx, vpc.VpcID, api)
		if terr == nil && tr != nil && tr.RealIP != "" {
			real = tr.RealIP
		} else if host, ok := hostOffsetInCIDR(vpc.CidrBlock, api); ok {
			if resolved, ok := ipForCIDRHost(vpc.DockerCidrBlock, host); ok {
				real = resolved
			}
		}
	}
	return api, real, vpc.VpcID
}

func (h *Handler) privateIPForAPI(ctx context.Context, vpcID, maybeRealIP string) string {
	if vpcID == "" || maybeRealIP == "" {
		return maybeRealIP
	}
	vpc, aerr := h.store.getVPC(ctx, vpcID)
	if aerr != nil || vpc.DockerCidrBlock == "" {
		return maybeRealIP
	}
	tr, terr := h.store.getVPCIPTranslationByReal(ctx, vpcID, maybeRealIP)
	if terr == nil && tr != nil && tr.FakeIP != "" {
		return tr.FakeIP
	}
	// Fallback: derive a synthetic fake IP by host offset.
	host, ok := hostOffsetInCIDR(vpc.DockerCidrBlock, maybeRealIP)
	if !ok {
		return maybeRealIP
	}
	fake, ok := ipForCIDRHost(vpc.CidrBlock, host)
	if !ok {
		return maybeRealIP
	}
	return fake
}

func ipForCIDRSequence(cidr string, seq uint32) (string, bool) {
	ip, _, ok := ipForCIDRSequenceWithHost(cidr, seq)
	return ip, ok
}

func ipForCIDRSequenceWithHost(cidr string, seq uint32) (string, uint32, bool) {
	netIP, hostCount, ok := parseIPv4CIDR(cidr)
	if !ok || hostCount <= 3 {
		return "", 0, false
	}
	host := (seq % (hostCount - 3)) + 2
	return uint32ToIPv4(netIP + host).String(), host, true
}

func ipForCIDRHost(cidr string, host uint32) (string, bool) {
	netIP, hostCount, ok := parseIPv4CIDR(cidr)
	if !ok || hostCount <= 3 {
		return "", false
	}
	if host >= hostCount-1 {
		host = (host % (hostCount - 3)) + 2
	}
	if host < 2 {
		host = 2
	}
	return uint32ToIPv4(netIP + host).String(), true
}

func hostOffsetInCIDR(cidr, ipStr string) (uint32, bool) {
	netIP, hostCount, ok := parseIPv4CIDR(cidr)
	if !ok {
		return 0, false
	}
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return 0, false
	}
	ipInt := binary.BigEndian.Uint32(ip)
	if ipInt < netIP {
		return 0, false
	}
	host := ipInt - netIP
	if host >= hostCount {
		return 0, false
	}
	return host, true
}

func parseIPv4CIDR(cidr string) (network uint32, hostCount uint32, ok bool) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, 0, false
	}
	ipv4 := ip.To4()
	if ipv4 == nil {
		return 0, 0, false
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 || ones >= 31 {
		return 0, 0, false
	}
	network = binary.BigEndian.Uint32(ipv4) & binary.BigEndian.Uint32(ipnet.Mask)
	hostCount = uint32(1) << uint32(32-ones)
	return network, hostCount, true
}

func uint32ToIPv4(v uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, v)
	return ip
}
