using Amazon.IdentityManagement.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class IamGroup(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        // iam-users
        ["CreateUser"] = CreateUserAsync,
        ["GetUser"] = GetUserAsync,
        ["ListUsers"] = ListUsersAsync,
        ["CreateAccessKey"] = CreateAccessKeyAsync,
        ["DeleteAccessKey"] = DeleteAccessKeyAsync,
        ["PutUserPolicy"] = PutUserPolicyAsync,
        ["GetUserPolicy"] = GetUserPolicyAsync,
        ["DeleteUserPolicy"] = DeleteUserPolicyAsync,
        ["UpdateUser"] = UpdateUserAsync,
        ["ListAccessKeys"] = ListAccessKeysAsync,
        ["DeleteUser"] = DeleteUserAsync,
        // iam-roles
        ["CreateRole"] = CreateRoleAsync,
        ["GetRole"] = GetRoleAsync,
        ["ListRoles"] = ListRolesAsync,
        ["AttachRolePolicy"] = AttachRolePolicyAsync,
        ["ListAttachedRolePolicies"] = ListAttachedRolePoliciesAsync,
        ["DetachRolePolicy"] = DetachRolePolicyAsync,
        ["CreateInstanceProfile"] = CreateInstanceProfileAsync,
        ["AddRoleToInstanceProfile"] = AddRoleToInstanceProfileAsync,
        ["GetInstanceProfile"] = GetInstanceProfileAsync,
        ["PutRolePolicy"] = PutRolePolicyAsync,
        ["GetRolePolicy"] = GetRolePolicyAsync,
        ["ListRolePolicies"] = ListRolePoliciesAsync,
        ["DeleteRolePolicy"] = DeleteRolePolicyAsync,
        ["DeleteRole"] = DeleteRoleAsync,
        // iam-policies
        ["CreatePolicy"] = CreatePolicyAsync,
        ["GetPolicy"] = GetPolicyAsync,
        ["ListPolicies"] = ListPoliciesAsync,
        ["DeletePolicy"] = DeletePolicyAsync,
        // iam-groups
        ["CreateGroup"] = CreateGroupAsync,
        ["AddUserToGroup"] = AddUserToGroupAsync,
        ["ListGroupsForUser"] = ListGroupsForUserAsync,
        ["RemoveUserFromGroup"] = RemoveUserFromGroupAsync,
        ["GetGroup"] = GetGroupAsync,
        ["DeleteGroup"] = DeleteGroupAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["iam-users"] = SetupUsersAsync,
        ["iam-roles"] = SetupRolesAsync,
        ["iam-policies"] = SetupPoliciesAsync,
        ["iam-groups"] = SetupGroupsAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["iam-users"] = TeardownUsersAsync,
        ["iam-roles"] = TeardownRolesAsync,
        ["iam-policies"] = TeardownPoliciesAsync,
        ["iam-groups"] = TeardownGroupsAsync,
    };

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
        Assertions.True(response.PolicyDocument.Contains("s3:GetObject"), "GetUserPolicy: PolicyDocument missing s3:GetObject");
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
        Assertions.True(response.PolicyDocument.Contains("logs:*"), "GetRolePolicy: PolicyDocument missing logs:*");
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
        });
        context.Set("IamPolicyArn", response.Policy.Arn);
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
}
