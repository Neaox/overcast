package cognito

// triggers.go — LambdaConfig trigger dispatch (issue #1171).
//
// LambdaConfig has been stored and round-tripped through CreateUserPool /
// DescribeUserPool / UpdateUserPool since #536, but nothing invoked the
// configured ARNs: a CDK pool built with `lambdaTriggers: { preSignUp: fn }`
// looked fully configured and the trigger never ran (the §2.1 shape-2 failure
// — provisions clean, reports success, and does not work).
//
// This file wires the synchronous Lambda invocation seam
// (events.FunctionSyncInvoker — internal/events/sink.go, the same one
// Secrets Manager rotation, API Gateway, and AppSync already use) into the
// five highest-usage triggers: PreSignUp, PostConfirmation,
// PreTokenGeneration, PostAuthentication, and CustomMessage.
//
// Deliberately out of scope, tracked elsewhere or left for a fast-follow:
//   - CUSTOM_AUTH's DefineAuthChallenge / CreateAuthChallenge /
//     VerifyAuthChallengeResponse — already tracked by #88, #94, #101.
//   - PreAuthentication and UserMigration — not yet wired; see
//     capabilities_dev.go.
//   - PreTokenGeneration event versions V2_0/V3_0 (access-token
//     customization) — only V1_0 (ID-token-only) is implemented, matching the
//     flat LambdaConfig.PreTokenGeneration ARN this emulator stores (no
//     LambdaVersion field).
//   - ClientMetadata / ValidationData passthrough — ClientMetadata and
//     ValidationData in signUp and AdminCreateUser: Overcast's request
//     structs never captured these fields, so trigger events fire without
//     them. That is a smaller, separable enhancement; see capabilities_dev.go.
//   - The Smithy RPC v2 duplicate implementation (typed_logic.go) gets
//     PreTokenGeneration for free (issueTokens is the single choke point for
//     both protocols) but not PreSignUp/PostConfirmation/PostAuthentication/
//     CustomMessage — those remain duplicated per-protocol logic and were out
//     of scope for this pass. Real Cognito SDK traffic uses the classic
//     X-Amz-Target JSON protocol implemented in handler_auth.go/handler_users.go,
//     which is what this issue and the integration test suite exercise.
//
// Failure semantics (matching AWS's documented behavior at
// https://docs.aws.amazon.com/cognito/latest/developerguide/cognito-user-pools-working-with-lambda-triggers.html#important-lambda-considerations):
// "If your Lambda function doesn't return the request and response parameters
// to Amazon Cognito, or returns an error, the authentication event doesn't
// succeed." Every trigger here except PostConfirmation is therefore blocking:
// an error or an unreachable function fails the calling API with
// UserLambdaValidationException, never silently. PostConfirmation is the
// documented, deliberate exception — the account is already confirmed by the
// time it runs, and the issue's own scoping call is "fire-and-log is enough
// for a first cut — AWS does not let a PostConfirmation failure roll back the
// confirmation."

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── trigger source values (AWS's own strings — see the Lambda trigger docs
// cited above and per-trigger doc pages) ──────────────────────────────────

const (
	triggerSourcePreSignUpSignUp        = "PreSignUp_SignUp"
	triggerSourcePreSignUpAdminCreate   = "PreSignUp_AdminCreateUser"
	triggerSourcePostConfirmSignUp      = "PostConfirmation_ConfirmSignUp"
	triggerSourcePostConfirmForgotPass  = "PostConfirmation_ConfirmForgotPassword"
	triggerSourcePostAuthentication     = "PostAuthentication_Authentication"
	triggerSourceTokenGenAuthentication = "TokenGeneration_Authentication"
	triggerSourceTokenGenNewPassword    = "TokenGeneration_NewPasswordChallenge"
	triggerSourceTokenGenRefreshTokens  = "TokenGeneration_RefreshTokens"
	triggerSourceTokenGenDevice         = "TokenGeneration_AuthenticateDevice"
	triggerSourceTokenGenHostedAuth     = "TokenGeneration_HostedAuth"
	triggerSourceCustomMessageSignUp    = "CustomMessage_SignUp"
	triggerSourceCustomMessageAdminUser = "CustomMessage_AdminCreateUser"
	triggerSourceCustomMessageResend    = "CustomMessage_ResendCode"
	triggerSourceCustomMessageForgotPwd = "CustomMessage_ForgotPassword"
)

// ─── common event envelope ────────────────────────────────────────────────

// triggerCallerContext is the "callerContext" object AWS adds to every
// Lambda trigger event.
type triggerCallerContext struct {
	AWSSDKVersion string `json:"awsSdkVersion"`
	ClientID      string `json:"clientId"`
}

// triggerEnvelope is the common shape every Cognito Lambda trigger event
// shares: https://docs.aws.amazon.com/cognito/latest/developerguide/cognito-user-pools-working-with-lambda-triggers.html#cognito-user-pools-lambda-trigger-syntax-shared
type triggerEnvelope[Req any, Resp any] struct {
	Version       string               `json:"version"`
	Region        string               `json:"region"`
	UserPoolID    string               `json:"userPoolId"`
	UserName      string               `json:"userName"`
	CallerContext triggerCallerContext `json:"callerContext"`
	TriggerSource string               `json:"triggerSource"`
	Request       Req                  `json:"request"`
	Response      Resp                 `json:"response"`
}

// userAttributesMap flattens []UserAttribute into the flat string map every
// trigger request's "userAttributes" field uses.
func userAttributesMap(attrs []UserAttribute) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Name] = a.Value
	}
	return out
}

// errUserLambdaValidation matches AWS's UserLambdaValidationException, the
// exception every synchronous trigger's failure surfaces as: an explicit
// error the function returned, or the function being unreachable at all
// (no such function, not in an invokable state, invoker not wired). Reported
// this way rather than swallowed — see the §2.1 shape-2 failure this issue
// exists to remove.
func errUserLambdaValidation(triggerName, detail string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "UserLambdaValidationException",
		Message:    fmt.Sprintf("%s failed with error %s.", triggerName, detail),
		HTTPStatus: 400,
	}
}

func errInvalidLambdaResponse(triggerName string, cause error) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidLambdaResponseException",
		Message:    fmt.Sprintf("%s trigger returned an invalid response: %s", triggerName, cause),
		HTTPStatus: 400,
	}
}

// lambdaFunctionNameFromARN reduces a function ARN to the bare name
// events.FunctionSyncInvoker expects. A bare name passes through unchanged.
// Mirrors secretsmanager.lambdaFunctionName / appsync.lambdaFunctionNameFromARN
// — each invoker-calling service owns its own copy rather than sharing one,
// consistent with how those two are structured.
func lambdaFunctionNameFromARN(arnOrName string) string {
	const marker = ":function:"
	if idx := strings.Index(arnOrName, marker); idx >= 0 {
		name := arnOrName[idx+len(marker):]
		if colon := strings.Index(name, ":"); colon >= 0 {
			name = name[:colon]
		}
		return name
	}
	return arnOrName
}

// invokeCognitoTrigger builds the common event envelope, invokes functionARN
// synchronously, and decodes the response. Three outcomes:
//   - functionARN == "" (trigger not configured on this pool): zero Resp,
//     ran=false, no error.
//   - the function could not be invoked, or returned an error, or an
//     unparseable response: zero Resp, ran=false, non-nil *protocol.AWSError
//     naming triggerName.
//   - success: the decoded Response, ran=true, no error.
func invokeCognitoTrigger[Req any, Resp any](
	ctx context.Context, s *Service, functionARN, triggerName, triggerSource, region, poolID, userName, clientID string, req Req,
) (Resp, bool, *protocol.AWSError) {
	var zero Resp
	if functionARN == "" {
		return zero, false, nil
	}
	if s.invoker == nil {
		return zero, false, errUserLambdaValidation(triggerName, "no Lambda invoker is wired into the Cognito service")
	}

	ev := triggerEnvelope[Req, Resp]{
		Version:       "1",
		Region:        region,
		UserPoolID:    poolID,
		UserName:      userName,
		CallerContext: triggerCallerContext{AWSSDKVersion: "overcast-emulator", ClientID: clientID},
		TriggerSource: triggerSource,
		Request:       req,
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return zero, false, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("cognito: marshal %s event: %w", triggerSource, err))
	}

	functionName := lambdaFunctionNameFromARN(functionARN)
	outcome, invokeErr := s.invoker.Invoke(ctx, functionName, payload)
	if invokeErr != nil {
		return zero, false, errUserLambdaValidation(triggerName, "the trigger function "+functionName+" could not be invoked: "+invokeErr.Error())
	}
	if outcome == nil {
		return zero, false, errUserLambdaValidation(triggerName, "the trigger function "+functionName+" could not be invoked — check that it exists and is Active")
	}
	if outcome.FunctionError != "" {
		return zero, false, errUserLambdaValidation(triggerName, strings.TrimSpace(string(outcome.Payload)))
	}

	var full triggerEnvelope[Req, Resp]
	if err := json.Unmarshal(outcome.Payload, &full); err != nil {
		return zero, false, errInvalidLambdaResponse(triggerName, err)
	}
	return full.Response, true, nil
}

// ─── PreSignUp ─────────────────────────────────────────────────────────────
// https://docs.aws.amazon.com/cognito/latest/developerguide/user-pool-lambda-pre-sign-up.html

type preSignUpRequest struct {
	UserAttributes map[string]string `json:"userAttributes"`
	ValidationData map[string]string `json:"validationData,omitempty"`
	ClientMetadata map[string]string `json:"clientMetadata,omitempty"`
}

type preSignUpResponse struct {
	AutoConfirmUser bool `json:"autoConfirmUser"`
	AutoVerifyEmail bool `json:"autoVerifyEmail"`
	AutoVerifyPhone bool `json:"autoVerifyPhone"`
}

// runPreSignUp invokes the pool's PreSignUp trigger, if configured. Blocking:
// an error fails SignUp/AdminCreateUser the way AWS does. autoConfirmUser /
// autoVerifyEmail / autoVerifyPhone are ignored by the caller when
// triggerSource is PreSignUp_AdminCreateUser, matching AWS's own documented
// note that AdminCreateUser ignores these response fields.
func (s *Service) runPreSignUp(ctx context.Context, pool *UserPool, triggerSource, clientID, username string, attrs []UserAttribute) (preSignUpResponse, *protocol.AWSError) {
	if pool.LambdaConfig == nil {
		return preSignUpResponse{}, nil
	}
	resp, _, aerr := invokeCognitoTrigger[preSignUpRequest, preSignUpResponse](
		ctx, s, pool.LambdaConfig.PreSignUp, "PreSignUp", triggerSource, s.region(ctx), pool.ID, username, clientID,
		preSignUpRequest{UserAttributes: userAttributesMap(attrs)},
	)
	return resp, aerr
}

// ─── PostConfirmation ──────────────────────────────────────────────────────
// https://docs.aws.amazon.com/cognito/latest/developerguide/user-pool-lambda-post-confirmation.html

type postConfirmationRequest struct {
	UserAttributes map[string]string `json:"userAttributes"`
}

// runPostConfirmation invokes the pool's PostConfirmation trigger, if
// configured. Fire-and-forget by design (see the file-level doc comment): a
// failure is logged, never returned to the caller, because the confirmation
// this trigger runs after cannot be rolled back.
func (s *Service) runPostConfirmation(ctx context.Context, pool *UserPool, triggerSource, clientID, username string, attrs []UserAttribute) {
	if pool.LambdaConfig == nil || pool.LambdaConfig.PostConfirmation == "" {
		return
	}
	_, _, aerr := invokeCognitoTrigger[postConfirmationRequest, struct{}](
		ctx, s, pool.LambdaConfig.PostConfirmation, "PostConfirmation", triggerSource, s.region(ctx), pool.ID, username, clientID,
		postConfirmationRequest{UserAttributes: userAttributesMap(attrs)},
	)
	if aerr != nil {
		s.log.Warn("cognito: PostConfirmation trigger failed (not fatal — AWS does not roll back confirmation on this trigger's error)",
			zap.String("poolId", pool.ID), zap.String("username", username), zap.String("error", aerr.Message))
	}
}

// ─── PostAuthentication ────────────────────────────────────────────────────
// https://docs.aws.amazon.com/cognito/latest/developerguide/user-pool-lambda-post-authentication.html

type postAuthenticationRequest struct {
	UserAttributes map[string]string `json:"userAttributes"`
	NewDeviceUsed  bool              `json:"newDeviceUsed"`
}

// runPostAuthentication invokes the pool's PostAuthentication trigger, if
// configured. Blocking: AWS runs this after authentication succeeds but
// before tokens are issued, and documents that a function which errors (or
// fails to return the event) "can still cause authentication to fail to
// complete" — so a failure here must abort token issuance rather than be
// swallowed.
func (s *Service) runPostAuthentication(ctx context.Context, pool *UserPool, clientID string, u *User) *protocol.AWSError {
	if pool.LambdaConfig == nil || pool.LambdaConfig.PostAuthentication == "" {
		return nil
	}
	_, _, aerr := invokeCognitoTrigger[postAuthenticationRequest, struct{}](
		ctx, s, pool.LambdaConfig.PostAuthentication, "PostAuthentication", triggerSourcePostAuthentication, s.region(ctx), pool.ID, u.Username, clientID,
		postAuthenticationRequest{UserAttributes: userAttributesMap(u.Attributes)},
	)
	return aerr
}

// ─── PreTokenGeneration (V1_0 only — see the file-level doc comment) ──────
// https://docs.aws.amazon.com/cognito/latest/developerguide/user-pool-lambda-pre-token-generation.html

type preTokenGenGroupConfig struct {
	GroupsToOverride   []string `json:"groupsToOverride,omitempty"`
	IamRolesToOverride []string `json:"iamRolesToOverride,omitempty"`
	PreferredRole      string   `json:"preferredRole,omitempty"`
}

type preTokenGenRequest struct {
	UserAttributes     map[string]string       `json:"userAttributes"`
	GroupConfiguration *preTokenGenGroupConfig `json:"groupConfiguration,omitempty"`
}

type claimsOverrideDetails struct {
	ClaimsToAddOrOverride map[string]string       `json:"claimsToAddOrOverride,omitempty"`
	ClaimsToSuppress      []string                `json:"claimsToSuppress,omitempty"`
	GroupOverrideDetails  *preTokenGenGroupConfig `json:"groupOverrideDetails,omitempty"`
}

type preTokenGenResponse struct {
	ClaimsOverrideDetails *claimsOverrideDetails `json:"claimsOverrideDetails,omitempty"`
}

// runPreTokenGeneration invokes the pool's PreTokenGeneration trigger, if
// configured, and applies the V1_0 claimsOverrideDetails merge directly onto
// idClaims/accessClaims: claimsToAddOrOverride first, then claimsToSuppress
// (AWS: "If your function both suppresses and replaces a claim value, then
// Amazon Cognito suppresses the claim"), then groupOverrideDetails, which
// replaces cognito:groups on both tokens and cognito:preferred_role /
// cognito:roles on the ID token — "the only changes to the access token that
// version 1 events can make."
//
// Blocking: AWS documents that an error here means "Amazon Cognito doesn't
// generate a token".
func (s *Service) runPreTokenGeneration(ctx context.Context, pool *UserPool, triggerSource, clientID string, u *User, idClaims, accessClaims map[string]any) *protocol.AWSError {
	if pool.LambdaConfig == nil || pool.LambdaConfig.PreTokenGeneration == "" {
		return nil
	}
	var groupCfg *preTokenGenGroupConfig
	if len(u.Groups) > 0 {
		groupCfg = &preTokenGenGroupConfig{GroupsToOverride: u.Groups}
	}
	resp, ran, aerr := invokeCognitoTrigger[preTokenGenRequest, preTokenGenResponse](
		ctx, s, pool.LambdaConfig.PreTokenGeneration, "PreTokenGeneration", triggerSource, s.region(ctx), pool.ID, u.Username, clientID,
		preTokenGenRequest{UserAttributes: userAttributesMap(u.Attributes), GroupConfiguration: groupCfg},
	)
	if aerr != nil {
		return aerr
	}
	if !ran || resp.ClaimsOverrideDetails == nil {
		return nil
	}
	details := resp.ClaimsOverrideDetails
	for k, v := range details.ClaimsToAddOrOverride {
		idClaims[k] = v
	}
	for _, k := range details.ClaimsToSuppress {
		delete(idClaims, k)
	}
	if details.GroupOverrideDetails != nil {
		g := details.GroupOverrideDetails
		if len(g.GroupsToOverride) > 0 {
			idClaims["cognito:groups"] = g.GroupsToOverride
			accessClaims["cognito:groups"] = g.GroupsToOverride
		} else {
			delete(idClaims, "cognito:groups")
			delete(accessClaims, "cognito:groups")
		}
		if len(g.IamRolesToOverride) > 0 {
			idClaims["cognito:roles"] = g.IamRolesToOverride
		}
		if g.PreferredRole != "" {
			idClaims["cognito:preferred_role"] = g.PreferredRole
		}
	}
	return nil
}

// ─── CustomMessage ─────────────────────────────────────────────────────────
// https://docs.aws.amazon.com/cognito/latest/developerguide/user-pool-lambda-custom-message.html

type customMessageRequest struct {
	UserAttributes    map[string]string `json:"userAttributes"`
	CodeParameter     string            `json:"codeParameter"`
	UsernameParameter string            `json:"usernameParameter,omitempty"`
}

type customMessageResponse struct {
	SMSMessage   *string `json:"smsMessage"`
	EmailMessage *string `json:"emailMessage"`
	EmailSubject *string `json:"emailSubject"`
}

// messageOverride carries a CustomMessage trigger's response into the
// messaging.go send functions. A nil field means "no override — use the
// pool's own template/default", matching AWS returning the response object
// unmodified. The override strings still contain the {####}/{username}
// placeholders (codeParameter/usernameParameter), substituted the same way
// the default templates are, in expandTemplate.
type messageOverride struct {
	SMS     *string
	Email   *string
	Subject *string
}

// runCustomMessage invokes the pool's CustomMessage trigger, if configured,
// and returns the override to apply. Blocking, per AWS's general Lambda
// trigger error rule — an error here fails the calling API (SignUp,
// AdminCreateUser, ResendConfirmationCode, ForgotPassword).
func (s *Service) runCustomMessage(ctx context.Context, pool *UserPool, triggerSource, clientID, username, code, usernameParam string, attrs []UserAttribute) (*messageOverride, *protocol.AWSError) {
	if pool.LambdaConfig == nil || pool.LambdaConfig.CustomMessage == "" {
		return nil, nil
	}
	resp, ran, aerr := invokeCognitoTrigger[customMessageRequest, customMessageResponse](
		ctx, s, pool.LambdaConfig.CustomMessage, "CustomMessage", triggerSource, s.region(ctx), pool.ID, username, clientID,
		customMessageRequest{UserAttributes: userAttributesMap(attrs), CodeParameter: "{####}", UsernameParameter: usernameParam},
	)
	if aerr != nil {
		return nil, aerr
	}
	if !ran {
		return nil, nil
	}
	return &messageOverride{SMS: resp.SMSMessage, Email: resp.EmailMessage, Subject: resp.EmailSubject}, nil
}
