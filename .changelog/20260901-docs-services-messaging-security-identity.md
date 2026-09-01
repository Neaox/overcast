~ [docs/sqs/sns/ses] rewrote the SQS, SNS and SES service pages to the service page template
  each opens with a first-screen quick start, a What-works table and a Differences-from-AWS table; SNS gained docs/services/sns/limitations.md for the long divergence list (subscription confirmation, FIFO, delivery-failure semantics, CloudFormation), and all three now say plainly what lands in the console Inbox — SES mail, SNS email/SMS/webhook deliveries, and Cognito pool messages
~ [docs/cognito/iam] rewrote the two heaviest service pages and cut the hand-maintained capability tables that duplicated the generated operations page
  Cognito gained limitations.md (Lambda trigger coverage, emulator-only routes) and examples.md (user import); IAM gained limitations.md (policy-language coverage, enforcement scope) and troubleshooting.md (DeleteConflict, DELETE_FAILED, AccessDenied)
~ [docs/sts/ssm/secretsmanager/kms/acm/organizations/shield/waf/bedrock] rewrote the remaining messaging, security and identity service pages to the template
* [docs/cognito] corrected several stale facts on the Cognito page
  sign-in supports USER_SRP_AUTH, CUSTOM_AUTH and USER_AUTH choice-based flows (not just USER_PASSWORD_AUTH and REFRESH_TOKEN_AUTH), passwords are bcrypt-hashed at minimum cost rather than cost 10, and TOTP tolerates only the previous 30-second window, not ±30 seconds
* [docs/waf] the supported WAFv2 surface is 7 operations, not 4 — TagResource, UntagResource and ListTagsForResource were missing from the page
* [docs/sts] STS does store the assumed-role session, so opt-in IAM enforcement can resolve a caller
  the access key maps to the role ARN; the page previously said credentials are never stored
* [docs/kms] documented three ways the KMS emulation is inert
  EncryptionContext is not bound into the ciphertext, key policies and grants are stored but never evaluated, and a scheduled deletion never completes
* [web] the SES dashboard card no longer promises delivery history; the console SES page manages identities only, and sent mail is in the Inbox
