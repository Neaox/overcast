package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.Assertions;
import io.overcast.compat.harness.TestContext;
import io.overcast.compat.harness.TestFn;
import software.amazon.awssdk.services.iam.IamClient;
import software.amazon.awssdk.services.iam.model.*;
import software.amazon.awssdk.services.iam.model.PolicyScopeType;

import java.util.List;
import java.util.Map;

/**
 * IAM compatibility test group.
 *
 * <p>Groups: iam-users, iam-roles, iam-policies, iam-groups, iam-simulate.
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

    /**
     * A document AWS refuses with MalformedPolicyDocument: "Statements must
     * include either an Action or NotAction element" (IAM User Guide,
     * reference_policies_elements_action.html). Every writer that takes a
     * document names that error (IAM API Reference, API_CreatePolicy.html and
     * API_CreateRole.html, Errors).
     */
    private static final String MALFORMED_POLICY = """
            {
              "Version": "2012-10-17",
              "Statement": [{
                "Effect": "Allow",
                "Resource": "*"
              }]
            }
            """;

    /**
     * The tag set the role and policy fixtures are created with, and the one
     * GetRole and GetPolicy must hand back on the resource itself.
     */
    private static final List<Tag> RESOURCE_TAGS = List.of(
            Tag.builder().key("owner").value("compat").build(),
            Tag.builder().key("stage").value("dev").build());

    private final AwsClients clients;

    public IamGroup(AwsClients clients) {
        this.clients = clients;
    }

    private IamClient iam() { return clients.iam(); }

    @Override
    public Map<String, TestFn> impls() {
        return Map.ofEntries(
                Map.entry("iam-users:CreateUser",                             this::createUser),
                Map.entry("iam-users:GetUser",                                this::getUser),
                Map.entry("iam-users:ListUsers",                              this::listUsers),
                Map.entry("iam-users:UpdateUser",                             this::updateUser),
                Map.entry("iam-users:CreateAccessKey",                        this::createAccessKey),
                Map.entry("iam-users:ListAccessKeys",                         this::listAccessKeys),
                Map.entry("iam-users:DeleteAccessKey",                        this::deleteAccessKey),
                Map.entry("iam-users:PutUserPolicy",                          this::putUserPolicy),
                Map.entry("iam-users:GetUserPolicy",                          this::getUserPolicy),
                Map.entry("iam-users:DeleteUserPolicy",                       this::deleteUserPolicy),
                Map.entry("iam-users:DeleteUser",                             this::deleteUser),
                Map.entry("iam-roles:CreateRole",                             this::createIamRole),
                Map.entry("iam-roles:CreateRoleMalformedDocument",            this::createRoleMalformedDocument),
                Map.entry("iam-roles:GetRole",                                this::getIamRole),
                Map.entry("iam-roles:GetRoleReturnsTags",                     this::getRoleReturnsTags),
                Map.entry("iam-roles:ListRoles",                              this::listIamRoles),
                Map.entry("iam-roles:PutRolePolicy",                          this::putRolePolicy),
                Map.entry("iam-roles:GetRolePolicy",                          this::getRolePolicy),
                Map.entry("iam-roles:ListRolePolicies",                       this::listRolePolicies),
                Map.entry("iam-roles:DeleteRolePolicy",                       this::deleteRolePolicy),
                Map.entry("iam-roles:AttachRolePolicy",                       this::attachRolePolicy),
                Map.entry("iam-roles:ListAttachedRolePolicies",               this::listAttachedRolePolicies),
                Map.entry("iam-roles:DetachRolePolicy",                       this::detachRolePolicy),
                Map.entry("iam-roles:DeleteRole",                             this::deleteIamRole),
                Map.entry("iam-roles:CreateInstanceProfile",                  this::createInstanceProfile),
                Map.entry("iam-roles:AddRoleToInstanceProfile",               this::addRoleToInstanceProfile),
                Map.entry("iam-roles:GetInstanceProfile",                     this::getInstanceProfile),
                Map.entry("iam-policies:CreatePolicy",                        this::createIamPolicy),
                Map.entry("iam-policies:CreatePolicyMalformedDocument",       this::createPolicyMalformedDocument),
                Map.entry("iam-policies:GetPolicy",                           this::getIamPolicy),
                Map.entry("iam-policies:GetPolicyReturnsTags",                this::getPolicyReturnsTags),
                Map.entry("iam-policies:ListPolicies",                        this::listIamPolicies),
                Map.entry("iam-policies:GetPolicyAttachmentCountAfterAttach", this::getPolicyAttachmentCountAfterAttach),
                Map.entry("iam-policies:GetPolicyAttachmentCountAfterDetach", this::getPolicyAttachmentCountAfterDetach),
                Map.entry("iam-policies:DeletePolicy",                        this::deleteIamPolicy),
                Map.entry("iam-groups:CreateGroup",                           this::createGroup),
                Map.entry("iam-groups:GetGroup",                              this::getGroup),
                Map.entry("iam-groups:AddUserToGroup",                        this::addUserToGroup),
                Map.entry("iam-groups:ListGroupsForUser",                     this::listGroupsForUser),
                Map.entry("iam-groups:RemoveUserFromGroup",                   this::removeUserFromGroup),
                Map.entry("iam-groups:DeleteGroup",                           this::deleteGroup),
                Map.entry("iam-simulate:SimulateCustomPolicyAllowed",         this::simulateCustomPolicyAllowed),
                Map.entry("iam-simulate:SimulateCustomPolicyImplicitDeny",    this::simulateCustomPolicyImplicitDeny),
                Map.entry("iam-simulate:SimulateCustomPolicyExplicitDeny",    this::simulateCustomPolicyExplicitDeny),
                Map.entry("iam-simulate:SimulatePrincipalPolicyAllowed",      this::simulatePrincipalPolicyAllowed),
                Map.entry("iam-simulate:SimulatePrincipalPolicyImplicitDeny", this::simulatePrincipalPolicyImplicitDeny)
        );
    }

    @Override
    public Map<String, TestFn> setups() {
        return Map.ofEntries(
                Map.entry("iam-users",    this::setupUsers),
                Map.entry("iam-roles",    this::setupRoles),
                Map.entry("iam-policies", this::setupPolicies),
                Map.entry("iam-groups",   this::setupIamGroups),
                Map.entry("iam-simulate", this::setupSimulate)
        );
    }

    @Override
    public Map<String, TestFn> teardowns() {
        return Map.ofEntries(
                Map.entry("iam-users",    this::teardownUsers),
                Map.entry("iam-roles",    this::teardownRoles),
                Map.entry("iam-policies", this::teardownPolicies),
                Map.entry("iam-groups",   this::teardownIamGroups),
                Map.entry("iam-simulate", this::teardownSimulate)
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
        var resp = iam().getUser(r -> r.userName(updated));
        Assertions.assertEquals(updated, resp.user().userName(), "UpdateUser: renamed user not readable under new name");
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
        try { iam().detachRolePolicy(r -> r.roleName(name).policyArn(ATTACHED_MANAGED_POLICY_ARN)); } catch (Exception ignored) {}
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
                .assumeRolePolicyDocument(BASIC_ASSUME_ROLE_POLICY)
                .tags(RESOURCE_TAGS));
        Assertions.assertNotBlank(resp.role().roleId(), "CreateIamRole: roleId is blank");
        Assertions.assertNotBlank(resp.role().arn(), "CreateIamRole: arn is blank");
    }

    private void getIamRole(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().getRole(r -> r.roleName(name));
        Assertions.assertEquals(name, resp.role().roleName(), "GetIamRole: roleName mismatch");
    }

    /** A trust policy AWS would refuse must be refused here, not stored unparsed. */
    private void createRoleMalformedDocument(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole") + "-malformed";
        try {
            iam().createRole(r -> r.roleName(name).assumeRolePolicyDocument(MALFORMED_POLICY));
        } catch (IamException e) {
            assertMalformedPolicyDocument("CreateRoleMalformedDocument", e);
            return;
        }
        throw new AssertionError(
                "CreateRoleMalformedDocument: expected MalformedPolicyDocument, call succeeded");
    }

    /** Tags given to CreateRole come back on the role (API_Role.html). */
    private void getRoleReturnsTags(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().getRole(r -> r.roleName(name));
        assertResourceTags("GetRoleReturnsTags", resp.role().tags());
    }

    private void listIamRoles(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().listRoles(r -> r.maxItems(1000));
        boolean found = resp.roles().stream().anyMatch(role -> name.equals(role.roleName()));
        Assertions.assertTrue(found, "ListIamRoles: created role " + name + " not found in list");
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

    // Each group gets its own TestContext, so the managed policy the
    // iam-policies group creates is never visible here. Attach an AWS-managed
    // policy instead (as the node-js/go suites do) so these three tests
    // actually exercise attach → list → detach rather than silently no-op'ing.
    private static final String ATTACHED_MANAGED_POLICY_ARN =
            "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess";

    private void attachRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        iam().attachRolePolicy(r -> r.roleName(name).policyArn(ATTACHED_MANAGED_POLICY_ARN));
    }

    private void listAttachedRolePolicies(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        var resp = iam().listAttachedRolePolicies(r -> r.roleName(name));
        boolean found = resp.attachedPolicies().stream()
                .anyMatch(p -> ATTACHED_MANAGED_POLICY_ARN.equals(p.policyArn()));
        Assertions.assertTrue(found, "ListAttachedRolePolicies: attached policy not found in list");
    }

    private void detachRolePolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        iam().detachRolePolicy(r -> r.roleName(name).policyArn(ATTACHED_MANAGED_POLICY_ARN));
    }

    private void deleteIamRole(TestContext ctx) throws Exception {
        String name = ctx.getString("iamRole");
        // AWS refuses DeleteRole with DeleteConflict while the role is still in
        // an instance profile, so clear that first — this group put it there in
        // AddRoleToInstanceProfile.
        String ipName = ctx.getString("iamInstanceProfile");
        if (ipName != null) {
            try { iam().removeRoleFromInstanceProfile(r -> r.instanceProfileName(ipName).roleName(name)); } catch (Exception ignored) {}
            try { iam().deleteInstanceProfile(r -> r.instanceProfileName(ipName)); } catch (Exception ignored) {}
            ctx.set("iamInstanceProfile", null);
        }
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
        // The counter tests attach this group's own policy to a role of its
        // own, so AttachmentCount moves for a customer managed policy rather
        // than for the AWS managed one iam-roles attaches.
        String role = "compat-policy-role-" + ctx.runId();
        iam().createRole(r -> r.roleName(role).assumeRolePolicyDocument(BASIC_ASSUME_ROLE_POLICY));
        ctx.set("iamPolicyRole", role);
    }

    private void teardownPolicies(TestContext ctx) {
        String arn = ctx.getString("managedPolicyArn");
        String role = ctx.getString("iamPolicyRole");
        if (role != null) {
            if (arn != null) {
                try { iam().detachRolePolicy(r -> r.roleName(role).policyArn(arn)); } catch (Exception ignored) {}
            }
            try { iam().deleteRole(r -> r.roleName(role)); } catch (Exception ignored) {}
        }
        if (arn == null) return;
        try { iam().deletePolicy(r -> r.policyArn(arn)); } catch (Exception ignored) {}
    }

    private void createIamPolicy(TestContext ctx) throws Exception {
        String name = ctx.getString("iamManagedPolicyName");
        var resp = iam().createPolicy(r -> r.policyName(name)
                .policyDocument(INLINE_POLICY)
                .tags(RESOURCE_TAGS));
        Assertions.assertNotBlank(resp.policy().policyId(), "CreateIamPolicy: policyId is blank");
        ctx.set("managedPolicyArn", resp.policy().arn());
    }

    private void getIamPolicy(TestContext ctx) throws Exception {
        String arn = ctx.getString("managedPolicyArn");
        var resp = iam().getPolicy(r -> r.policyArn(arn));
        Assertions.assertNotBlank(resp.policy().policyName(), "GetIamPolicy: policyName is blank");
    }

    private void listIamPolicies(TestContext ctx) throws Exception {
        String name = ctx.getString("iamManagedPolicyName");
        var resp = iam().listPolicies(r -> r.scope(PolicyScopeType.LOCAL).maxItems(1000));
        boolean found = resp.policies().stream().anyMatch(p -> name.equals(p.policyName()));
        Assertions.assertTrue(found, "ListIamPolicies: created policy " + name + " not found in list");
    }

    /** The same refusal on the identity-policy writer. */
    private void createPolicyMalformedDocument(TestContext ctx) throws Exception {
        String name = ctx.getString("iamManagedPolicyName") + "-malformed";
        try {
            iam().createPolicy(r -> r.policyName(name).policyDocument(MALFORMED_POLICY));
        } catch (IamException e) {
            assertMalformedPolicyDocument("CreatePolicyMalformedDocument", e);
            return;
        }
        throw new AssertionError(
                "CreatePolicyMalformedDocument: expected MalformedPolicyDocument, call succeeded");
    }

    /** Tags given to CreatePolicy come back on the policy (API_Policy.html). */
    private void getPolicyReturnsTags(TestContext ctx) throws Exception {
        String arn = ctx.getString("managedPolicyArn");
        var resp = iam().getPolicy(r -> r.policyArn(arn));
        assertResourceTags("GetPolicyReturnsTags", resp.policy().tags());
    }

    /** Reads AttachmentCount back through GetPolicy. */
    private int attachmentCount(TestContext ctx, String op) {
        String arn = ctx.getString("managedPolicyArn");
        var resp = iam().getPolicy(r -> r.policyArn(arn));
        Integer count = resp.policy().attachmentCount();
        Assertions.assertNotNull(count, op + ": GetPolicy returned no AttachmentCount");
        return count;
    }

    /**
     * AttachmentCount moves 0 to 1. "The number of entities (users, groups, and
     * roles) that the policy is attached to" (IAM API Reference,
     * API_Policy.html) is what a cleanup script reads before deleting a policy,
     * so a stuck 0 deletes something in use.
     */
    private void getPolicyAttachmentCountAfterAttach(TestContext ctx) throws Exception {
        final String op = "GetPolicyAttachmentCountAfterAttach";
        Assertions.assertEquals(0, attachmentCount(ctx, op), op + ": AttachmentCount before the attach");
        String arn = ctx.getString("managedPolicyArn");
        String role = ctx.getString("iamPolicyRole");
        Assertions.assertNotNull(role, op + ": no role from setup");
        iam().attachRolePolicy(r -> r.roleName(role).policyArn(arn));
        Assertions.assertEquals(1, attachmentCount(ctx, op),
                op + ": AttachmentCount after attaching to one role");
    }

    /** The counter moves back to 0, which a never-decremented one would fail. */
    private void getPolicyAttachmentCountAfterDetach(TestContext ctx) throws Exception {
        final String op = "GetPolicyAttachmentCountAfterDetach";
        String arn = ctx.getString("managedPolicyArn");
        String role = ctx.getString("iamPolicyRole");
        Assertions.assertNotNull(role, op + ": no role from setup");
        iam().detachRolePolicy(r -> r.roleName(role).policyArn(arn));
        Assertions.assertEquals(0, attachmentCount(ctx, op),
                op + ": AttachmentCount after the detach");
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

    // ── iam-simulate ──────────────────────────────────────────────────────────

    /** The identity policy the simulate group evaluates: read one run-scoped prefix, nothing else. */
    private String simPolicy(TestContext ctx) {
        return """
                {
                  "Version": "2012-10-17",
                  "Statement": [{
                    "Effect": "Allow",
                    "Action": "s3:GetObject",
                    "Resource": "arn:aws:s3:::compat-sim-%s/*"
                  }]
                }
                """.formatted(ctx.runId());
    }

    private String simResource(TestContext ctx) {
        return "arn:aws:s3:::compat-sim-" + ctx.runId() + "/report.csv";
    }

    private void setupSimulate(TestContext ctx) {
        String name = "compat-sim-user-" + ctx.runId();
        iam().createUser(r -> r.userName(name));
        iam().putUserPolicy(r -> r.userName(name)
                .policyName("sim-allow-read")
                .policyDocument(simPolicy(ctx)));
        ctx.set("iamSimUser", name);
    }

    private void teardownSimulate(TestContext ctx) {
        String name = ctx.getString("iamSimUser");
        if (name == null) return;
        try { iam().deleteUserPolicy(r -> r.userName(name).policyName("sim-allow-read")); } catch (Exception ignored) {}
        try { iam().deleteUser(r -> r.userName(name)); } catch (Exception ignored) {}
    }

    private void simulateCustomPolicyAllowed(TestContext ctx) throws Exception {
        var resp = iam().simulateCustomPolicy(r -> r
                .policyInputList(simPolicy(ctx))
                .actionNames("s3:GetObject")
                .resourceArns(simResource(ctx)));
        Assertions.assertTrue(!resp.evaluationResults().isEmpty(),
                "SimulateCustomPolicy: no evaluation results");
        var result = resp.evaluationResults().get(0);
        Assertions.assertEquals("allowed", result.evalDecisionAsString(),
                "SimulateCustomPolicy: wrong decision");
        Assertions.assertEquals("s3:GetObject", result.evalActionName(),
                "SimulateCustomPolicy: wrong evalActionName");
    }

    private void simulateCustomPolicyImplicitDeny(TestContext ctx) throws Exception {
        var resp = iam().simulateCustomPolicy(r -> r
                .policyInputList(simPolicy(ctx))
                .actionNames("s3:PutObject")
                .resourceArns(simResource(ctx)));
        Assertions.assertTrue(!resp.evaluationResults().isEmpty(),
                "SimulateCustomPolicy: no evaluation results");
        Assertions.assertEquals("implicitDeny", resp.evaluationResults().get(0).evalDecisionAsString(),
                "SimulateCustomPolicy: uncovered action should be implicitDeny");
    }

    private void simulateCustomPolicyExplicitDeny(TestContext ctx) throws Exception {
        String doc = """
                {
                  "Version": "2012-10-17",
                  "Statement": [
                    {"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
                    {"Effect": "Deny", "Action": "s3:DeleteObject", "Resource": "*"}
                  ]
                }
                """;
        var resp = iam().simulateCustomPolicy(r -> r
                .policyInputList(doc)
                .actionNames("s3:DeleteObject")
                .resourceArns(simResource(ctx)));
        Assertions.assertTrue(!resp.evaluationResults().isEmpty(),
                "SimulateCustomPolicy: no evaluation results");
        Assertions.assertEquals("explicitDeny", resp.evaluationResults().get(0).evalDecisionAsString(),
                "SimulateCustomPolicy: explicit deny should win");
    }

    private void simulatePrincipalPolicyAllowed(TestContext ctx) throws Exception {
        String arn = "arn:aws:iam::000000000000:user/" + ctx.getString("iamSimUser");
        var resp = iam().simulatePrincipalPolicy(r -> r
                .policySourceArn(arn)
                .actionNames("s3:GetObject")
                .resourceArns(simResource(ctx)));
        Assertions.assertTrue(!resp.evaluationResults().isEmpty(),
                "SimulatePrincipalPolicy: no evaluation results");
        var result = resp.evaluationResults().get(0);
        Assertions.assertEquals("allowed", result.evalDecisionAsString(),
                "SimulatePrincipalPolicy: wrong decision");
        boolean matched = result.matchedStatements().stream()
                .anyMatch(s -> "sim-allow-read".equals(s.sourcePolicyId()));
        Assertions.assertTrue(matched,
                "SimulatePrincipalPolicy: matchedStatements missing sim-allow-read");
    }

    private void simulatePrincipalPolicyImplicitDeny(TestContext ctx) throws Exception {
        String arn = "arn:aws:iam::000000000000:user/" + ctx.getString("iamSimUser");
        var resp = iam().simulatePrincipalPolicy(r -> r
                .policySourceArn(arn)
                .actionNames("s3:DeleteObject")
                .resourceArns(simResource(ctx)));
        Assertions.assertTrue(!resp.evaluationResults().isEmpty(),
                "SimulatePrincipalPolicy: no evaluation results");
        Assertions.assertEquals("implicitDeny", resp.evaluationResults().get(0).evalDecisionAsString(),
                "SimulatePrincipalPolicy: uncovered action should be implicitDeny");
    }

    // ── shared assertions ─────────────────────────────────────────────────────

    /**
     * Checks both halves of the error contract: the code AWS's model names, and
     * the 400 the Query protocol binds it to.
     */
    private static void assertMalformedPolicyDocument(String op, IamException e) {
        String code = e.awsErrorDetails() == null ? "" : e.awsErrorDetails().errorCode();
        Assertions.assertEquals("MalformedPolicyDocument", code, op + ": unexpected error code");
        Assertions.assertEquals(400, e.statusCode(),
                op + ": expected HTTP 400 for MalformedPolicyDocument");
    }

    /** Checks the two fixture tags on a resource returned by a Get* call. */
    private static void assertResourceTags(String op, List<Tag> tags) {
        for (Tag want : RESOURCE_TAGS) {
            boolean found = tags != null && tags.stream()
                    .anyMatch(t -> want.key().equals(t.key()) && want.value().equals(t.value()));
            Assertions.assertTrue(found,
                    op + ": tag " + want.key() + "=" + want.value() + " not on the resource (tags: " + tags + ")");
        }
    }
}
