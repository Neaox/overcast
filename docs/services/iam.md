# IAM — Identity and Access Management

> AWS docs: [IAM API Reference](https://docs.aws.amazon.com/IAM/latest/APIReference/Welcome.html)

IAM uses the AWS Query protocol (`POST /` with form-encoded body). Actions are dispatched by
the `Action` parameter. Overcast emulates IAM resource management (users, roles, groups,
policies, instance profiles) for CDK/IaC compatibility — **credentials are accepted but not
validated**.

> [!CAUTION]
> **Emulation tier: Inert** — IAM resources are created and stored, but policies are
> **never enforced** and permissions are **never checked**. Every API call succeeds
> regardless of attached policies. `SimulatePrincipalPolicy` always returns `allowed`.
> Do not use Overcast to test IAM authorization logic.

---

## Notes

- **No policy versions.** `CreatePolicy` stores the document but there is no `CreatePolicyVersion`
  or version history.
- **Event bus integration.** User, role, policy and group lifecycle events are published to the
  internal event bus for topology/UI updates.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category               | ✅ Supported |
| ---------------------- | ------------ |
| Users                  | 5            |
| Access keys            | 3            |
| User inline policies   | 4            |
| User managed policies  | 3            |
| User tagging           | 3            |
| Roles                  | 6            |
| Role inline policies   | 4            |
| Role managed policies  | 3            |
| Role tagging           | 3            |
| Instance profiles      | 7            |
| Managed policies       | 4            |
| Groups                 | 7            |
| Group inline policies  | 4            |
| Group managed policies | 3            |
| Policy simulation      | 1            |
| Account details        | 1            |

---

## Endpoints

### Users

| Operation    | Status       | Notes | AWS Docs                                                                        |
| ------------ | ------------ | ----- | ------------------------------------------------------------------------------- |
| `CreateUser` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateUser.html) |
| `GetUser`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetUser.html)    |
| `ListUsers`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListUsers.html)  |
| `UpdateUser` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UpdateUser.html) |
| `DeleteUser` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteUser.html) |

### Access keys

| Operation         | Status       | Notes                                | AWS Docs                                                                             |
| ----------------- | ------------ | ------------------------------------ | ------------------------------------------------------------------------------------ |
| `CreateAccessKey` | ✅ Supported | Generates AKIA-prefixed key + secret | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateAccessKey.html) |
| `ListAccessKeys`  | ✅ Supported |                                      | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListAccessKeys.html)  |
| `DeleteAccessKey` | ✅ Supported |                                      | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteAccessKey.html) |

### User inline policies

| Operation          | Status       | Notes | AWS Docs                                                                              |
| ------------------ | ------------ | ----- | ------------------------------------------------------------------------------------- |
| `PutUserPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_PutUserPolicy.html)    |
| `GetUserPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetUserPolicy.html)    |
| `DeleteUserPolicy` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteUserPolicy.html) |
| `ListUserPolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListUserPolicies.html) |

### User managed policies

| Operation                  | Status       | Notes | AWS Docs                                                                                      |
| -------------------------- | ------------ | ----- | --------------------------------------------------------------------------------------------- |
| `AttachUserPolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AttachUserPolicy.html)         |
| `DetachUserPolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DetachUserPolicy.html)         |
| `ListAttachedUserPolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListAttachedUserPolicies.html) |

### User tagging

| Operation      | Status       | Notes | AWS Docs                                                                          |
| -------------- | ------------ | ----- | --------------------------------------------------------------------------------- |
| `TagUser`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_TagUser.html)      |
| `UntagUser`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UntagUser.html)    |
| `ListUserTags` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListUserTags.html) |

### Roles

| Operation                 | Status       | Notes | AWS Docs                                                                                     |
| ------------------------- | ------------ | ----- | -------------------------------------------------------------------------------------------- |
| `CreateRole`              | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateRole.html)              |
| `GetRole`                 | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetRole.html)                 |
| `ListRoles`               | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListRoles.html)               |
| `DeleteRole`              | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteRole.html)              |
| `UpdateAssumeRolePolicy`  | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UpdateAssumeRolePolicy.html)  |
| `CreateServiceLinkedRole` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateServiceLinkedRole.html) |

### Role inline policies

| Operation          | Status       | Notes | AWS Docs                                                                              |
| ------------------ | ------------ | ----- | ------------------------------------------------------------------------------------- |
| `PutRolePolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_PutRolePolicy.html)    |
| `GetRolePolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetRolePolicy.html)    |
| `ListRolePolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListRolePolicies.html) |
| `DeleteRolePolicy` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteRolePolicy.html) |

### Role managed policies

| Operation                  | Status       | Notes | AWS Docs                                                                                      |
| -------------------------- | ------------ | ----- | --------------------------------------------------------------------------------------------- |
| `AttachRolePolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AttachRolePolicy.html)         |
| `DetachRolePolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DetachRolePolicy.html)         |
| `ListAttachedRolePolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListAttachedRolePolicies.html) |

### Role tagging

| Operation      | Status       | Notes | AWS Docs                                                                          |
| -------------- | ------------ | ----- | --------------------------------------------------------------------------------- |
| `TagRole`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_TagRole.html)      |
| `UntagRole`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UntagRole.html)    |
| `ListRoleTags` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListRoleTags.html) |

### Instance profiles

| Operation                       | Status       | Notes | AWS Docs                                                                                           |
| ------------------------------- | ------------ | ----- | -------------------------------------------------------------------------------------------------- |
| `CreateInstanceProfile`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateInstanceProfile.html)         |
| `GetInstanceProfile`            | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetInstanceProfile.html)            |
| `DeleteInstanceProfile`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteInstanceProfile.html)         |
| `AddRoleToInstanceProfile`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AddRoleToInstanceProfile.html)      |
| `RemoveRoleFromInstanceProfile` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_RemoveRoleFromInstanceProfile.html) |
| `ListInstanceProfiles`          | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListInstanceProfiles.html)          |
| `ListInstanceProfilesForRole`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListInstanceProfilesForRole.html)   |

### Managed policies

| Operation      | Status       | Notes | AWS Docs                                                                          |
| -------------- | ------------ | ----- | --------------------------------------------------------------------------------- |
| `CreatePolicy` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreatePolicy.html) |
| `GetPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetPolicy.html)    |
| `ListPolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListPolicies.html) |
| `DeletePolicy` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeletePolicy.html) |

### Groups

| Operation             | Status       | Notes | AWS Docs                                                                                 |
| --------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------- |
| `CreateGroup`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateGroup.html)         |
| `GetGroup`            | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetGroup.html)            |
| `DeleteGroup`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteGroup.html)         |
| `ListGroups`          | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListGroups.html)          |
| `AddUserToGroup`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AddUserToGroup.html)      |
| `RemoveUserFromGroup` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_RemoveUserFromGroup.html) |
| `ListGroupsForUser`   | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListGroupsForUser.html)   |

### Group inline policies

| Operation           | Status       | Notes | AWS Docs                                                                               |
| ------------------- | ------------ | ----- | -------------------------------------------------------------------------------------- |
| `PutGroupPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_PutGroupPolicy.html)    |
| `GetGroupPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetGroupPolicy.html)    |
| `DeleteGroupPolicy` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteGroupPolicy.html) |
| `ListGroupPolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListGroupPolicies.html) |

### Group managed policies

| Operation                   | Status       | Notes | AWS Docs                                                                                       |
| --------------------------- | ------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `AttachGroupPolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AttachGroupPolicy.html)         |
| `DetachGroupPolicy`         | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DetachGroupPolicy.html)         |
| `ListAttachedGroupPolicies` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListAttachedGroupPolicies.html) |

### Policy simulation

| Operation                 | Status       | Notes                                          | AWS Docs                                                                                     |
| ------------------------- | ------------ | ---------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `SimulatePrincipalPolicy` | ✅ Supported | Always returns allowed — no enforcement engine | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_SimulatePrincipalPolicy.html) |

### Account details

| Operation                        | Status       | Notes                                                              | AWS Docs                                                                                            |
| -------------------------------- | ------------ | ------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `GetAccountAuthorizationDetails` | ✅ Supported | Returns all users, groups, roles, and managed policies in one call | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetAccountAuthorizationDetails.html) |

<!-- END overcast:capabilities -->
