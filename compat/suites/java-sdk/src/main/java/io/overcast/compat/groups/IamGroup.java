package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.iam.IamClient;
import software.amazon.awssdk.services.iam.model.*;
import software.amazon.awssdk.services.iam.model.PolicyScopeType;

import java.util.Map;

/**
 * IAM compatibility test group.
 *
 * <p>Groups: iam-users, iam-roles, iam-policies, iam-groups.
 */
public final class IamGroup implements ServiceGroup {

    private static final String BASIC_ASSUME_ROLE_POLICY = """
            {
              "Version": "2012-10-17",
              "Statement": [{
                "Effect": "Allow",
                "Principal": {"Service": "lambda.amazonaws.com"},
                "Action": "sts:AssumeRole"
              }]
            }
            """;

    private static final String INLINE_POLICY = """
            {
              "Version": "2012-10-17",
              "Statement": [{
                "Effect": "Allow",
                "Action": "s3:ListAllMyBuckets",
                "Resource": "*"
              }]
            }
            """;

    private final AwsClients clients;

    public IamGroup(AwsClients clients) {
        this.clients = clients;
    }

    private IamClient iam() { return clients.iam(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("CreateUser",             this::createUser),
                Map.entry("GetUser",                this::getUser),
                Map.entry("iam-users/ListUsers",    this::listUsers),
                Map.entry("UpdateUser",             this::updateUser),
                Map.entry("CreateAccessKey",        this::createAccessKey),
                Map.entry("ListAccessKeys",         this::listAccessKeys),
                Map.entry("DeleteAccessKey",        this::deleteAccessKey),
                Map.entry("PutUserPolicy",          this::putUserPolicy),
                Map.entry("GetUserPolicy",          this::getUserPolicy),
                Map.entry("DeleteUserPolicy",       this::deleteUserPolicy),
                Map.entry("DeleteUser",             this::deleteUser),
                Map.entry("CreateRole",             this::createIamRole),
                Map.entry("GetRole",                this::getIamRole),
                Map.entry("ListRoles",              this::listIamRoles),
                Map.entry("PutRolePolicy",          this::putRolePolicy),
                Map.entry("GetRolePolicy",          this::getRolePolicy),
                Map.entry("ListRolePolicies",       this::listRolePolicies),
                Map.entry("DeleteRolePolicy",       this::deleteRolePolicy),
                Map.entry("AttachRolePolicy",       this::attachRolePolicy),
                Map.entry("ListAttachedRolePolicies",this::listAttachedRolePolicies),
                Map.entry("DetachRolePolicy",       this::detachRolePolicy),
                Map.entry("DeleteRole",             this::deleteIamRole),
                Map.entry("CreateInstanceProfile",  this::createInstanceProfile),
                Map.entry("AddRoleToInstanceProfile",this::addRoleToInstanceProfile),
                Map.entry("GetInstanceProfile",     this::getInstanceProfile),
                Map.entry("CreatePolicy",           this::createIamPolicy),
                Map.entry("GetPolicy",              this::getIamPolicy),
                Map.entry("ListPolicies",           this::listIamPolicies),
                Map.entry("DeletePolicy",           this::deleteIamPolicy),
                Map.entry("CreateGroup",            this::createGroup),
                Map.entry("GetGroup",               this::getGroup),
                Map.entry("AddUserToGroup",         this::addUserToGroup),
                Map.entry("ListGroupsForUser",      this::listGroupsForUser),
                Map.entry("RemoveUserFromGroup",    this::removeUserFromGroup),
                Map.entry("DeleteGroup",            this::deleteGroup)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("iam-users",    this::setupUsers),
                Map.entry("iam-roles",    this::setupRoles),
                Map.entry("iam-policies", this::setupPolicies),
                Map.entry("iam-groups",   this::setupIamGroups)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("iam-users",    this::teardownUsers),
                Map.entry("iam-roles",    this::teardownRoles),
                Map.entry("iam-policies", this::teardownPolicies),
                Map.entry("iam-groups",   this::teardownIamGroups)
        );
    }

    // ── iam-users ─────────────────────────────────────────────────────────────

    private void setupUsers(TestContext ctx) {
        String name = "compat-user-" + ctx.runId();
        ctx.set("iamUser", name);
    }

    private void teardownUsers(TestContext ctx) {
        String name = ctx.getString("iamUser");
        if (name == null) return;
        // Delete access keys if present.
        String keyId = ctx.getString("accessKeyId");
        if (keyId != null) {
            try { iam().deleteAccessKey(r -> r.userName(name).accessKeyId(keyId)); } catch (Exception ignored) {}
        }
        try { iam().deleteUser(r -> r.userName(name)); } catch (Exception ignored) {}
    }

    private void createUser(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        var resp = iam().createUser(r -> r.userName(name));
        Assertions.assertNotBlank(resp.user().userId(), "CreateUser: userId is blank");
        Assertions.assertNotBlank(resp.user().arn(), "CreateUser: arn is blank");
    }

    private void getUser(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        var resp = iam().getUser(r -> r.userName(name));
        Assertions.assertEquals(name, resp.user().userName(), "GetUser: userName mismatch");
    }

    private void listUsers(TestContext ctx) throws Exception {
        var resp = iam().listUsers(r -> r.maxItems(100));
        Assertions.assertNotNull(resp.users(), "ListUsers: users is null");
        String name = ctx.getString("iamUser");
        boolean found = resp.users().stream().anyMatch(u -> u.userName().equals(name));
        Assertions.assertTrue(found, "ListUsers: created user not found in list");
    }

    private void updateUser(TestContext ctx) throws Exception {
        String current = ctx.getString("iamUser");
        if (current == null) {
            current = "compat-user-upd-" + ctx.runId();
            final String c = current;
            iam().createUser(r -> r.userName(c));
            ctx.set("iamUser", current);
        }
        final String cur = current;
        String updated = current + "-updated";
        iam().updateUser(r -> r.userName(cur).newUserName(updated));
        iam().getUser(r -> r.userName(updated));
        // Rename back so teardown works.
        iam().updateUser(r -> r.userName(updated).newUserName(cur));
    }

    private void createAccessKey(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        var resp = iam().createAccessKey(r -> r.userName(name));
        Assertions.assertNotBlank(resp.accessKey().accessKeyId(), "CreateAccessKey: accessKeyId is blank");
        ctx.set("accessKeyId", resp.accessKey().accessKeyId());
    }

    private void listAccessKeys(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        if (name == null) {
            name = "compat-user-lak-" + ctx.runId();
            final String n = name;
            iam().createUser(r -> r.userName(n));
            ctx.set("iamUser", name);
        }
        final String n = name;
        var resp = iam().listAccessKeys(r -> r.userName(n));
        Assertions.assertNotNull(resp.accessKeyMetadata(), "ListAccessKeys: metadata is null");
    }

    private void deleteAccessKey(TestContext ctx) throws Exception {
        String name  = ctx.getString("iamUser");
        String keyId = ctx.getString("accessKeyId");
        iam().deleteAccessKey(r -> r.userName(name).accessKeyId(keyId));
        ctx.set("accessKeyId", null);
    }

    private void putUserPolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        iam().putUserPolicy(r -> r.userName(name).policyName("inline").policyDocument(INLINE_POLICY));
    }

    private void getUserPolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        var resp = iam().getUserPolicy(r -> r.userName(name).policyName("inline"));
        Assertions.assertNotBlank(resp.policyDocument(), "GetUserPolicy: policyDocument is blank");
    }

    private void deleteUserPolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        iam().deleteUserPolicy(r -> r.userName(name).policyName("inline"));
    }

    private void deleteUser(TestContext ctx) throws Exception {
        String name = ctx.getString("iamUser");
        iam().deleteUser(r -> r.userName(name));
        ctx.set("iamUser", null);
    }

    // ── iam-roles ─────────────────────────────────────────────────────────────

    private void setupRoles(TestContext ctx) {
        ctx.set("iamRole", "compat-role-" + ctx.runId());
    }

    private void teardownRoles(TestContext ctx) {
        String name = ctx.getString("iamRole");
        if (name == null) return;
        String policyArn = ctx.getString("managedPolicyArn");
        if (policyArn != null) {
            try { iam().detachRolePolicy(r -> r.roleName(name).policyArn(policyArn)); } catch (Exception ignored) {}
        }
        try { iam().deleteRolePolicy(r -> r.roleName(name).policyName("inline")); } catch (Exception ignored) {}
        // Clean up instance profile
        String ipName = ctx.getString("iamInstanceProfile");
        if (ipName != null) {
            try { iam().removeRoleFromInstanceProfile(r -> r.instanceProfileName(ipName).roleName(name)); } catch (Exception ignored) {}
            try { iam().deleteInstanceProfile(r -> r.instanceProfileName(ipName)); } catch (Exception ignored) {}
        }
        try { iam().deleteRole(r -> r.roleName(name)); } catch (Exception ignored) {}
    }

    private void createIamRole(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().createRole(r -> r
                .roleName(name)
                .assumeRolePolicyDocument(BASIC_ASSUME_ROLE_POLICY));
        Assertions.assertNotBlank(resp.role().roleId(), "CreateIamRole: roleId is blank");
        Assertions.assertNotBlank(resp.role().arn(), "CreateIamRole: arn is blank");
    }

    private void getIamRole(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().getRole(r -> r.roleName(name));
        Assertions.assertEquals(name, resp.role().roleName(), "GetIamRole: roleName mismatch");
    }

    private void listIamRoles(TestContext ctx) throws Exception {
        var resp = iam().listRoles(r -> r.maxItems(100));
        Assertions.assertNotNull(resp.roles(), "ListIamRoles: roles is null");
    }

    private void putRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        iam().putRolePolicy(r -> r.roleName(name).policyName("inline").policyDocument(INLINE_POLICY));
    }

    private void getRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().getRolePolicy(r -> r.roleName(name).policyName("inline"));
        Assertions.assertNotBlank(resp.policyDocument(), "GetRolePolicy: policyDocument is blank");
    }

    private void listRolePolicies(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().listRolePolicies(r -> r.roleName(name));
        Assertions.assertTrue(resp.policyNames().contains("inline"), "ListRolePolicies: inline policy not found");
    }

    private void deleteRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        iam().deleteRolePolicy(r -> r.roleName(name).policyName("inline"));
    }

    private void attachRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        String arn  = ctx.getString("managedPolicyArn");
        if (arn == null) return; // requires CreateIamPolicy group to ran first
        iam().attachRolePolicy(r -> r.roleName(name).policyArn(arn));
    }

    private void listAttachedRolePolicies(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().listAttachedRolePolicies(r -> r.roleName(name));
        Assertions.assertNotNull(resp.attachedPolicies(), "ListAttachedRolePolicies: attachedPolicies is null");
    }

    private void detachRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        String arn  = ctx.getString("managedPolicyArn");
        if (arn == null) return;
        iam().detachRolePolicy(r -> r.roleName(name).policyArn(arn));
    }

    private void deleteIamRole(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        iam().deleteRole(r -> r.roleName(name));
        ctx.set("iamRole", null);
    }

    private void createInstanceProfile(TestContext ctx) throws Exception {
        String name = "compat-ip-" + ctx.runId();
        try {
            var resp = iam().createInstanceProfile(r -> r.instanceProfileName(name));
            Assertions.assertNotBlank(resp.instanceProfile().instanceProfileName(), "CreateInstanceProfile: name is blank");
        } catch (EntityAlreadyExistsException e) {
            // Idempotent: profile exists from a prior run against persistent emulator.
        }
        ctx.set("iamInstanceProfile", name);
    }

    private void addRoleToInstanceProfile(TestContext ctx) throws Exception {
        String ipName = ctx.getString("iamInstanceProfile");
        String roleName = ctx.getString("iamRole");
        iam().addRoleToInstanceProfile(r -> r.instanceProfileName(ipName).roleName(roleName));
    }

    private void getInstanceProfile(TestContext ctx) throws Exception {
        String ipName = ctx.getString("iamInstanceProfile");
        var resp = iam().getInstanceProfile(r -> r.instanceProfileName(ipName));
        Assertions.assertEquals(ipName, resp.instanceProfile().instanceProfileName(), "GetInstanceProfile: name mismatch");
    }

    // ── iam-policies ──────────────────────────────────────────────────────────

    private void setupPolicies(TestContext ctx) {
        ctx.set("iamManagedPolicyName", "compat-policy-" + ctx.runId());
    }

    private void teardownPolicies(TestContext ctx) {
        String arn = ctx.getString("managedPolicyArn");
        if (arn == null) return;
        try { iam().deletePolicy(r -> r.policyArn(arn)); } catch (Exception ignored) {}
    }

    private void createIamPolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamManagedPolicyName");
        var resp = iam().createPolicy(r -> r.policyName(name).policyDocument(INLINE_POLICY));
        Assertions.assertNotBlank(resp.policy().policyId(), "CreateIamPolicy: policyId is blank");
        ctx.set("managedPolicyArn", resp.policy().arn());
    }

    private void getIamPolicy(TestContext ctx) throws Exception {
        String arn = ctx.getString("managedPolicyArn");
        var resp = iam().getPolicy(r -> r.policyArn(arn));
        Assertions.assertNotBlank(resp.policy().policyName(), "GetIamPolicy: policyName is blank");
    }

    private void listIamPolicies(TestContext ctx) throws Exception {
        var resp = iam().listPolicies(r -> r.scope(PolicyScopeType.LOCAL).maxItems(100));
        Assertions.assertNotNull(resp.policies(), "ListIamPolicies: policies is null");
    }

    private void deleteIamPolicy(TestContext ctx) throws Exception {
        String arn = ctx.getString("managedPolicyArn");
        iam().deletePolicy(r -> r.policyArn(arn));
        ctx.set("managedPolicyArn", null);
    }

    // ── iam-groups ────────────────────────────────────────────────────────────

    private void setupIamGroups(TestContext ctx) throws Exception {
        String grp  = "compat-grp-" + ctx.runId();
        String user = "compat-grpu-" + ctx.runId();
        iam().createGroup(r -> r.groupName(grp));
        iam().createUser(r -> r.userName(user));
        ctx.set("iamGroupName", grp);
        ctx.set("iamGroupUser", user);
    }

    private void teardownIamGroups(TestContext ctx) {
        String grp  = ctx.getString("iamGroupName");
        String user = ctx.getString("iamGroupUser");
        if (user != null)
            try { iam().removeUserFromGroup(r -> r.groupName(grp).userName(user)); } catch (Exception ignored) {}
        if (grp != null)
            try { iam().deleteGroup(r -> r.groupName(grp)); } catch (Exception ignored) {}
        if (user != null)
            try { iam().deleteUser(r -> r.userName(user)); } catch (Exception ignored) {}
    }

    private void createGroup(TestContext ctx) {
        Assertions.assertNotBlank(ctx.getString("iamGroupName"), "iamGroupName");
    }

    private void getGroup(TestContext ctx) throws Exception {
        String grp = ctx.getString("iamGroupName");
        var resp = iam().getGroup(r -> r.groupName(grp));
        Assertions.assertEquals(grp, resp.group().groupName(), "GetGroup: groupName mismatch");
    }

    private void addUserToGroup(TestContext ctx) throws Exception {
        String grp  = ctx.getString("iamGroupName");
        String user = ctx.getString("iamGroupUser");
        iam().addUserToGroup(r -> r.groupName(grp).userName(user));
    }

    private void listGroupsForUser(TestContext ctx) throws Exception {
        String user = ctx.getString("iamGroupUser");
        var resp = iam().listGroupsForUser(r -> r.userName(user));
        boolean found = resp.groups().stream()
                .anyMatch(g -> g.groupName().equals(ctx.getString("iamGroupName")));
        Assertions.assertTrue(found, "ListGroupsForUser: group not found for user");
    }

    private void removeUserFromGroup(TestContext ctx) throws Exception {
        String grp  = ctx.getString("iamGroupName");
        String user = ctx.getString("iamGroupUser");
        iam().removeUserFromGroup(r -> r.groupName(grp).userName(user));
    }

    private void deleteGroup(TestContext ctx) throws Exception {
        String grp = ctx.getString("iamGroupName");
        iam().deleteGroup(r -> r.groupName(grp));
        ctx.set("iamGroupName", null);
    }
}
