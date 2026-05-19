package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.cognitoidentityprovider.CognitoIdentityProviderClient;
import software.amazon.awssdk.services.cognitoidentityprovider.model.*;

import java.util.Map;

/**
 * Cognito User Pools compatibility test group.
 *
 * <p>Groups: cognito-userpools.
 */
public final class CognitoGroup implements ServiceGroup {

    private final AwsClients clients;

    public CognitoGroup(AwsClients clients) {
        this.clients = clients;
    }

    private CognitoIdentityProviderClient cognito() { return clients.cognito(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateUserPool",       this::createUserPool),
                Map.entry("DescribeUserPool",     this::describeUserPool),
                Map.entry("ListUserPools",        this::listUserPools),
                Map.entry("CreateUserPoolClient", this::createUserPoolClient),
                Map.entry("ListUserPoolClients",  this::listUserPoolClients),
                Map.entry("AdminCreateUser",      this::adminCreateUser),
                Map.entry("cognito-userpools/ListUsers", this::listUsers),
                Map.entry("AdminDeleteUser",      this::adminDeleteUser),
                Map.entry("DeleteUserPool",       this::deleteUserPool),
                Map.entry("CreateUserPoolClient with token validity",      this::createClientTokenValidity),
                Map.entry("DescribeUserPoolClient returns token validity",  this::describeClientTokenValidity),
                Map.entry("UpdateUserPoolClient changes token validity",   this::updateClientTokenValidity),
                Map.entry("DeleteUserPoolClient", this::deleteUserPoolClient)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.of(
                "cognito-userpools",      this::setupUserPools,
                "cognito-token-validity", this::setupNoop
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.of(
                "cognito-userpools",      this::teardownUserPools,
                "cognito-token-validity", this::teardownTokenValidity
        );
    }

    // ── cognito-userpools ─────────────────────────────────────────────────────

    private void setupUserPools(TestContext ctx) {
        ctx.set("cognitoPoolName", "compat-" + ctx.runId());
    }

    private void setupNoop(TestContext ctx) {}

    private void teardownUserPools(TestContext ctx) {
        String poolId  = ctx.getString("cognitoPoolId");
        String username = ctx.getString("cognitoUsername");
        if (poolId != null && username != null)
            try { cognito().adminDeleteUser(r -> r.userPoolId(poolId).username(username)); } catch (Exception ignored) {}
        if (poolId != null)
            try { cognito().deleteUserPool(r -> r.userPoolId(poolId)); } catch (Exception ignored) {}
    }

    private void createUserPool(TestContext ctx) throws Exception {
        String name = ctx.getString("cognitoPoolName");
        var resp = cognito().createUserPool(r -> r.poolName(name));
        Assertions.assertNotBlank(resp.userPool().id(), "CreateUserPool: id is blank");
        ctx.set("cognitoPoolId", resp.userPool().id());
    }

    private void describeUserPool(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        var resp = cognito().describeUserPool(r -> r.userPoolId(poolId));
        Assertions.assertEquals(poolId, resp.userPool().id(), "DescribeUserPool: id mismatch");
    }

    private void listUserPools(TestContext ctx) throws Exception {
        var resp = cognito().listUserPools(r -> r.maxResults(60));
        Assertions.assertNotNull(resp.userPools(), "ListUserPools: userPools is null");
    }

    private void createUserPoolClient(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        var resp = cognito().createUserPoolClient(r -> r
                .userPoolId(poolId)
                .clientName("compat-client"));
        Assertions.assertNotBlank(resp.userPoolClient().clientId(),
                "CreateUserPoolClient: clientId is blank");
        ctx.set("cognitoClientId", resp.userPoolClient().clientId());
    }

    private void listUserPoolClients(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        var resp = cognito().listUserPoolClients(r -> r.userPoolId(poolId).maxResults(60));
        Assertions.assertNotNull(resp.userPoolClients(), "ListUserPoolClients: userPoolClients is null");
    }

    private void adminCreateUser(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        String username = "compat-user-" + ctx.runId();
        var resp = cognito().adminCreateUser(r -> r
                .userPoolId(poolId)
                .username(username)
                .temporaryPassword("Passw0rd!#"));
        Assertions.assertNotBlank(resp.user().username(), "AdminCreateUser: username is blank");
        ctx.set("cognitoUsername", username);
    }

    private void listUsers(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        var resp = cognito().listUsers(r -> r.userPoolId(poolId).limit(60));
        Assertions.assertNotNull(resp.users(), "ListUsers: users is null");
    }

    private void adminDeleteUser(TestContext ctx) throws Exception {
        String poolId   = ctx.getString("cognitoPoolId");
        String username = ctx.getString("cognitoUsername");
        if (username == null) throw new AssertionError("AdminDeleteUser: prerequisite AdminCreateUser is not implemented");
        cognito().adminDeleteUser(r -> r.userPoolId(poolId).username(username));
        ctx.set("cognitoUsername", null);
    }

    private void deleteUserPool(TestContext ctx) throws Exception {
        String poolId = ctx.getString("cognitoPoolId");
        cognito().deleteUserPool(r -> r.userPoolId(poolId));
        ctx.set("cognitoPoolId", null);
    }

    // ── cognito-token-validity ────────────────────────────────────────────────

    private void teardownTokenValidity(TestContext ctx) {
        String poolId = ctx.getString("tvPoolId");
        String clientId = ctx.getString("tvClientId");
        if (poolId != null && clientId != null)
            try { cognito().deleteUserPoolClient(r -> r.userPoolId(poolId).clientId(clientId)); } catch (Exception ignored) {}
        if (poolId != null)
            try { cognito().deleteUserPool(r -> r.userPoolId(poolId)); } catch (Exception ignored) {}
    }

    private void createClientTokenValidity(TestContext ctx) throws Exception {
        String poolName = "compat-tv-" + ctx.runId();
        var poolResp = cognito().createUserPool(r -> r.poolName(poolName));
        String poolId = poolResp.userPool().id();
        Assertions.assertNotBlank(poolId, "CreateClientTokenValidity: missing pool Id");
        ctx.set("tvPoolId", poolId);

        var resp = cognito().createUserPoolClient(r -> r
                .userPoolId(poolId)
                .clientName("compat-client-" + ctx.runId())
                .accessTokenValidity(2)
                .idTokenValidity(3)
                .refreshTokenValidity(7)
                .tokenValidityUnits(u -> u
                        .accessToken("hours")
                        .idToken("hours")
                        .refreshToken("days")));
        Assertions.assertNotBlank(resp.userPoolClient().clientId(),
                "CreateClientTokenValidity: missing ClientId");
        ctx.set("tvClientId", resp.userPoolClient().clientId());
    }

    private void describeClientTokenValidity(TestContext ctx) throws Exception {
        String poolId = ctx.getString("tvPoolId");
        String clientId = ctx.getString("tvClientId");
        Assertions.assertNotBlank(poolId, "DescribeClientTokenValidity: missing poolId");
        Assertions.assertNotBlank(clientId, "DescribeClientTokenValidity: missing clientId");
        cognito().describeUserPoolClient(r -> r.userPoolId(poolId).clientId(clientId));
    }

    private void updateClientTokenValidity(TestContext ctx) throws Exception {
        String poolId = ctx.getString("tvPoolId");
        String clientId = ctx.getString("tvClientId");
        Assertions.assertNotBlank(poolId, "UpdateClientTokenValidity: missing poolId");
        Assertions.assertNotBlank(clientId, "UpdateClientTokenValidity: missing clientId");
        cognito().updateUserPoolClient(r -> r
                .userPoolId(poolId)
                .clientId(clientId)
                .accessTokenValidity(30)
                .tokenValidityUnits(u -> u
                        .accessToken("minutes")
                        .idToken("hours")
                        .refreshToken("days")));
    }

    private void deleteUserPoolClient(TestContext ctx) throws Exception {
        String poolId = ctx.getString("tvPoolId");
        String clientId = ctx.getString("tvClientId");
        if (poolId == null || clientId == null) return;
        cognito().deleteUserPoolClient(r -> r.userPoolId(poolId).clientId(clientId));
    }
}
