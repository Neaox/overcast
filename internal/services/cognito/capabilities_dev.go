//go:build dev

package cognito

import "github.com/Neaox/overcast/internal/capabilities"

func init() {
	capabilities.Default.Register(
		// User Pool operations
		capabilities.Capability{Service: "cognito", Operation: "CreateUserPool", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Returns Id and Arn; Id format {region}_{8-char-hex}; accepts SignInPolicy, email templates, UserAttributeUpdateSettings, and DeviceConfiguration"},
		capabilities.Capability{Service: "cognito", Operation: "DescribeUserPool", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Returns SignInPolicy, email templates, admin config, email configuration, and UserAttributeUpdateSettings"},
		capabilities.Capability{Service: "cognito", Operation: "DeleteUserPool", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateUserPool", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Updates SignInPolicy, VerificationMessageTemplate, AdminCreateUserConfig, EmailConfiguration, UserAttributeUpdateSettings"},
		capabilities.Capability{Service: "cognito", Operation: "ListUserPools", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Pagination via NextToken"},
		capabilities.Capability{Service: "cognito", Operation: "SetUserPoolMfaConfig", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Stores MfaConfiguration and WebAuthnConfiguration for passkey sign-in; passkey cryptographic validation is intentionally partial"},
		capabilities.Capability{Service: "cognito", Operation: "GetUserPoolMfaConfig", Category: "User Pool operations", Status: capabilities.StatusSupported, Notes: "Returns stored MfaConfiguration and WebAuthnConfiguration"},
		// User Pool Client operations
		capabilities.Capability{Service: "cognito", Operation: "CreateUserPoolClient", Category: "User Pool Client operations", Status: capabilities.StatusSupported, Notes: "Returns ClientId (26-char hex); accepts and validates ExplicitAuthFlows, AccessTokenValidity, IdTokenValidity, RefreshTokenValidity, TokenValidityUnits"},
		capabilities.Capability{Service: "cognito", Operation: "DescribeUserPoolClient", Category: "User Pool Client operations", Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "DeleteUserPoolClient", Category: "User Pool Client operations", Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateUserPoolClient", Category: "User Pool Client operations", Status: capabilities.StatusSupported, Notes: "Updates client name, validates ExplicitAuthFlows, token validity"},
		capabilities.Capability{Service: "cognito", Operation: "ListUserPoolClients", Category: "User Pool Client operations", Status: capabilities.StatusSupported, Notes: "Pagination via NextToken"},
		// User operations
		capabilities.Capability{Service: "cognito", Operation: "AdminCreateUser", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Bcrypt hashes password; sends email unless MessageAction=SUPPRESS"},
		capabilities.Capability{Service: "cognito", Operation: "AdminDeleteUser", Category: "User operations", Status: capabilities.StatusSupported, Notes: "UserNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "AdminGetUser", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Returns attributes + status"},
		capabilities.Capability{Service: "cognito", Operation: "AdminSetUserPassword", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Permanent=true sets status CONFIRMED"},
		capabilities.Capability{Service: "cognito", Operation: "AdminConfirmSignUp", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Confirms a UNCONFIRMED user"},
		capabilities.Capability{Service: "cognito", Operation: "AdminUpdateUserAttributes", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Merges attributes; honors verification-before-update settings unless *_verified=true is supplied"},
		capabilities.Capability{Service: "cognito", Operation: "AdminDeleteUserAttributes", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Removes named attributes from a user"},
		capabilities.Capability{Service: "cognito", Operation: "AdminDisableUser", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Sets Enabled=false; sign-in returns NotAuthorizedException"},
		capabilities.Capability{Service: "cognito", Operation: "AdminEnableUser", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Re-enables a disabled user"},
		capabilities.Capability{Service: "cognito", Operation: "ListUsers", Category: "User operations", Status: capabilities.StatusSupported, Notes: "Pagination via PaginationToken"},
		// Auth / Token operations
		capabilities.Capability{Service: "cognito", Operation: "SignUp", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Sends confirmation email; returns UserSub"},
		capabilities.Capability{Service: "cognito", Operation: "ConfirmSignUp", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "CodeMismatchException / ExpiredCodeException on failure; returns Session for USER_AUTH sign-in"},
		capabilities.Capability{Service: "cognito", Operation: "ResendConfirmationCode", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Generates and emails a new confirmation code"},
		capabilities.Capability{Service: "cognito", Operation: "InitiateAuth", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "USER_PASSWORD_AUTH + USER_SRP_AUTH + REFRESH_TOKEN_AUTH + CUSTOM_AUTH; USER_AUTH with ConfirmSignUp Session, SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, or preferred PASSWORD/WEB_AUTHN/EMAIL_OTP/SMS_OTP; returns NEW_PASSWORD_REQUIRED, SOFTWARE_TOKEN_MFA, or DEVICE_SRP_AUTH"},
		capabilities.Capability{Service: "cognito", Operation: "AdminInitiateAuth", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "USER_PASSWORD_AUTH + USER_SRP_AUTH + REFRESH_TOKEN_AUTH + CUSTOM_AUTH with UserPoolId; USER_AUTH with ConfirmSignUp Session, SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, or preferred PASSWORD/WEB_AUTHN/EMAIL_OTP/SMS_OTP"},
		capabilities.Capability{Service: "cognito", Operation: "RespondToAuthChallenge", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "SELECT_CHALLENGE/PASSWORD/PASSWORD_SRP/WEB_AUTHN/EMAIL_OTP/SMS_OTP, PASSWORD, PASSWORD_VERIFIER, CUSTOM_CHALLENGE, DEVICE_SRP_AUTH, DEVICE_PASSWORD_VERIFIER, WEB_AUTHN, EMAIL_OTP, SMS_OTP, NEW_PASSWORD_REQUIRED, and SOFTWARE_TOKEN_MFA challenges"},
		capabilities.Capability{Service: "cognito", Operation: "AdminRespondToAuthChallenge", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Same as above with admin credentials"},
		capabilities.Capability{Service: "cognito", Operation: "ConfirmDevice", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Confirms a NewDeviceMetadata device key and stores remembered-device metadata; SRP verifier validation is intentionally partial"},
		capabilities.Capability{Service: "cognito", Operation: "GetDevice", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Returns a confirmed device for the signed-in user"},
		capabilities.Capability{Service: "cognito", Operation: "ListDevices", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Lists confirmed devices for the signed-in user"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateDeviceStatus", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Marks a signed-in user's confirmed device as remembered or not_remembered"},
		capabilities.Capability{Service: "cognito", Operation: "ForgetDevice", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Removes a confirmed device for the signed-in user"},
		capabilities.Capability{Service: "cognito", Operation: "AdminGetDevice", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Returns a user's confirmed device"},
		capabilities.Capability{Service: "cognito", Operation: "AdminListDevices", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Lists a user's confirmed devices with pagination"},
		capabilities.Capability{Service: "cognito", Operation: "AdminUpdateDeviceStatus", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Marks a user's confirmed device as remembered or not_remembered"},
		capabilities.Capability{Service: "cognito", Operation: "AdminForgetDevice", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Removes a user's confirmed device"},
		capabilities.Capability{Service: "cognito", Operation: "ForgotPassword", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Sends password-reset code by email"},
		capabilities.Capability{Service: "cognito", Operation: "ConfirmForgotPassword", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Validates reset code; sets new bcrypt password"},
		capabilities.Capability{Service: "cognito", Operation: "ChangePassword", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Validates AccessToken + old password before setting new one"},
		capabilities.Capability{Service: "cognito", Operation: "GetUser", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Validates AccessToken; returns full user profile"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateUserAttributes", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Self-service; validates AccessToken; merges attributes or creates pending email/phone updates with CodeDeliveryDetailsList"},
		capabilities.Capability{Service: "cognito", Operation: "VerifyUserAttribute", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Verifies pending email/phone updates and sets *_verified=true"},
		capabilities.Capability{Service: "cognito", Operation: "GetUserAttributeVerificationCode", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Sends or resends email/phone verification codes for the signed-in user"},
		capabilities.Capability{Service: "cognito", Operation: "DeleteUserAttributes", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Self-service; validates AccessToken; removes named attributes"},
		capabilities.Capability{Service: "cognito", Operation: "GlobalSignOut", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Revokes access + id + refresh tokens for the user"},
		capabilities.Capability{Service: "cognito", Operation: "RevokeToken", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Revokes a specific refresh token"},
		capabilities.Capability{Service: "cognito", Operation: "AssociateSoftwareToken", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Issues a TOTP secret for the user; requires valid AccessToken"},
		capabilities.Capability{Service: "cognito", Operation: "VerifySoftwareToken", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Verifies a TOTP code and marks the secret verified"},
		capabilities.Capability{Service: "cognito", Operation: "StartWebAuthnRegistration", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Returns passkey CredentialCreationOptions for the signed-in user"},
		capabilities.Capability{Service: "cognito", Operation: "CompleteWebAuthnRegistration", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Registers passkey credential metadata; attestation validation is intentionally partial"},
		capabilities.Capability{Service: "cognito", Operation: "SetUserMFAPreference", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Enables/disables TOTP MFA for the calling user"},
		capabilities.Capability{Service: "cognito", Operation: "AdminSetUserMFAPreference", Category: "Auth / Token operations", Status: capabilities.StatusSupported, Notes: "Same as above, admin version"},
		// Group operations
		capabilities.Capability{Service: "cognito", Operation: "CreateGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "GroupExistsException if duplicate"},
		capabilities.Capability{Service: "cognito", Operation: "GetGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "DeleteGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "ResourceNotFoundException if not found"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "Updates Description, Precedence, RoleArn"},
		capabilities.Capability{Service: "cognito", Operation: "ListGroups", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "Returns all groups for a pool"},
		capabilities.Capability{Service: "cognito", Operation: "AdminAddUserToGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "Idempotent"},
		capabilities.Capability{Service: "cognito", Operation: "AdminRemoveUserFromGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "No error if user is not in group"},
		capabilities.Capability{Service: "cognito", Operation: "AdminListGroupsForUser", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "Returns groups the user belongs to"},
		capabilities.Capability{Service: "cognito", Operation: "ListUsersInGroup", Category: "Group operations", Status: capabilities.StatusSupported, Notes: "Returns users belonging to the specified group"},
		// User Pool Domain operations
		capabilities.Capability{Service: "cognito", Operation: "CreateUserPoolDomain", Category: "User Pool Domain operations", Status: capabilities.StatusSupported, Notes: "Associates a domain with the user pool's hosted UI"},
		capabilities.Capability{Service: "cognito", Operation: "DescribeUserPoolDomain", Category: "User Pool Domain operations", Status: capabilities.StatusSupported, Notes: "Returns domain details; empty DomainDescription when domain not found"},
		capabilities.Capability{Service: "cognito", Operation: "DeleteUserPoolDomain", Category: "User Pool Domain operations", Status: capabilities.StatusSupported, Notes: "Removes the domain association from the pool"},
		capabilities.Capability{Service: "cognito", Operation: "UpdateUserPoolDomain", Category: "User Pool Domain operations", Status: capabilities.StatusSupported, Notes: "Accepted; SSL certificate updates are inert in the emulator"},
	)
}
