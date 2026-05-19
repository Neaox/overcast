package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.sts.StsClient;

import java.util.Map;

/**
 * STS compatibility test group.
 *
 * <p>Groups: sts-identity, sts-assume.
 */
public final class StsGroup implements ServiceGroup {

    private final AwsClients clients;

    public StsGroup(AwsClients clients) {
        this.clients = clients;
    }

    private StsClient sts() { return clients.sts(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("GetCallerIdentity",        this::getCallerIdentity),
                Map.entry("GetSessionToken",          this::getSessionToken),
                Map.entry("GetFederationToken",       this::getFederationToken),
                Map.entry("AssumeRole",               this::assumeRole),
                Map.entry("AssumeRoleWithWebIdentity",this::assumeRoleWithWebIdentity)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of();
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of();
    }

    // ── sts-identity ──────────────────────────────────────────────────────────

    private void getCallerIdentity(TestContext ctx) throws Exception {
        var resp = sts().getCallerIdentity();
        Assertions.assertNotBlank(resp.account(), "GetCallerIdentity: account");
        Assertions.assertNotBlank(resp.arn(), "GetCallerIdentity: arn");
        Assertions.assertNotBlank(resp.userId(), "GetCallerIdentity: userId");
    }

    private void getSessionToken(TestContext ctx) throws Exception {
        var resp = sts().getSessionToken();
        Assertions.assertNotNull(resp.credentials(), "GetSessionToken: credentials");
        Assertions.assertNotBlank(resp.credentials().accessKeyId(), "GetSessionToken: accessKeyId");
    }

    private void getFederationToken(TestContext ctx) throws Exception {
        var resp = sts().getFederationToken(r -> r.name("compat-user"));
        Assertions.assertNotNull(resp.credentials(), "GetFederationToken: credentials");
        Assertions.assertNotBlank(resp.credentials().accessKeyId(), "GetFederationToken: accessKeyId");
    }

    // ── sts-assume ────────────────────────────────────────────────────────────

    private void assumeRole(TestContext ctx) throws Exception {
        var resp = sts().assumeRole(r -> r
                .roleArn("arn:aws:iam::000000000000:role/compat-role")
                .roleSessionName("java-sdk-compat"));
        Assertions.assertNotNull(resp.credentials(), "AssumeRole: credentials");
        Assertions.assertNotBlank(resp.credentials().accessKeyId(), "AssumeRole: accessKeyId");
    }

    private void assumeRoleWithWebIdentity(TestContext ctx) throws Exception {
        var resp = sts().assumeRoleWithWebIdentity(r -> r
                .roleArn("arn:aws:iam::000000000000:role/compat-role")
                .roleSessionName("java-sdk-compat")
                .webIdentityToken("fake-web-identity-token"));
        Assertions.assertNotNull(resp.credentials(), "AssumeRoleWithWebIdentity: credentials");
    }
}
