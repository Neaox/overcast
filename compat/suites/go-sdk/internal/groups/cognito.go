package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	ciptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
)

func Cognito(c *clients.Clients) ServiceGroup {
	g := &cognitoGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateUserPool":       g.CreateUserPool,
			"DescribeUserPool":     g.DescribeUserPool,
			"ListUserPools":        g.ListUserPools,
			"CreateUserPoolClient": g.CreateUserPoolClient,
			"ListUserPoolClients":  g.ListUserPoolClients,
			"AdminCreateUser":      g.AdminCreateUser,
			"ListUsers":            g.ListUsers,
			"AdminDeleteUser":      g.AdminDeleteUser,
			"DeleteUserPool":       g.DeleteUserPool,
			"CreateUserPoolClient with token validity":      g.CreateClientTokenValidity,
			"DescribeUserPoolClient returns token validity": g.DescribeClientTokenValidity,
			"UpdateUserPoolClient changes token validity":   g.UpdateClientTokenValidity,
			"DeleteUserPoolClient":                          g.DeleteUserPoolClient,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cognito-userpools":      g.setupUserPools,
			"cognito-token-validity": g.setupTokenValidity,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"cognito-userpools":      g.teardownUserPools,
			"cognito-token-validity": g.teardownTokenValidity,
		},
	}
}

type cognitoGroup struct{ c *clients.Clients }

func (g *cognitoGroup) cl() *cip.Client { return g.c.Cognito() }

func (g *cognitoGroup) setupUserPools(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *cognitoGroup) teardownUserPools(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return nil
	}
	if username := t.GetString("cognito_username"); username != "" {
		g.cl().AdminDeleteUser(ctx, &cip.AdminDeleteUserInput{ //nolint:errcheck
			UserPoolId: aws.String(poolID),
			Username:   aws.String(username),
		})
	}
	g.cl().DeleteUserPool(ctx, &cip.DeleteUserPoolInput{UserPoolId: aws.String(poolID)}) //nolint:errcheck
	return nil
}

func (g *cognitoGroup) CreateUserPool(ctx context.Context, t *harness.TestContext) error {
	poolName := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateUserPool(ctx, &cip.CreateUserPoolInput{
		PoolName: aws.String(poolName),
	})
	if err != nil {
		return err
	}
	if resp.UserPool == nil || resp.UserPool.Id == nil {
		return fmt.Errorf("CreateUserPool: missing Id")
	}
	t.Set("cognito_pool_id", *resp.UserPool.Id)
	return nil
}

func (g *cognitoGroup) DescribeUserPool(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return fmt.Errorf("DescribeUserPool: no pool from CreateUserPool")
	}
	resp, err := g.cl().DescribeUserPool(ctx, &cip.DescribeUserPoolInput{
		UserPoolId: aws.String(poolID),
	})
	if err != nil {
		return err
	}
	if resp.UserPool == nil || resp.UserPool.Id == nil {
		return fmt.Errorf("DescribeUserPool: missing Id")
	}
	return nil
}

func (g *cognitoGroup) ListUserPools(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListUserPools(ctx, &cip.ListUserPoolsInput{
		MaxResults: aws.Int32(10),
	})
	return err
}

func (g *cognitoGroup) AdminCreateUser(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return fmt.Errorf("AdminCreateUser: no pool from CreateUserPool")
	}
	username := fmt.Sprintf("compat-user-%s", t.RunID)
	_, err := g.cl().AdminCreateUser(ctx, &cip.AdminCreateUserInput{
		UserPoolId: aws.String(poolID),
		Username:   aws.String(username),
	})
	if err != nil {
		return err
	}
	t.Set("cognito_username", username)
	return nil
}

func (g *cognitoGroup) ListUsers(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return nil
	}
	_, err := g.cl().ListUsers(ctx, &cip.ListUsersInput{
		UserPoolId: aws.String(poolID),
	})
	return err
}

func (g *cognitoGroup) AdminDeleteUser(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	username := t.GetString("cognito_username")
	if poolID == "" || username == "" {
		return nil
	}
	_, err := g.cl().AdminDeleteUser(ctx, &cip.AdminDeleteUserInput{
		UserPoolId: aws.String(poolID),
		Username:   aws.String(username),
	})
	return err
}

func (g *cognitoGroup) DeleteUserPool(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return nil
	}
	_, err := g.cl().DeleteUserPool(ctx, &cip.DeleteUserPoolInput{
		UserPoolId: aws.String(poolID),
	})
	return err
}

func (g *cognitoGroup) CreateUserPoolClient(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return fmt.Errorf("CreateUserPoolClient: no pool from CreateUserPool")
	}
	resp, err := g.cl().CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientName: aws.String(fmt.Sprintf("compat-client-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	if resp.UserPoolClient == nil || resp.UserPoolClient.ClientId == nil {
		return fmt.Errorf("CreateUserPoolClient: missing ClientId")
	}
	t.Set("cognito_client_id", *resp.UserPoolClient.ClientId)
	return nil
}

func (g *cognitoGroup) ListUserPoolClients(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("cognito_pool_id")
	if poolID == "" {
		return fmt.Errorf("ListUserPoolClients: no pool from CreateUserPool")
	}
	_, err := g.cl().ListUserPoolClients(ctx, &cip.ListUserPoolClientsInput{
		UserPoolId: aws.String(poolID),
		MaxResults: aws.Int32(10),
	})
	return err
}

// ── cognito-token-validity ─────────────────────────────────────────────────

func (g *cognitoGroup) setupTokenValidity(_ context.Context, _ *harness.TestContext) error {
	return nil
}

func (g *cognitoGroup) teardownTokenValidity(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID != "" && clientID != "" {
		g.cl().DeleteUserPoolClient(ctx, &cip.DeleteUserPoolClientInput{ //nolint:errcheck
			UserPoolId: aws.String(poolID),
			ClientId:   aws.String(clientID),
		})
	}
	if poolID != "" {
		g.cl().DeleteUserPool(ctx, &cip.DeleteUserPoolInput{UserPoolId: aws.String(poolID)}) //nolint:errcheck
	}
	return nil
}

func (g *cognitoGroup) CreateClientTokenValidity(ctx context.Context, t *harness.TestContext) error {
	poolName := fmt.Sprintf("compat-tv-%s", t.RunID)
	poolResp, err := g.cl().CreateUserPool(ctx, &cip.CreateUserPoolInput{
		PoolName: aws.String(poolName),
	})
	if err != nil {
		return err
	}
	poolID := *poolResp.UserPool.Id
	t.Set("tv_pool_id", poolID)

	resp, err := g.cl().CreateUserPoolClient(ctx, &cip.CreateUserPoolClientInput{
		UserPoolId:           aws.String(poolID),
		ClientName:           aws.String(fmt.Sprintf("compat-client-%s", t.RunID)),
		AccessTokenValidity:  aws.Int32(2),
		IdTokenValidity:      aws.Int32(3),
		RefreshTokenValidity: 7,
		TokenValidityUnits: &ciptypes.TokenValidityUnitsType{
			AccessToken:  ciptypes.TimeUnitsTypeHours,
			IdToken:      ciptypes.TimeUnitsTypeHours,
			RefreshToken: ciptypes.TimeUnitsTypeDays,
		},
	})
	if err != nil {
		return err
	}
	if resp.UserPoolClient == nil || resp.UserPoolClient.ClientId == nil {
		return fmt.Errorf("CreateClientTokenValidity: missing ClientId")
	}
	t.Set("tv_client_id", *resp.UserPoolClient.ClientId)
	return nil
}

func (g *cognitoGroup) DescribeClientTokenValidity(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return fmt.Errorf("DescribeClientTokenValidity: missing pool/client from create")
	}
	_, err := g.cl().DescribeUserPoolClient(ctx, &cip.DescribeUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientId:   aws.String(clientID),
	})
	return err
}

func (g *cognitoGroup) UpdateClientTokenValidity(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return fmt.Errorf("UpdateClientTokenValidity: missing pool/client from create")
	}
	_, err := g.cl().UpdateUserPoolClient(ctx, &cip.UpdateUserPoolClientInput{
		UserPoolId:          aws.String(poolID),
		ClientId:            aws.String(clientID),
		AccessTokenValidity: aws.Int32(30),
		TokenValidityUnits: &ciptypes.TokenValidityUnitsType{
			AccessToken:  ciptypes.TimeUnitsTypeMinutes,
			IdToken:      ciptypes.TimeUnitsTypeHours,
			RefreshToken: ciptypes.TimeUnitsTypeDays,
		},
	})
	return err
}

func (g *cognitoGroup) DeleteUserPoolClient(ctx context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return nil
	}
	_, err := g.cl().DeleteUserPoolClient(ctx, &cip.DeleteUserPoolClientInput{
		UserPoolId: aws.String(poolID),
		ClientId:   aws.String(clientID),
	})
	return err
}
