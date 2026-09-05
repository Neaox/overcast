package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.ec2.Ec2Client;
import software.amazon.awssdk.services.ec2.model.*;

import java.util.Map;

/**
 * EC2 compatibility test group.
 *
 * <p>Groups: ec2-instances, ec2-vpc.
 */
public final class Ec2Group implements ServiceGroup {

    private final AwsClients clients;

    public Ec2Group(AwsClients clients) {
        this.clients = clients;
    }

    private Ec2Client ec2() { return clients.ec2(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("ec2-instances:DescribeRegions",           this::describeRegions),
                Map.entry("ec2-instances:DescribeAvailabilityZones", this::describeAvailabilityZones),
                Map.entry("ec2-instances:DescribeInstanceTypes",     this::describeInstanceTypes),
                // ECR models a DescribeImages of its own, so the bare key is
                // ambiguous and the loader refuses it.
                Map.entry("ec2-instances:DescribeImages",                           this::describeImages),
                Map.entry("ec2-instances:RunInstances",                             this::runInstances),
                Map.entry("ec2-instances:DescribeInstances",                        this::describeInstances),
                Map.entry("ec2-instances:StopInstances",                            this::stopInstances),
                Map.entry("ec2-instances:StartInstances",                           this::startInstances),
                Map.entry("ec2-instances:TerminateInstances",                       this::terminateInstances),
                Map.entry("ec2-vpc:CreateVpc",                                      this::createVpc),
                Map.entry("ec2-vpc:DescribeVpcs",                                   this::describeVpcs),
                Map.entry("ec2-vpc:CreateVpnGateway",                               this::createVpnGateway),
                Map.entry("ec2-vpc:AttachVpnGateway",                               this::attachVpnGateway),
                Map.entry("ec2-vpc:DescribeVpnGateways",                            this::describeVpnGateways),
                Map.entry("ec2-vpc:CreateSubnet",                                   this::createSubnet),
                Map.entry("ec2-vpc:DescribeSubnets",                                this::describeSubnets),
                Map.entry("ec2-vpc:CreateSecurityGroup",                            this::createSecurityGroup),
                Map.entry("ec2-vpc:DeleteSecurityGroup",                            this::deleteSecurityGroup),
                Map.entry("ec2-vpc:CreateInternetGateway",                          this::createInternetGateway),
                Map.entry("ec2-vpc:AttachInternetGateway",                          this::attachInternetGateway),
                Map.entry("ec2-vpc:DetachVpnGateway",                               this::detachVpnGateway),
                Map.entry("ec2-vpc:DeleteVpnGateway",                               this::deleteVpnGateway),
                Map.entry("ec2-vpc:DeleteSubnet",                                   this::deleteSubnet),
                Map.entry("ec2-vpc:DeleteVpc",                                      this::deleteVpc),
                Map.entry("ec2-security-group-rules:AuthorizeSecurityGroupIngress", this::authorizeSecurityGroupIngress),
                Map.entry("ec2-security-group-rules:DescribeSecurityGroups",        this::describeSecurityGroups),
                Map.entry("ec2-security-group-rules:RevokeSecurityGroupIngress",    this::revokeSecurityGroupIngress),
                Map.entry("ec2-keypairs:CreateKeyPair",                             this::createKeyPair),
                Map.entry("ec2-keypairs:DescribeKeyPairs",                          this::describeKeyPairs),
                Map.entry("ec2-keypairs:DeleteKeyPair",                             this::deleteKeyPair)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("ec2-instances",            this::setupInstances),
                Map.entry("ec2-vpc",                  this::setupVpc),
                Map.entry("ec2-security-group-rules", this::setupSgRules),
                Map.entry("ec2-keypairs",             this::setupNoop)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("ec2-instances",            this::teardownInstances),
                Map.entry("ec2-vpc",                  this::teardownVpc),
                Map.entry("ec2-security-group-rules", this::teardownSgRules),
                Map.entry("ec2-keypairs",             this::teardownKeyPairs)
        );
    }

    // ── ec2-instances ─────────────────────────────────────────────────────────

    private void setupInstances(TestContext ctx) {
        ctx.set("ec2AmiId", "ami-12345678");
    }

    private void setupNoop(TestContext ctx) {}

    private void teardownInstances(TestContext ctx) {
        String instanceId = ctx.getString("ec2InstanceId");
        if (instanceId != null) {
            try { ec2().terminateInstances(r -> r.instanceIds(instanceId)); } catch (Exception ignored) {}
        }
    }

    private void describeImages(TestContext ctx) throws Exception {
        var resp = ec2().describeImages(r -> r.owners("amazon").maxResults(5));
        Assertions.assertNotEmpty(resp.images(), "DescribeImages: no images returned");
    }

    private void runInstances(TestContext ctx) throws Exception {
        String ami = ctx.getString("ec2AmiId");
        var resp = ec2().runInstances(r -> r
                .imageId(ami)
                .instanceType(InstanceType.T2_MICRO)
                .minCount(1)
                .maxCount(1));
        Assertions.assertNotEmpty(resp.instances(), "RunInstances: no instances returned");
        ctx.set("ec2InstanceId", resp.instances().get(0).instanceId());
    }

    private void describeInstances(TestContext ctx) throws Exception {
        String instanceId = ctx.getString("ec2InstanceId");
        if (instanceId == null) throw new AssertionError("DescribeInstances: prerequisite RunInstances is not implemented");
        var resp = ec2().describeInstances(r -> r.instanceIds(instanceId));
        Assertions.assertNotEmpty(resp.reservations(), "DescribeInstances: no reservations returned");
    }

    private void stopInstances(TestContext ctx) throws Exception {
        String instanceId = ctx.getString("ec2InstanceId");
        if (instanceId == null) throw new AssertionError("StopInstances: prerequisite RunInstances is not implemented");
        ec2().stopInstances(r -> r.instanceIds(instanceId));
    }

    private void terminateInstances(TestContext ctx) throws Exception {
        String instanceId = ctx.getString("ec2InstanceId");
        if (instanceId == null) throw new AssertionError("TerminateInstances: prerequisite RunInstances is not implemented");
        ec2().terminateInstances(r -> r.instanceIds(instanceId));
        ctx.set("ec2InstanceId", null);
    }

    // ── ec2-vpc ───────────────────────────────────────────────────────────────

    private void setupVpc(TestContext ctx) {
        ctx.set("ec2VpcReady", "true");
    }

    private void teardownVpc(TestContext ctx) {
        String sgId = ctx.getString("ec2SgId");
        if (sgId != null) {
            try { ec2().deleteSecurityGroup(r -> r.groupId(sgId)); } catch (Exception ignored) {}
        }
        String igwId = ctx.getString("ec2IgwId");
        String vpcId = ctx.getString("ec2VpcId");
        if (igwId != null && vpcId != null) {
            try { ec2().detachInternetGateway(r -> r.internetGatewayId(igwId).vpcId(vpcId)); } catch (Exception ignored) {}
            try { ec2().deleteInternetGateway(r -> r.internetGatewayId(igwId)); } catch (Exception ignored) {}
        }
        String vpnGatewayId = ctx.getString("ec2VpnGatewayId");
        if (vpnGatewayId != null && vpcId != null) {
            try { ec2().detachVpnGateway(r -> r.vpcId(vpcId).vpnGatewayId(vpnGatewayId)); } catch (Exception ignored) {}
            try { ec2().deleteVpnGateway(r -> r.vpnGatewayId(vpnGatewayId)); } catch (Exception ignored) {}
        }
        String subnetId = ctx.getString("ec2SubnetId");
        if (subnetId != null) {
            try { ec2().deleteSubnet(r -> r.subnetId(subnetId)); } catch (Exception ignored) {}
        }
        if (vpcId != null) {
            try { ec2().deleteVpc(r -> r.vpcId(vpcId)); } catch (Exception ignored) {}
        }
    }

    private void createVpc(TestContext ctx) throws Exception {
        var resp = ec2().createVpc(r -> r.cidrBlock("10.0.0.0/16"));
        Assertions.assertNotBlank(resp.vpc().vpcId(), "CreateVpc: vpcId is blank");
        ctx.set("ec2VpcId", resp.vpc().vpcId());
    }

    private void describeVpcs(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        var resp = ec2().describeVpcs(r -> r.vpcIds(vpcId));
        Assertions.assertNotEmpty(resp.vpcs(), "DescribeVpcs: no VPCs returned");
    }

    private void createSubnet(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        var resp = ec2().createSubnet(r -> r.vpcId(vpcId).cidrBlock("10.0.1.0/24"));
        Assertions.assertNotBlank(resp.subnet().subnetId(), "CreateSubnet: subnetId is blank");
        ctx.set("ec2SubnetId", resp.subnet().subnetId());
    }

    private void describeSubnets(TestContext ctx) throws Exception {
        String subnetId = ctx.getString("ec2SubnetId");
        var resp = ec2().describeSubnets(r -> r.subnetIds(subnetId));
        Assertions.assertNotEmpty(resp.subnets(), "DescribeSubnets: no subnets returned");
    }

    private void createInternetGateway(TestContext ctx) throws Exception {
        var resp = ec2().createInternetGateway(r -> {});
        Assertions.assertNotBlank(resp.internetGateway().internetGatewayId(),
                "CreateInternetGateway: igwId is blank");
        ctx.set("ec2IgwId", resp.internetGateway().internetGatewayId());
    }

    private void attachInternetGateway(TestContext ctx) throws Exception {
        String igwId = ctx.getString("ec2IgwId");
        String vpcId = ctx.getString("ec2VpcId");
        ec2().attachInternetGateway(r -> r.internetGatewayId(igwId).vpcId(vpcId));
    }

    private void createVpnGateway(TestContext ctx) throws Exception {
        var resp = ec2().createVpnGateway(r -> r
                .type("ipsec.1")
                .amazonSideAsn(65001L));
        Assertions.assertNotBlank(resp.vpnGateway().vpnGatewayId(),
                "CreateVpnGateway: vpnGatewayId is blank");
        ctx.set("ec2VpnGatewayId", resp.vpnGateway().vpnGatewayId());
    }

    private void attachVpnGateway(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        String vpnGatewayId = ctx.getString("ec2VpnGatewayId");
        if (vpcId == null) throw new AssertionError("AttachVpnGateway: no VPC from CreateVpc");
        if (vpnGatewayId == null) throw new AssertionError("AttachVpnGateway: no gateway from CreateVpnGateway");
        var resp = ec2().attachVpnGateway(r -> r.vpcId(vpcId).vpnGatewayId(vpnGatewayId));
        Assertions.assertEquals(resp.vpcAttachment().vpcId(), vpcId,
                "AttachVpnGateway: VpcAttachment VpcId mismatch");
    }

    private void describeVpnGateways(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        String vpnGatewayId = ctx.getString("ec2VpnGatewayId");
        if (vpcId == null) throw new AssertionError("DescribeVpnGateways: no VPC from CreateVpc");
        if (vpnGatewayId == null) throw new AssertionError("DescribeVpnGateways: no gateway from CreateVpnGateway");
        var resp = ec2().describeVpnGateways(r -> r
                .filters(Filter.builder().name("attachment.vpc-id").values(vpcId).build(),
                         Filter.builder().name("attachment.state").values("attached").build(),
                         Filter.builder().name("state").values("available").build()));
        Assertions.assertEquals(resp.vpnGateways().size(), 1,
                "DescribeVpnGateways: expected one VPN gateway");
    }

    private void detachVpnGateway(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        String vpnGatewayId = ctx.getString("ec2VpnGatewayId");
        if (vpcId == null || vpnGatewayId == null) return;
        ec2().detachVpnGateway(r -> r.vpcId(vpcId).vpnGatewayId(vpnGatewayId));
    }

    private void deleteVpnGateway(TestContext ctx) throws Exception {
        String vpnGatewayId = ctx.getString("ec2VpnGatewayId");
        if (vpnGatewayId == null) return;
        ec2().deleteVpnGateway(r -> r.vpnGatewayId(vpnGatewayId));
    }

    private void deleteVpc(TestContext ctx) throws Exception {
        // Detach and delete IGW first.
        String igwId = ctx.getString("ec2IgwId");
        String vpcId = ctx.getString("ec2VpcId");
        if (igwId != null) {
            try { ec2().detachInternetGateway(r -> r.internetGatewayId(igwId).vpcId(vpcId)); } catch (Exception ignored) {}
            try { ec2().deleteInternetGateway(r -> r.internetGatewayId(igwId)); } catch (Exception ignored) {}
        }
        String subnetId = ctx.getString("ec2SubnetId");
        if (subnetId != null)
            try { ec2().deleteSubnet(r -> r.subnetId(subnetId)); } catch (Exception ignored) {}

        ec2().deleteVpc(r -> r.vpcId(vpcId));
        ctx.set("ec2VpcId",    null);
        ctx.set("ec2SubnetId", null);
        ctx.set("ec2IgwId",    null);
    }

    // ── ec2-instances (additional) ────────────────────────────────────────────

    private void describeRegions(TestContext ctx) throws Exception {
        var resp = ec2().describeRegions(r -> {});
        Assertions.assertNotEmpty(resp.regions(), "DescribeRegions: no regions returned");
    }

    private void describeAvailabilityZones(TestContext ctx) throws Exception {
        var resp = ec2().describeAvailabilityZones(r -> {});
        Assertions.assertNotEmpty(resp.availabilityZones(), "DescribeAvailabilityZones: no zones returned");
    }

    private void describeInstanceTypes(TestContext ctx) throws Exception {
        // An unfiltered DescribeInstanceTypes is legitimately empty-able (the
        // emulator returns nothing without an explicit type list), so ask for
        // a concrete type and assert it comes back.
        var resp = ec2().describeInstanceTypes(r -> r.instanceTypes(InstanceType.T3_MICRO));
        boolean found = resp.instanceTypes().stream()
                .anyMatch(t -> t.instanceType() == InstanceType.T3_MICRO);
        Assertions.assertTrue(found, "DescribeInstanceTypes: t3.micro not returned");
    }

    private void startInstances(TestContext ctx) throws Exception {
        String instanceId = ctx.getString("ec2InstanceId");
        if (instanceId == null) throw new AssertionError("StartInstances: prerequisite RunInstances missing");
        var resp = ec2().startInstances(r -> r.instanceIds(instanceId));
        Assertions.assertNotEmpty(resp.startingInstances(), "StartInstances: no StartingInstances returned");
    }

    // ── ec2-vpc (additional) ─────────────────────────────────────────────────

    private void createSecurityGroup(TestContext ctx) throws Exception {
        String vpcId = ctx.getString("ec2VpcId");
        if (vpcId == null) throw new AssertionError("CreateSecurityGroup: no VPC");
        var resp = ec2().createSecurityGroup(r -> r
                .groupName("compat-" + ctx.runId())
                .description("compat test group")
                .vpcId(vpcId));
        Assertions.assertNotBlank(resp.groupId(), "CreateSecurityGroup: groupId is blank");
        ctx.set("ec2SgId", resp.groupId());
    }

    private void deleteSecurityGroup(TestContext ctx) throws Exception {
        String sgId = ctx.getString("ec2SgId");
        if (sgId == null) return;
        ec2().deleteSecurityGroup(r -> r.groupId(sgId));
    }

    private void deleteSubnet(TestContext ctx) throws Exception {
        String subnetId = ctx.getString("ec2SubnetId");
        if (subnetId == null) return;
        ec2().deleteSubnet(r -> r.subnetId(subnetId));
    }

    // ── ec2-security-group-rules ─────────────────────────────────────────────

    private void setupSgRules(TestContext ctx) {
        var vpcResp = ec2().createVpc(r -> r.cidrBlock("10.100.0.0/16"));
        ctx.set("ec2SgRulesVpcId", vpcResp.vpc().vpcId());
        var sgResp = ec2().createSecurityGroup(r -> r
                .groupName("compat-sgrules-" + ctx.runId())
                .description("compat SG rules test")
                .vpcId(vpcResp.vpc().vpcId()));
        ctx.set("ec2SgRulesSgId", sgResp.groupId());
    }

    private void teardownSgRules(TestContext ctx) {
        String sgId = ctx.getString("ec2SgRulesSgId");
        if (sgId != null) {
            try { ec2().deleteSecurityGroup(r -> r.groupId(sgId)); } catch (Exception ignored) {}
        }
        String vpcId = ctx.getString("ec2SgRulesVpcId");
        if (vpcId != null) {
            try { ec2().deleteVpc(r -> r.vpcId(vpcId)); } catch (Exception ignored) {}
        }
    }

    private void authorizeSecurityGroupIngress(TestContext ctx) throws Exception {
        String sgId = ctx.getString("ec2SgRulesSgId");
        if (sgId == null) throw new AssertionError("AuthorizeSecurityGroupIngress: no SG from setup");
        ec2().authorizeSecurityGroupIngress(r -> r
                .groupId(sgId)
                .ipPermissions(IpPermission.builder()
                        .ipProtocol("tcp")
                        .fromPort(443)
                        .toPort(443)
                        .ipRanges(IpRange.builder().cidrIp("10.0.0.0/8").build())
                        .build()));
    }

    private void describeSecurityGroups(TestContext ctx) throws Exception {
        String sgId = ctx.getString("ec2SgRulesSgId");
        if (sgId == null) throw new AssertionError("DescribeSecurityGroups: no SG from setup");
        var resp = ec2().describeSecurityGroups(r -> r.groupIds(sgId));
        Assertions.assertNotEmpty(resp.securityGroups(), "DescribeSecurityGroups: no security groups returned");
    }

    private void revokeSecurityGroupIngress(TestContext ctx) throws Exception {
        String sgId = ctx.getString("ec2SgRulesSgId");
        if (sgId == null) throw new AssertionError("RevokeSecurityGroupIngress: no SG from setup");
        ec2().revokeSecurityGroupIngress(r -> r
                .groupId(sgId)
                .ipPermissions(IpPermission.builder()
                        .ipProtocol("tcp")
                        .fromPort(443)
                        .toPort(443)
                        .ipRanges(IpRange.builder().cidrIp("10.0.0.0/8").build())
                        .build()));
    }

    // ── ec2-keypairs ─────────────────────────────────────────────────────────

    private void teardownKeyPairs(TestContext ctx) {
        String keyName = ctx.getString("ec2KeyName");
        if (keyName != null) {
            try { ec2().deleteKeyPair(r -> r.keyName(keyName)); } catch (Exception ignored) {}
        }
    }

    private void createKeyPair(TestContext ctx) throws Exception {
        String keyName = "compat-" + ctx.runId();
        var resp = ec2().createKeyPair(r -> r.keyName(keyName));
        Assertions.assertNotBlank(resp.keyPairId(), "CreateKeyPair: keyPairId is blank");
        ctx.set("ec2KeyName", keyName);
    }

    private void describeKeyPairs(TestContext ctx) throws Exception {
        String keyName = ctx.getString("ec2KeyName");
        if (keyName == null) throw new AssertionError("DescribeKeyPairs: no key from CreateKeyPair");
        var resp = ec2().describeKeyPairs(r -> r.keyNames(keyName));
        Assertions.assertNotEmpty(resp.keyPairs(), "DescribeKeyPairs: no key pairs returned");
    }

    private void deleteKeyPair(TestContext ctx) throws Exception {
        String keyName = ctx.getString("ec2KeyName");
        if (keyName == null) return;
        ec2().deleteKeyPair(r -> r.keyName(keyName));
    }
}
