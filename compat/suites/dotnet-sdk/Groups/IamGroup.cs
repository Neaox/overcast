using Amazon.IdentityManagement;
using Amazon.IdentityManagement.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class IamGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        // iam-users
        ["iam-users:CreateUser"] = CreateUserAsync,
        ["iam-users:GetUser"] = GetUserAsync,
        ["iam-users:ListUsers"] = ListUsersAsync,
        ["iam-users:CreateAccessKey"] = CreateAccessKeyAsync,
        ["iam-users:DeleteAccessKey"] = DeleteAccessKeyAsync,
        ["iam-users:PutUserPolicy"] = PutUserPolicyAsync,
        ["iam-users:GetUserPolicy"] = GetUserPolicyAsync,
        ["iam-users:DeleteUserPolicy"] = DeleteUserPolicyAsync,
        ["iam-users:UpdateUser"] = UpdateUserAsync,
        ["iam-users:ListAccessKeys"] = ListAccessKeysAsync,
        ["iam-users:DeleteUser"] = DeleteUserAsync,
        // iam-roles
        ["iam-roles:CreateRole"] = CreateRoleAsync,
        ["iam-roles:CreateRoleMalformedDocument"] = CreateRoleMalformedDocumentAsync,
        ["iam-roles:GetRole"] = GetRoleAsync,
        ["iam-roles:GetRoleReturnsTags"] = GetRoleReturnsTagsAsync,
        ["iam-roles:ListRoles"] = ListRolesAsync,
        ["iam-roles:AttachRolePolicy"] = AttachRolePolicyAsync,
        ["iam-roles:ListAttachedRolePolicies"] = ListAttachedRolePoliciesAsync,
        ["iam-roles:DetachRolePolicy"] = DetachRolePolicyAsync,
        ["iam-roles:CreateInstanceProfile"] = CreateInstanceProfileAsync,
        ["iam-roles:AddRoleToInstanceProfile"] = AddRoleToInstanceProfileAsync,
        ["iam-roles:GetInstanceProfile"] = GetInstanceProfileAsync,
        ["iam-roles:PutRolePolicy"] = PutRolePolicyAsync,
        ["iam-roles:GetRolePolicy"] = GetRolePolicyAsync,
        ["iam-roles:ListRolePolicies"] = ListRolePoliciesAsync,
        ["iam-roles:DeleteRolePolicy"] = DeleteRolePolicyAsync,
        ["iam-roles:DeleteRole"] = DeleteRoleAsync,
        // iam-policies
        ["iam-policies:CreatePolicy"] = CreatePolicyAsync,
        ["iam-policies:CreatePolicyMalformedDocument"] = CreatePolicyMalformedDocumentAsync,
        ["iam-policies:GetPolicy"] = GetPolicyAsync,
        ["iam-policies:GetPolicyReturnsTags"] = GetPolicyReturnsTagsAsync,
        ["iam-policies:ListPolicies"] = ListPoliciesAsync,
        ["iam-policies:GetPolicyAttachmentCountAfterAttach"] = GetPolicyAttachmentCountAfterAttachAsync,
        ["iam-policies:GetPolicyAttachmentCountAfterDetach"] = GetPolicyAttachmentCountAfterDetachAsync,
        ["iam-policies:DeletePolicy"] = DeletePolicyAsync,
        // iam-groups
        ["iam-groups:CreateGroup"] = CreateGroupAsync,
        ["iam-groups:AddUserToGroup"] = AddUserToGroupAsync,
        ["iam-groups:ListGroupsForUser"] = ListGroupsForUserAsync,
        ["iam-groups:RemoveUserFromGroup"] = RemoveUserFromGroupAsync,
        ["iam-groups:GetGroup"] = GetGroupAsync,
        ["iam-groups:DeleteGroup"] = DeleteGroupAsync,
        // iam-simulate
        ["iam-simulate:SimulateCustomPolicyAllowed"] = SimulateCustomPolicyAllowedAsync,
        ["iam-simulate:SimulateCustomPolicyImplicitDeny"] = SimulateCustomPolicyImplicitDenyAsync,
        ["iam-simulate:SimulateCustomPolicyExplicitDeny"] = SimulateCustomPolicyExplicitDenyAsync,
        ["iam-simulate:SimulatePrincipalPolicyAllowed"] = SimulatePrincipalPolicyAllowedAsync,
        ["iam-simulate:SimulatePrincipalPolicyImplicitDeny"] = SimulatePrincipalPolicyImplicitDenyAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["iam-users"] = SetupUsersAsync,
        ["iam-roles"] = SetupRolesAsync,
        ["iam-policies"] = SetupPoliciesAsync,
        ["iam-groups"] = SetupGroupsAsync,
        ["iam-simulate"] = SetupSimulateAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["iam-users"] = TeardownUsersAsync,
        ["iam-roles"] = TeardownRolesAsync,
        ["iam-policies"] = TeardownPoliciesAsync,
        ["iam-groups"] = TeardownGroupsAsync,
        ["iam-simulate"] = TeardownSimulateAsync,
    };

    /// <summary>
    /// A document AWS refuses with MalformedPolicyDocument: "Statements must
    /// include either an Action or NotAction element" (IAM User Guide,
    /// reference_policies_elements_action.html). Every writer that takes a
    /// document names that error (IAM API Reference, API_CreatePolicy.html and
    /// API_CreateRole.html, Errors).
    /// </summary>
    private const string MalformedPolicy = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Resource"":""*""}]}";

    /// <summary>
    /// The tag set the role and policy fixtures are created with, and the one
    /// GetRole and GetPolicy must hand back on the resource itself.
    /// </summary>
    private static List<Tag> ResourceTags() => new()
    {
        new Tag { Key = "owner", Value = "compat" },
        new Tag { Key = "stage", Value = "dev" },
    };

    /// <summary>
    /// Checks both halves of the error contract: the code AWS's model names,
    /// and the 400 the Query protocol binds it to.
    /// </summary>
    private static void AssertMalformedPolicyDocument(string op, AmazonIdentityManagementServiceException e)
    {
        Assertions.Equal("MalformedPolicyDocument", e.ErrorCode, $"{op}: expected MalformedPolicyDocument but was {e.ErrorCode}");
        Assertions.Equal(System.Net.HttpStatusCode.BadRequest, e.StatusCode, $"{op}: expected HTTP 400 for MalformedPolicyDocument but was {(int)e.StatusCode}");
    }

    /// <summary>Checks the two fixture tags on a resource returned by a Get* call.</summary>
    private static void AssertResourceTags(string op, List<Tag>? tags)
    {
        foreach (var want in ResourceTags())
        {
            Assertions.True(
                tags is not null && tags.Any(t => t.Key == want.Key && t.Value == want.Value),
                $"{op}: tag {want.Key}={want.Value} not on the resource");
        }
    }

    // ── iam-users ──

    private async Task SetupUsersAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-user";
        await clients.IAM().CreateUserAsync(new CreateUserRequest { UserName = name });
        context.Set("IamUserName", name);
    }

    private async Task CreateUserAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-create-user";
        var response = await clients.IAM().CreateUserAsync(new CreateUserRequest { UserName = name });
        Assertions.NotBlank(response.User.UserId, "CreateUser: UserId");
        try
        {
            var list = await clients.IAM().ListUsersAsync(new ListUsersRequest());
            Assertions.True(list.Users.Any(u => u.UserName == name), $"CreateUser: user {name} not found in ListUsers (runId={context.RunId})");
        }
        finally
        {
            try { await clients.IAM().DeleteUserAsync(new DeleteUserRequest { UserName = name }); } catch { }
        }
    }

    private async Task GetUserAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var response = await clients.IAM().GetUserAsync(new GetUserRequest { UserName = userName });
        Assertions.Equal(userName, response.User.UserName, "GetUser: UserName mismatch");
    }

    private async Task ListUsersAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var response = await clients.IAM().ListUsersAsync(new ListUsersRequest());
        Assertions.True(response.Users.Any(u => u.UserName == userName), $"ListUsers: user {userName} not found (runId={context.RunId})");
    }

    private async Task CreateAccessKeyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var response = await clients.IAM().CreateAccessKeyAsync(new CreateAccessKeyRequest { UserName = userName });
        Assertions.NotBlank(response.AccessKey.AccessKeyId, "CreateAccessKey: AccessKeyId");
        context.Set("IamAccessKeyId", response.AccessKey.AccessKeyId);
    }

    private async Task DeleteAccessKeyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var accessKeyId = RequireString(context, "IamAccessKeyId");
        await clients.IAM().DeleteAccessKeyAsync(new DeleteAccessKeyRequest { UserName = userName, AccessKeyId = accessKeyId });
    }

    private async Task PutUserPolicyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        await clients.IAM().PutUserPolicyAsync(new PutUserPolicyRequest
        {
            UserName = userName,
            PolicyName = "inline-user-policy",
            PolicyDocument = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Action"":""s3:GetObject"",""Resource"":""*""}]}",
        });
    }

    private async Task GetUserPolicyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var response = await clients.IAM().GetUserPolicyAsync(new GetUserPolicyRequest { UserName = userName, PolicyName = "inline-user-policy" });
        Assertions.NotBlank(response.PolicyDocument, "GetUserPolicy: PolicyDocument");
        // IAM returns the document "URL-encoded compliant with RFC 3986" and
        // documents that callers decode it; the .NET SDK does not do it for
        // you, unlike boto3.
        Assertions.True(Uri.UnescapeDataString(response.PolicyDocument).Contains("s3:GetObject"), "GetUserPolicy: PolicyDocument missing s3:GetObject");
    }

    private async Task DeleteUserPolicyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        await clients.IAM().DeleteUserPolicyAsync(new DeleteUserPolicyRequest { UserName = userName, PolicyName = "inline-user-policy" });
    }

    private async Task UpdateUserAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        await clients.IAM().UpdateUserAsync(new UpdateUserRequest { UserName = userName, NewPath = "/newpath/" });
        var response = await clients.IAM().GetUserAsync(new GetUserRequest { UserName = userName });
        Assertions.Equal("/newpath/", response.User.Path, "UpdateUser: Path mismatch");
    }

    private async Task ListAccessKeysAsync(TestContext context)
    {
        var userName = RequireString(context, "IamUserName");
        var resp = await clients.IAM().CreateAccessKeyAsync(new CreateAccessKeyRequest { UserName = userName });
        var keyId = resp.AccessKey.AccessKeyId;
        try
        {
            var list = await clients.IAM().ListAccessKeysAsync(new ListAccessKeysRequest { UserName = userName });
            Assertions.True(list.AccessKeyMetadata.Any(k => k.AccessKeyId == keyId), $"ListAccessKeys: key {keyId} not found (runId={context.RunId})");
        }
        finally
        {
            try { await clients.IAM().DeleteAccessKeyAsync(new DeleteAccessKeyRequest { UserName = userName, AccessKeyId = keyId }); } catch { }
        }
    }

    private async Task DeleteUserAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-del-user";
        await clients.IAM().CreateUserAsync(new CreateUserRequest { UserName = name });
        await clients.IAM().DeleteUserAsync(new DeleteUserRequest { UserName = name });
        var list = await clients.IAM().ListUsersAsync(new ListUsersRequest());
        Assertions.False(list.Users.Any(u => u.UserName == name), $"DeleteUser: user {name} still present after deletion (runId={context.RunId})");
    }

    private async Task TeardownUsersAsync(TestContext context)
    {
        var userName = context.GetString("IamUserName");
        if (string.IsNullOrWhiteSpace(userName))
        {
            return;
        }

        try
        {
            var keys = await clients.IAM().ListAccessKeysAsync(new ListAccessKeysRequest { UserName = userName });
            foreach (var key in keys.AccessKeyMetadata)
            {
                try { await clients.IAM().DeleteAccessKeyAsync(new DeleteAccessKeyRequest { UserName = userName, AccessKeyId = key.AccessKeyId }); } catch { }
            }
        }
        catch { }

        try { await clients.IAM().DeleteUserPolicyAsync(new DeleteUserPolicyRequest { UserName = userName, PolicyName = "inline-user-policy" }); } catch { }

        try { await clients.IAM().DeleteUserAsync(new DeleteUserRequest { UserName = userName }); } catch { }
    }

    // ── iam-roles ──

    private async Task SetupRolesAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-role";
        await clients.IAM().CreateRoleAsync(new CreateRoleRequest
        {
            RoleName = name,
            AssumeRolePolicyDocument = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Principal"":{""Service"":""lambda.amazonaws.com""},""Action"":""sts:AssumeRole""}]}",
            Tags = ResourceTags(),
        });
        context.Set("IamRoleName", name);
    }

    private async Task CreateRoleAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-create-role";
        var response = await clients.IAM().CreateRoleAsync(new CreateRoleRequest
        {
            RoleName = name,
            AssumeRolePolicyDocument = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Principal"":{""Service"":""lambda.amazonaws.com""},""Action"":""sts:AssumeRole""}]}",
        });
        Assertions.NotBlank(response.Role.Arn, "CreateRole: Arn");
        try
        {
            var list = await clients.IAM().ListRolesAsync(new ListRolesRequest());
            Assertions.True(list.Roles.Any(r => r.RoleName == name), $"CreateRole: role {name} not found in ListRoles (runId={context.RunId})");
        }
        finally
        {
            try { await clients.IAM().DeleteRoleAsync(new DeleteRoleRequest { RoleName = name }); } catch { }
        }
    }

    private async Task GetRoleAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().GetRoleAsync(new GetRoleRequest { RoleName = roleName });
        Assertions.NotBlank(response.Role.Arn, "GetRole: Arn");
        Assertions.Equal(roleName, response.Role.RoleName, "GetRole: RoleName mismatch");
    }

    /// <summary>A trust policy AWS would refuse must be refused here, not stored unparsed.</summary>
    private async Task CreateRoleMalformedDocumentAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-role-malformed";
        try
        {
            await clients.IAM().CreateRoleAsync(new CreateRoleRequest
            {
                RoleName = name,
                AssumeRolePolicyDocument = MalformedPolicy,
            });
        }
        catch (AmazonIdentityManagementServiceException e)
        {
            AssertMalformedPolicyDocument("CreateRoleMalformedDocument", e);
            return;
        }

        try { await clients.IAM().DeleteRoleAsync(new DeleteRoleRequest { RoleName = name }); } catch { }
        throw new InvalidOperationException(
            $"CreateRoleMalformedDocument: expected MalformedPolicyDocument, call succeeded (runId={context.RunId})");
    }

    /// <summary>Tags given to CreateRole come back on the role (API_Role.html).</summary>
    private async Task GetRoleReturnsTagsAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().GetRoleAsync(new GetRoleRequest { RoleName = roleName });
        AssertResourceTags("GetRoleReturnsTags", response.Role.Tags);
    }

    private async Task ListRolesAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().ListRolesAsync(new ListRolesRequest());
        Assertions.True(response.Roles.Any(r => r.RoleName == roleName), $"ListRoles: role {roleName} not found (runId={context.RunId})");
    }

    private async Task AttachRolePolicyAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        await clients.IAM().AttachRolePolicyAsync(new AttachRolePolicyRequest
        {
            RoleName = roleName,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
        });
    }

    private async Task ListAttachedRolePoliciesAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().ListAttachedRolePoliciesAsync(new ListAttachedRolePoliciesRequest { RoleName = roleName });
        Assertions.True(response.AttachedPolicies.Any(p => p.PolicyArn == "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess"), "ListAttachedRolePolicies: AmazonS3ReadOnlyAccess not found");
    }

    private async Task DetachRolePolicyAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        await clients.IAM().DetachRolePolicyAsync(new DetachRolePolicyRequest
        {
            RoleName = roleName,
            PolicyArn = "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess",
        });
    }

    private async Task CreateInstanceProfileAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-instance-profile";
        var response = await clients.IAM().CreateInstanceProfileAsync(new CreateInstanceProfileRequest { InstanceProfileName = name });
        Assertions.NotBlank(response.InstanceProfile.Arn, "CreateInstanceProfile: Arn");
        context.Set("IamInstanceProfileName", name);
    }

    private async Task AddRoleToInstanceProfileAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var profileName = RequireString(context, "IamInstanceProfileName");
        await clients.IAM().AddRoleToInstanceProfileAsync(new AddRoleToInstanceProfileRequest
        {
            RoleName = roleName,
            InstanceProfileName = profileName,
        });
    }

    private async Task GetInstanceProfileAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var profileName = RequireString(context, "IamInstanceProfileName");
        var response = await clients.IAM().GetInstanceProfileAsync(new GetInstanceProfileRequest { InstanceProfileName = profileName });
        Assertions.True(response.InstanceProfile.Roles.Any(r => r.RoleName == roleName), $"GetInstanceProfile: role {roleName} not attached to profile (runId={context.RunId})");
    }

    private async Task PutRolePolicyAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        await clients.IAM().PutRolePolicyAsync(new PutRolePolicyRequest
        {
            RoleName = roleName,
            PolicyName = "inline-role-policy",
            PolicyDocument = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"logs:*\",\"Resource\":\"*\"}]}",
        });
    }

    private async Task GetRolePolicyAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().GetRolePolicyAsync(new GetRolePolicyRequest { RoleName = roleName, PolicyName = "inline-role-policy" });
        Assertions.NotBlank(response.PolicyDocument, "GetRolePolicy: PolicyDocument");
        Assertions.True(Uri.UnescapeDataString(response.PolicyDocument).Contains("logs:*"), "GetRolePolicy: PolicyDocument missing logs:*");
    }

    private async Task ListRolePoliciesAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        var response = await clients.IAM().ListRolePoliciesAsync(new ListRolePoliciesRequest { RoleName = roleName });
        Assertions.True(response.PolicyNames.Any(n => n == "inline-role-policy"), "ListRolePolicies: inline-role-policy not found");
    }

    private async Task DeleteRolePolicyAsync(TestContext context)
    {
        var roleName = RequireString(context, "IamRoleName");
        await clients.IAM().DeleteRolePolicyAsync(new DeleteRolePolicyRequest { RoleName = roleName, PolicyName = "inline-role-policy" });
    }

    private async Task DeleteRoleAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-del-role";
        await clients.IAM().CreateRoleAsync(new CreateRoleRequest
        {
            RoleName = name,
            AssumeRolePolicyDocument = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Principal"":{""Service"":""lambda.amazonaws.com""},""Action"":""sts:AssumeRole""}]}",
        });
        await clients.IAM().DeleteRoleAsync(new DeleteRoleRequest { RoleName = name });
        var list = await clients.IAM().ListRolesAsync(new ListRolesRequest());
        Assertions.False(list.Roles.Any(r => r.RoleName == name), $"DeleteRole: role {name} still present after deletion (runId={context.RunId})");
    }

    private async Task TeardownRolesAsync(TestContext context)
    {
        var roleName = context.GetString("IamRoleName");
        if (string.IsNullOrWhiteSpace(roleName))
        {
            return;
        }

        try { await clients.IAM().DetachRolePolicyAsync(new DetachRolePolicyRequest { RoleName = roleName, PolicyArn = "arn:aws:iam::aws:policy/AmazonS3ReadOnlyAccess" }); } catch { }

        var profileName = context.GetString("IamInstanceProfileName");
        if (!string.IsNullOrWhiteSpace(profileName))
        {
            try { await clients.IAM().RemoveRoleFromInstanceProfileAsync(new RemoveRoleFromInstanceProfileRequest { RoleName = roleName, InstanceProfileName = profileName }); } catch { }
            try { await clients.IAM().DeleteInstanceProfileAsync(new DeleteInstanceProfileRequest { InstanceProfileName = profileName }); } catch { }
        }

        try { await clients.IAM().DeleteRolePolicyAsync(new DeleteRolePolicyRequest { RoleName = roleName, PolicyName = "inline-role-policy" }); } catch { }

        try { await clients.IAM().DeleteRoleAsync(new DeleteRoleRequest { RoleName = roleName }); } catch { }
    }

    // ── iam-policies ──

    private async Task SetupPoliciesAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-policy";
        var response = await clients.IAM().CreatePolicyAsync(new CreatePolicyRequest
        {
            PolicyName = name,
            PolicyDocument = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:ListBucket\",\"Resource\":\"*\"}]}",
            Tags = ResourceTags(),
        });
        context.Set("IamPolicyArn", response.Policy.Arn);

        // The counter tests attach this group's own policy to a role of its
        // own, so AttachmentCount moves for a customer managed policy rather
        // than for the AWS managed one iam-roles attaches.
        var roleName = $"{context.RunId}-iam-policy-role";
        await clients.IAM().CreateRoleAsync(new CreateRoleRequest
        {
            RoleName = roleName,
            AssumeRolePolicyDocument = @"{""Version"":""2012-10-17"",""Statement"":[{""Effect"":""Allow"",""Principal"":{""Service"":""lambda.amazonaws.com""},""Action"":""sts:AssumeRole""}]}",
        });
        context.Set("IamPolicyRoleName", roleName);
    }

    private async Task CreatePolicyAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-create-policy";
        var response = await clients.IAM().CreatePolicyAsync(new CreatePolicyRequest
        {
            PolicyName = name,
            PolicyDocument = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:ListBucket\",\"Resource\":\"*\"}]}",
        });
        var arn = response.Policy.Arn;
        Assertions.NotBlank(arn, "CreatePolicy: Arn");
        try
        {
            var list = await clients.IAM().ListPoliciesAsync(new ListPoliciesRequest());
            Assertions.True(list.Policies.Any(p => p.Arn == arn), $"CreatePolicy: policy {arn} not found in ListPolicies (runId={context.RunId})");
        }
        finally
        {
            try { await clients.IAM().DeletePolicyAsync(new DeletePolicyRequest { PolicyArn = arn }); } catch { }
        }
    }

    private async Task GetPolicyAsync(TestContext context)
    {
        var arn = RequireString(context, "IamPolicyArn");
        var response = await clients.IAM().GetPolicyAsync(new GetPolicyRequest { PolicyArn = arn });
        Assertions.NotBlank(response.Policy.PolicyName, "GetPolicy: PolicyName");
    }

    private async Task ListPoliciesAsync(TestContext context)
    {
        var arn = RequireString(context, "IamPolicyArn");
        var response = await clients.IAM().ListPoliciesAsync(new ListPoliciesRequest());
        Assertions.True(response.Policies.Any(p => p.Arn == arn), $"ListPolicies: policy {arn} not found (runId={context.RunId})");
    }

    /// <summary>The same refusal on the identity-policy writer.</summary>
    private async Task CreatePolicyMalformedDocumentAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-policy-malformed";
        try
        {
            await clients.IAM().CreatePolicyAsync(new CreatePolicyRequest
            {
                PolicyName = name,
                PolicyDocument = MalformedPolicy,
            });
        }
        catch (AmazonIdentityManagementServiceException e)
        {
            AssertMalformedPolicyDocument("CreatePolicyMalformedDocument", e);
            return;
        }

        throw new InvalidOperationException(
            $"CreatePolicyMalformedDocument: expected MalformedPolicyDocument, call succeeded (runId={context.RunId})");
    }

    /// <summary>Tags given to CreatePolicy come back on the policy (API_Policy.html).</summary>
    private async Task GetPolicyReturnsTagsAsync(TestContext context)
    {
        var arn = RequireString(context, "IamPolicyArn");
        var response = await clients.IAM().GetPolicyAsync(new GetPolicyRequest { PolicyArn = arn });
        AssertResourceTags("GetPolicyReturnsTags", response.Policy.Tags);
    }

    /// <summary>Reads AttachmentCount back through GetPolicy.</summary>
    private async Task<int?> AttachmentCountAsync(TestContext context)
    {
        var arn = RequireString(context, "IamPolicyArn");
        var response = await clients.IAM().GetPolicyAsync(new GetPolicyRequest { PolicyArn = arn });
        return response.Policy.AttachmentCount;
    }

    /// <summary>
    /// AttachmentCount moves 0 to 1. "The number of entities (users, groups,
    /// and roles) that the policy is attached to" (IAM API Reference,
    /// API_Policy.html) is what a cleanup script reads before deleting a
    /// policy, so a stuck 0 deletes something in use.
    /// </summary>
    private async Task GetPolicyAttachmentCountAfterAttachAsync(TestContext context)
    {
        const string op = "GetPolicyAttachmentCountAfterAttach";
        var arn = RequireString(context, "IamPolicyArn");
        var roleName = RequireString(context, "IamPolicyRoleName");
        Assertions.Equal<int?>(0, await AttachmentCountAsync(context), $"{op}: AttachmentCount before the attach");
        await clients.IAM().AttachRolePolicyAsync(new AttachRolePolicyRequest { RoleName = roleName, PolicyArn = arn });
        Assertions.Equal<int?>(1, await AttachmentCountAsync(context), $"{op}: AttachmentCount after attaching to one role");
    }

    /// <summary>The counter moves back to 0, which a never-decremented one would fail.</summary>
    private async Task GetPolicyAttachmentCountAfterDetachAsync(TestContext context)
    {
        const string op = "GetPolicyAttachmentCountAfterDetach";
        var arn = RequireString(context, "IamPolicyArn");
        var roleName = RequireString(context, "IamPolicyRoleName");
        await clients.IAM().DetachRolePolicyAsync(new DetachRolePolicyRequest { RoleName = roleName, PolicyArn = arn });
        Assertions.Equal<int?>(0, await AttachmentCountAsync(context), $"{op}: AttachmentCount after the detach");
    }

    private async Task DeletePolicyAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-del-policy";
        var create = await clients.IAM().CreatePolicyAsync(new CreatePolicyRequest
        {
            PolicyName = name,
            PolicyDocument = "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:ListBucket\",\"Resource\":\"*\"}]}",
        });
        var arn = create.Policy.Arn;
        await clients.IAM().DeletePolicyAsync(new DeletePolicyRequest { PolicyArn = arn });
        var list = await clients.IAM().ListPoliciesAsync(new ListPoliciesRequest());
        Assertions.False(list.Policies.Any(p => p.Arn == arn), $"DeletePolicy: policy {arn} still present after deletion (runId={context.RunId})");
    }

    private async Task TeardownPoliciesAsync(TestContext context)
    {
        var arn = context.GetString("IamPolicyArn");
        var roleName = context.GetString("IamPolicyRoleName");
        if (!string.IsNullOrWhiteSpace(roleName))
        {
            if (!string.IsNullOrWhiteSpace(arn))
            {
                try { await clients.IAM().DetachRolePolicyAsync(new DetachRolePolicyRequest { RoleName = roleName, PolicyArn = arn }); } catch { }
            }

            try { await clients.IAM().DeleteRoleAsync(new DeleteRoleRequest { RoleName = roleName }); } catch { }
        }

        if (!string.IsNullOrWhiteSpace(arn))
        {
            try { await clients.IAM().DeletePolicyAsync(new DeletePolicyRequest { PolicyArn = arn }); } catch { }
        }
    }

    // ── iam-groups ──

    private async Task SetupGroupsAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-group";
        await clients.IAM().CreateGroupAsync(new CreateGroupRequest { GroupName = name });
        context.Set("IamGroupName", name);
    }

    private async Task CreateGroupAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-create-group";
        var response = await clients.IAM().CreateGroupAsync(new CreateGroupRequest { GroupName = name });
        Assertions.NotBlank(response.Group.GroupId, "CreateGroup: GroupId");
        try
        {
            var get = await clients.IAM().GetGroupAsync(new GetGroupRequest { GroupName = name });
            Assertions.Equal(name, get.Group.GroupName, "CreateGroup: GroupName mismatch");
        }
        finally
        {
            try { await clients.IAM().DeleteGroupAsync(new DeleteGroupRequest { GroupName = name }); } catch { }
        }
    }

    private async Task AddUserToGroupAsync(TestContext context)
    {
        var groupName = RequireString(context, "IamGroupName");
        var userName = $"{context.RunId}-iam-temp-user";
        await clients.IAM().CreateUserAsync(new CreateUserRequest { UserName = userName });
        context.Set("IamTempUserName", userName);
        await clients.IAM().AddUserToGroupAsync(new AddUserToGroupRequest { GroupName = groupName, UserName = userName });
    }

    private async Task ListGroupsForUserAsync(TestContext context)
    {
        var groupName = RequireString(context, "IamGroupName");
        var userName = RequireString(context, "IamTempUserName");
        var response = await clients.IAM().ListGroupsForUserAsync(new ListGroupsForUserRequest { UserName = userName });
        Assertions.True(response.Groups.Any(g => g.GroupName == groupName), $"ListGroupsForUser: group {groupName} not found for user (runId={context.RunId})");
    }

    private async Task RemoveUserFromGroupAsync(TestContext context)
    {
        var groupName = RequireString(context, "IamGroupName");
        var userName = RequireString(context, "IamTempUserName");
        await clients.IAM().RemoveUserFromGroupAsync(new RemoveUserFromGroupRequest { GroupName = groupName, UserName = userName });
    }

    private async Task GetGroupAsync(TestContext context)
    {
        var groupName = RequireString(context, "IamGroupName");
        var response = await clients.IAM().GetGroupAsync(new GetGroupRequest { GroupName = groupName });
        Assertions.Equal(groupName, response.Group.GroupName, "GetGroup: GroupName mismatch");
    }

    private async Task DeleteGroupAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-del-group";
        await clients.IAM().CreateGroupAsync(new CreateGroupRequest { GroupName = name });
        await clients.IAM().DeleteGroupAsync(new DeleteGroupRequest { GroupName = name });
        try
        {
            await clients.IAM().GetGroupAsync(new GetGroupRequest { GroupName = name });
            throw new InvalidOperationException($"DeleteGroup: group {name} still present after deletion (runId={context.RunId})");
        }
        catch (NoSuchEntityException)
        {
            // expected
        }
    }

    private async Task TeardownGroupsAsync(TestContext context)
    {
        var groupName = context.GetString("IamGroupName");
        if (string.IsNullOrWhiteSpace(groupName))
        {
            return;
        }

        var userName = context.GetString("IamTempUserName");
        if (!string.IsNullOrWhiteSpace(userName))
        {
            try { await clients.IAM().RemoveUserFromGroupAsync(new RemoveUserFromGroupRequest { GroupName = groupName, UserName = userName }); } catch { }
            try { await clients.IAM().DeleteUserAsync(new DeleteUserRequest { UserName = userName }); } catch { }
        }

        try { await clients.IAM().DeleteGroupAsync(new DeleteGroupRequest { GroupName = groupName }); } catch { }
    }

    private static string RequireString(TestContext context, string key)
    {
        return context.GetString(key) ?? throw new InvalidOperationException($"{key} not set");
    }

    // ── iam-simulate ──

    // SimPolicy is the identity policy the simulate group evaluates: read one
    // run-scoped prefix, nothing else.
    private static string SimPolicy(TestContext context) =>
        $@"{{""Version"":""2012-10-17"",""Statement"":[{{""Effect"":""Allow"",""Action"":""s3:GetObject"",""Resource"":""arn:aws:s3:::{context.RunId}-sim/*""}}]}}";

    private static string SimResource(TestContext context) =>
        $"arn:aws:s3:::{context.RunId}-sim/report.csv";

    private async Task SetupSimulateAsync(TestContext context)
    {
        var name = $"{context.RunId}-iam-sim-user";
        await clients.IAM().CreateUserAsync(new CreateUserRequest { UserName = name });
        await clients.IAM().PutUserPolicyAsync(new PutUserPolicyRequest
        {
            UserName = name,
            PolicyName = "sim-allow-read",
            PolicyDocument = SimPolicy(context),
        });
        context.Set("IamSimUserName", name);
    }

    private async Task TeardownSimulateAsync(TestContext context)
    {
        var name = context.GetString("IamSimUserName");
        if (string.IsNullOrEmpty(name)) return;
        try { await clients.IAM().DeleteUserPolicyAsync(new DeleteUserPolicyRequest { UserName = name, PolicyName = "sim-allow-read" }); } catch { }
        try { await clients.IAM().DeleteUserAsync(new DeleteUserRequest { UserName = name }); } catch { }
    }

    private async Task SimulateCustomPolicyAllowedAsync(TestContext context)
    {
        var response = await clients.IAM().SimulateCustomPolicyAsync(new SimulateCustomPolicyRequest
        {
            PolicyInputList = [SimPolicy(context)],
            ActionNames = ["s3:GetObject"],
            ResourceArns = [SimResource(context)],
        });
        Assertions.True(response.EvaluationResults.Count > 0, "SimulateCustomPolicy: no EvaluationResults");
        Assertions.Equal("allowed", response.EvaluationResults[0].EvalDecision.Value, "SimulateCustomPolicy: EvalDecision");
        Assertions.Equal("s3:GetObject", response.EvaluationResults[0].EvalActionName, "SimulateCustomPolicy: EvalActionName");
    }

    private async Task SimulateCustomPolicyImplicitDenyAsync(TestContext context)
    {
        var response = await clients.IAM().SimulateCustomPolicyAsync(new SimulateCustomPolicyRequest
        {
            PolicyInputList = [SimPolicy(context)],
            ActionNames = ["s3:PutObject"],
            ResourceArns = [SimResource(context)],
        });
        Assertions.True(response.EvaluationResults.Count > 0, "SimulateCustomPolicy: no EvaluationResults");
        Assertions.Equal("implicitDeny", response.EvaluationResults[0].EvalDecision.Value, "SimulateCustomPolicy: EvalDecision");
    }

    private async Task SimulateCustomPolicyExplicitDenyAsync(TestContext context)
    {
        const string doc = @"{""Version"":""2012-10-17"",""Statement"":[" +
            @"{""Effect"":""Allow"",""Action"":""s3:*"",""Resource"":""*""}," +
            @"{""Effect"":""Deny"",""Action"":""s3:DeleteObject"",""Resource"":""*""}]}";
        var response = await clients.IAM().SimulateCustomPolicyAsync(new SimulateCustomPolicyRequest
        {
            PolicyInputList = [doc],
            ActionNames = ["s3:DeleteObject"],
            ResourceArns = [SimResource(context)],
        });
        Assertions.True(response.EvaluationResults.Count > 0, "SimulateCustomPolicy: no EvaluationResults");
        Assertions.Equal("explicitDeny", response.EvaluationResults[0].EvalDecision.Value, "SimulateCustomPolicy: EvalDecision");
    }

    private async Task SimulatePrincipalPolicyAllowedAsync(TestContext context)
    {
        var userName = RequireString(context, "IamSimUserName");
        var response = await clients.IAM().SimulatePrincipalPolicyAsync(new SimulatePrincipalPolicyRequest
        {
            PolicySourceArn = $"arn:aws:iam::000000000000:user/{userName}",
            ActionNames = ["s3:GetObject"],
            ResourceArns = [SimResource(context)],
        });
        Assertions.True(response.EvaluationResults.Count > 0, "SimulatePrincipalPolicy: no EvaluationResults");
        Assertions.Equal("allowed", response.EvaluationResults[0].EvalDecision.Value, "SimulatePrincipalPolicy: EvalDecision");
        Assertions.True(
            response.EvaluationResults[0].MatchedStatements.Any(s => s.SourcePolicyId == "sim-allow-read"),
            "SimulatePrincipalPolicy: MatchedStatements missing sim-allow-read");
    }

    private async Task SimulatePrincipalPolicyImplicitDenyAsync(TestContext context)
    {
        var userName = RequireString(context, "IamSimUserName");
        var response = await clients.IAM().SimulatePrincipalPolicyAsync(new SimulatePrincipalPolicyRequest
        {
            PolicySourceArn = $"arn:aws:iam::000000000000:user/{userName}",
            ActionNames = ["s3:DeleteObject"],
            ResourceArns = [SimResource(context)],
        });
        Assertions.True(response.EvaluationResults.Count > 0, "SimulatePrincipalPolicy: no EvaluationResults");
        Assertions.Equal("implicitDeny", response.EvaluationResults[0].EvalDecision.Value, "SimulatePrincipalPolicy: EvalDecision");
    }

}
