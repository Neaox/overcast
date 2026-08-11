---
title: "IAM — Identity and Access Management"
description: "IAM uses the AWS Query protocol (POST / with form-encoded body). Actions are dispatched by the Action parameter. Overcast emulates IAM resource management (users, roles, groups,..."
section: "Service Reference"
tags:
  - access
  - docs
  - iam
  - identity
  - management
  - services
---

# IAM — Identity and Access Management

> AWS docs: [IAM API Reference](https://docs.aws.amazon.com/IAM/latest/APIReference/Welcome.html)

IAM uses the AWS Query protocol (`POST /` with form-encoded body). Actions are dispatched by
the `Action` parameter. Overcast emulates IAM resource management (users, roles, groups,
policies, instance profiles) for CDK/IaC compatibility — **credentials are accepted but not
validated**.

> [!CAUTION]
> **Policies are not enforced by default.** IAM resources are created and stored, and every
> API call succeeds regardless of attached policies unless you opt in to enforcement with
> `OVERCAST_ENFORCE_IAM` (see below). Overcast is not a security boundary: credentials are
> accepted but never verified, and the evaluator covers a subset of the IAM policy language.

---

## Notes

- **No policy versions.** `CreatePolicy` stores the document but there is no `CreatePolicyVersion`
  or version history.
- **`GetGroup` returns the group's members.** Membership recorded by `AddUserToGroup` /
  `RemoveUserFromGroup` is resolved into the response's `Users` collection, paginated with
  `Marker` / `MaxItems` (AWS's documented default of 100 and cap of 1000). A membership entry
  whose user record has since gone, or cannot be decoded, is skipped rather than failing the
  call — one bad stored record cannot make a group unreadable. A `Marker` that does not decode
  is rejected with `InvalidInput` instead of silently restarting at the first page.
- **Event bus integration.** User, role, policy and group lifecycle events are published to the
  internal event bus for topology/UI updates.

## Deletes enforce dependencies

`DeleteUser`, `DeleteRole`, `DeleteGroup` and `DeletePolicy` refuse with AWS's `DeleteConflict`
(HTTP 409) while the entity still has dependencies, and the message names what to clear — the
same behaviour Terraform, CDK, `aws-nuke` and eksctl already expect from real IAM.

| Delete         | Refused while                     | Message                                                            |
| -------------- | --------------------------------- | ------------------------------------------------------------------ |
| `DeleteUser`   | access keys exist                 | `Cannot delete entity, must delete access keys first.`             |
|                | inline policies exist             | `Cannot delete entity, must delete policies first.`                |
|                | managed policies are attached     | `Cannot delete entity, must detach all policies first.`            |
|                | the user is in a group            | `Cannot delete entity, must remove users from group first.`        |
| `DeleteRole`   | the role is in an instance profile | `Cannot delete entity, must remove roles from instance profile first.` |
|                | inline policies exist             | `Cannot delete entity, must delete policies first.`                |
|                | managed policies are attached     | `Cannot delete entity, must detach all policies first.`            |
| `DeleteGroup`  | the group has members             | `Cannot delete entity, must remove users from group first.`        |
|                | inline policies exist             | `Cannot delete entity, must delete policies first.`                |
|                | managed policies are attached     | `Cannot delete entity, must detach all policies first.`            |
| `DeletePolicy` | attached to any user, role or group, or used as one of their permissions boundaries | `Cannot delete a policy attached to entities.` |

A non-existent entity is still `NoSuchEntity` (404): existence is checked before dependencies.
When several dependencies block at once the checks run in the order listed above, which is the
order AWS's own API Reference lists the prerequisites in; AWS does not document which one wins,
so clearing them top to bottom is what a caller should expect.

Overcast does not model login profiles, signing certificates, SSH keys, Git credentials or MFA
devices, so the AWS conflicts for those cannot arise here.

> [!NOTE]
> Local teardown scripts that used to delete an IAM entity without unwinding it first will now
> get a 409. Remove the dependency through the modeled API — `DeleteAccessKey`,
> `DeleteUserPolicy`, `DetachUserPolicy`, `RemoveUserFromGroup`,
> `RemoveRoleFromInstanceProfile`, `DeleteRolePolicy`, `DetachRolePolicy`,
> `DeleteGroupPolicy`, `DetachGroupPolicy` — exactly as you would against AWS. CloudFormation
> stack teardown handles the dependencies a stack owns itself: `AWS::IAM::Policy` removes its
> inline document from the entities it named, and reverse dependency order puts instance
> profiles before their roles.

A `DeleteConflict` a stack cannot clear for itself — something outside the stack attached a
policy, minted an access key, or added the user to a group — fails the stack. `DeleteStack`
reports `DELETE_FAILED` with IAM's own message on the resource's event and status reason, and
leaves the entity standing, as real CloudFormation does. Clear the dependency and delete the
stack again. See [CloudFormation § Teardown failure](./cloudformation.md).

## Policy simulation

`SimulateCustomPolicy` and `SimulatePrincipalPolicy` run a real evaluation of the IAM policy
language and return AWS's decision vocabulary — `allowed`, `explicitDeny`, `implicitDeny` —
with `MatchedStatements` naming the policy and statement that decided it, and
`MissingContextValues` naming condition keys the call did not supply. Simulation reads
nothing else and changes nothing: it is the "what would happen" view, and it is available
whether or not enforcement is switched on.

Each `MatchedStatements` entry carries `SourcePolicyId`/`SourcePolicyType` plus AWS's
`StartPosition`/`EndPosition` (`Line`/`Column`), which point at the deciding statement's
opening `{` and closing `}` in the exact document text supplied on the call (a
`PolicyInputList` entry, an inline/managed policy document, or `ResourcePolicy`). This is
what lets a caller tell two statements apart when they come from the *same* document — for
example an `Allow` and a `Deny` in one policy — which `SourcePolicyId`/`SourcePolicyType`
alone cannot do. The positions are Overcast's own byte-accurate computation against the
document text it was given; they are not copied from any upstream source, and a caller
should not expect them to match what real AWS would report for the same document down to
the byte.

What the evaluator covers:

- `Effect`, `Action`/`NotAction`, `Resource`/`NotResource`, with `*` and `?` wildcards.
  Actions match case-insensitively, as on AWS; resource ARNs match case-sensitively.
- Explicit deny wins, then allow, otherwise the default implicit deny.
- `Condition` operators: the `String*`, `Numeric*`, `Date*`, `Bool`, `IpAddress`/`NotIpAddress`,
  `Arn*` and `Null` families, including the `…IfExists` suffix.
- Policy variables (`${aws:username}`, `${aws:userid}`, …) in resources and condition values.
- Resource-based policies passed as `ResourcePolicy`, including `Principal`/`NotPrincipal`
  matching. Within the single account Overcast emulates, an allow in either the identity
  policies or the resource policy is sufficient, and a deny in either is final.
- Permissions boundaries — both the one attached to the simulated principal and one supplied
  as `PermissionsBoundaryPolicyInputList` — reported through
  `PermissionsBoundaryDecisionDetail`. See below.

What it does not cover, and says so rather than guessing: a condition operator or principal
type it does not implement makes the call fail with AWS's `PolicyEvaluation` error naming the
construct, instead of resolving to an allow or a deny. Service control policies, session
policies, and the `ForAllValues`/`ForAnyValue` set operators are not implemented. A policy
document that cannot be parsed is rejected with `InvalidInput`.

Permissions boundaries granted through a `ResourcePolicy` are **not** exempted: on AWS a
resource-based policy that names an IAM user principal directly is allowed to bypass that
user's boundary, and Overcast applies the boundary to the combined identity/resource decision
instead. That divergence only shows up when a simulation supplies a `ResourcePolicy` *and* the
principal carries a boundary.

## Permissions boundaries

`PutUserPermissionsBoundary` / `PutRolePermissionsBoundary` attach a managed policy as a user's
or role's permissions boundary, and `CreateUser` / `CreateRole` accept one directly through
their `PermissionsBoundary` parameter (which is what `AWS::IAM::User` passes from a template).
`GetUser`, `GetRole`, `ListUsers`, `ListRoles` and `GetAccountAuthorizationDetails` report it as
AWS's `AttachedPermissionsBoundary`. A boundary naming a policy that does not exist is refused
with `NoSuchEntity`, and `DeletePolicy` refuses with `DeleteConflict` while a policy is still
bounding an entity.

A boundary grants nothing on its own: it caps what the entity's identity policies can grant, so
the effective permissions are the **intersection** of the two, and an explicit deny in either is
final. Both `SimulatePrincipalPolicy` and opt-in request-time enforcement read the stored
boundary, so a boundary attached out of band takes effect on the very next call — attaching,
replacing or removing one invalidates the enforcement middleware's compiled-policy cache, as any
other policy change does.

Supplying `PermissionsBoundaryPolicyInputList` to `SimulatePrincipalPolicy` uses that boundary
*instead of* the stored one: AWS allows only one boundary per simulation, and asking "what would
this boundary do" is the reason to supply it.

A boundary that is attached but cannot be read — its managed policy has been deleted out of
band, its stored record does not decode, or its document is not a valid policy — allows nothing.
Reading it as absent would grant exactly the permissions it was attached to withhold, and
failing the whole call would let one corrupt record break an otherwise healthy principal. The
reason is logged at warn level.

## Request-time enforcement (opt-in)

Set `OVERCAST_ENFORCE_IAM=true` to have every request evaluated against the calling
principal's policies before it reaches the service handler. **It is off by default, and with
it off the evaluator reads nothing and decides nothing** — behaviour is exactly as it was.

When it is on:

- The caller is resolved from the SigV4 access key to an IAM user (its inline, attached and
  group policies) or to a role assumed through STS (its inline and attached policies), plus
  that entity's permissions boundary if it has one.
- A request the policies do not allow is refused with the calling service's own
  `AccessDenied`-shaped error, in that service's wire format.
- The action evaluated is `<prefix>:<Operation>`, where the operation is the one the request
  invokes and the prefix is **the IAM action prefix AWS uses for that service** — so write
  policies with the names the AWS documentation gives. Most services are called the same
  thing throughout, but eight are not: MSK authorizes as `kafka:`, Step Functions as
  `states:`, EFS as `elasticfilesystem:`, OpenSearch as `es:`, ELBv2 as
  `elasticloadbalancing:`, Service Catalog AppRegistry as `servicecatalog:`, Cognito user
  pools as `cognito-idp:`, and WAF as `wafv2:`.
- Enforcement is **fail-closed**: an unsigned request, a policy that cannot be parsed, or a
  construct the evaluator does not implement all deny. The reason is logged at debug level.
- The one exception is a request whose operation cannot be named, which is **not** gated. S3
  reaches this routinely, because its sub-resource operations (`?tagging`, `?restore`,
  `?legal-hold`, …) are identified by query parameters rather than by path. Denying them
  would break ordinary S3 traffic the moment enforcement was switched on. The gap is logged
  at debug level rather than passing silently.
- Resource-based policies (S3 bucket policies, Lambda/SQS/SNS policies) are **not** consulted
  at request time yet — only identity policies are. The simulator accepts a resource policy
  explicitly, which is the way to test one today.

Enforcement decides only what the emulator's own evaluator can see. It is a development aid
for catching missing permissions early, not a security control.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category                 | ✅ Supported |
| ------------------------ | ------------ |
| Users                    | 5            |
| Access keys              | 3            |
| User inline policies     | 4            |
| User managed policies    | 3            |
| Permissions boundaries   | 4            |
| User tagging             | 3            |
| Roles                    | 6            |
| Role inline policies     | 4            |
| Role managed policies    | 3            |
| Role tagging             | 3            |
| Managed policy tagging   | 3            |
| Instance profile tagging | 3            |
| Instance profiles        | 7            |
| Managed policies         | 4            |
| Groups                   | 7            |
| Group inline policies    | 4            |
| Group managed policies   | 3            |
| Policy simulation        | 2            |
| Account details          | 1            |

---

## Endpoints

### Users

| Operation    | Status       | Notes                                                                                            | AWS Docs                                                                        |
| ------------ | ------------ | ------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `CreateUser` | ✅ Supported | Inline `Tags` applied at creation                                                                | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateUser.html) |
| `GetUser`    | ✅ Supported |                                                                                                  | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetUser.html)    |
| `ListUsers`  | ✅ Supported |                                                                                                  | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListUsers.html)  |
| `UpdateUser` | ✅ Supported |                                                                                                  | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UpdateUser.html) |
| `DeleteUser` | ✅ Supported | DeleteConflict (409) while access keys, inline or attached policies, or group memberships remain | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteUser.html) |

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

### Permissions boundaries

| Operation                       | Status       | Notes                                                                                                   | AWS Docs                                                                                           |
| ------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `PutUserPermissionsBoundary`    | ✅ Supported | Applied by SimulatePrincipalPolicy and by opt-in enforcement; NoSuchEntity if the policy does not exist | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_PutUserPermissionsBoundary.html)    |
| `DeleteUserPermissionsBoundary` | ✅ Supported |                                                                                                         | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteUserPermissionsBoundary.html) |
| `PutRolePermissionsBoundary`    | ✅ Supported | Applied by SimulatePrincipalPolicy and by opt-in enforcement; NoSuchEntity if the policy does not exist | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_PutRolePermissionsBoundary.html)    |
| `DeleteRolePermissionsBoundary` | ✅ Supported |                                                                                                         | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteRolePermissionsBoundary.html) |

### User tagging

| Operation      | Status       | Notes | AWS Docs                                                                          |
| -------------- | ------------ | ----- | --------------------------------------------------------------------------------- |
| `TagUser`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_TagUser.html)      |
| `UntagUser`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UntagUser.html)    |
| `ListUserTags` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListUserTags.html) |

### Roles

| Operation                 | Status       | Notes                                                                                         | AWS Docs                                                                                     |
| ------------------------- | ------------ | --------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `CreateRole`              | ✅ Supported | Inline `Tags` applied at creation                                                             | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateRole.html)              |
| `GetRole`                 | ✅ Supported |                                                                                               | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetRole.html)                 |
| `ListRoles`               | ✅ Supported |                                                                                               | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListRoles.html)               |
| `DeleteRole`              | ✅ Supported | DeleteConflict (409) while an instance profile association or inline/attached policies remain | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteRole.html)              |
| `UpdateAssumeRolePolicy`  | ✅ Supported |                                                                                               | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UpdateAssumeRolePolicy.html)  |
| `CreateServiceLinkedRole` | ✅ Supported |                                                                                               | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateServiceLinkedRole.html) |

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

### Managed policy tagging

| Operation        | Status       | Notes | AWS Docs                                                                            |
| ---------------- | ------------ | ----- | ----------------------------------------------------------------------------------- |
| `TagPolicy`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_TagPolicy.html)      |
| `UntagPolicy`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UntagPolicy.html)    |
| `ListPolicyTags` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListPolicyTags.html) |

### Instance profile tagging

| Operation                 | Status       | Notes | AWS Docs                                                                                     |
| ------------------------- | ------------ | ----- | -------------------------------------------------------------------------------------------- |
| `TagInstanceProfile`      | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_TagInstanceProfile.html)      |
| `UntagInstanceProfile`    | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_UntagInstanceProfile.html)    |
| `ListInstanceProfileTags` | ✅ Supported |       | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListInstanceProfileTags.html) |

### Instance profiles

| Operation                       | Status       | Notes                             | AWS Docs                                                                                           |
| ------------------------------- | ------------ | --------------------------------- | -------------------------------------------------------------------------------------------------- |
| `CreateInstanceProfile`         | ✅ Supported | Inline `Tags` applied at creation | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateInstanceProfile.html)         |
| `GetInstanceProfile`            | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetInstanceProfile.html)            |
| `DeleteInstanceProfile`         | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteInstanceProfile.html)         |
| `AddRoleToInstanceProfile`      | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AddRoleToInstanceProfile.html)      |
| `RemoveRoleFromInstanceProfile` | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_RemoveRoleFromInstanceProfile.html) |
| `ListInstanceProfiles`          | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListInstanceProfiles.html)          |
| `ListInstanceProfilesForRole`   | ✅ Supported |                                   | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListInstanceProfilesForRole.html)   |

### Managed policies

| Operation      | Status       | Notes                                                                                                                        | AWS Docs                                                                          |
| -------------- | ------------ | ---------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `CreatePolicy` | ✅ Supported | Inline `Tags` applied at creation                                                                                            | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreatePolicy.html) |
| `GetPolicy`    | ✅ Supported |                                                                                                                              | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetPolicy.html)    |
| `ListPolicies` | ✅ Supported |                                                                                                                              | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListPolicies.html) |
| `DeletePolicy` | ✅ Supported | DeleteConflict (409) while the policy is attached to any user, role or group, or used as one of their permissions boundaries | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeletePolicy.html) |

### Groups

| Operation             | Status       | Notes                                                                               | AWS Docs                                                                                 |
| --------------------- | ------------ | ----------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `CreateGroup`         | ✅ Supported |                                                                                     | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_CreateGroup.html)         |
| `GetGroup`            | ✅ Supported | Returns the group's members, paginated with Marker/MaxItems (default 100, max 1000) | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetGroup.html)            |
| `DeleteGroup`         | ✅ Supported | DeleteConflict (409) while members or inline/attached policies remain               | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_DeleteGroup.html)         |
| `ListGroups`          | ✅ Supported |                                                                                     | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListGroups.html)          |
| `AddUserToGroup`      | ✅ Supported |                                                                                     | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_AddUserToGroup.html)      |
| `RemoveUserFromGroup` | ✅ Supported |                                                                                     | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_RemoveUserFromGroup.html) |
| `ListGroupsForUser`   | ✅ Supported |                                                                                     | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_ListGroupsForUser.html)   |

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

| Operation                 | Status       | Notes                                                                                                                                                                                                  | AWS Docs                                                                                     |
| ------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- |
| `SimulatePrincipalPolicy` | ✅ Supported | Real evaluation of the principal's identity policies (plus an optional ResourcePolicy and permissions boundary): allowed / explicitDeny / implicitDeny with MatchedStatements and MissingContextValues | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_SimulatePrincipalPolicy.html) |
| `SimulateCustomPolicy`    | ✅ Supported | Evaluates the supplied PolicyInputList without touching any stored entity                                                                                                                              | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_SimulateCustomPolicy.html)    |

### Account details

| Operation                        | Status       | Notes                                                              | AWS Docs                                                                                            |
| -------------------------------- | ------------ | ------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `GetAccountAuthorizationDetails` | ✅ Supported | Returns all users, groups, roles, and managed policies in one call | [docs](https://docs.aws.amazon.com/IAM/latest/APIReference/API_GetAccountAuthorizationDetails.html) |

<!-- END overcast:capabilities -->
