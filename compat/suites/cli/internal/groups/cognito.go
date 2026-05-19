package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// Cognito returns the Cognito service group.
func Cognito() ServiceGroup {
	g := &cognitoCliGroup{}
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

type cognitoCliGroup struct{}

func (g *cognitoCliGroup) setupUserPools(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *cognitoCliGroup) teardownUserPools(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("pool_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "cognito-idp", "delete-user-pool", "--user-pool-id", id) //nolint:errcheck
	}
	return nil
}

func (g *cognitoCliGroup) CreateUserPool(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "create-user-pool",
		"--pool-name", name,
	)
	if err != nil {
		return err
	}
	pool, _ := out["UserPool"].(map[string]interface{})
	id, _ := pool["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateUserPool: missing Id")
	}
	t.Set("pool_id", id)
	return nil
}

func (g *cognitoCliGroup) DescribeUserPool(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	if id == "" {
		return fmt.Errorf("DescribeUserPool: no pool_id from CreateUserPool")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "describe-user-pool",
		"--user-pool-id", id,
	)
	if err != nil {
		return err
	}
	if out["UserPool"] == nil {
		return fmt.Errorf("DescribeUserPool: missing UserPool")
	}
	return nil
}

func (g *cognitoCliGroup) ListUserPools(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "list-user-pools",
		"--max-results", "10",
	)
	return err
}

func (g *cognitoCliGroup) AdminCreateUser(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	if id == "" {
		return fmt.Errorf("AdminCreateUser: no pool_id from CreateUserPool")
	}
	username := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "admin-create-user",
		"--user-pool-id", id,
		"--username", username,
	)
	if err != nil {
		return err
	}
	if out["User"] == nil {
		return fmt.Errorf("AdminCreateUser: missing User")
	}
	t.Set("cognito_username", username)
	return nil
}

func (g *cognitoCliGroup) ListUsers(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	if id == "" {
		return nil
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "list-users",
		"--user-pool-id", id,
	)
	return err
}

func (g *cognitoCliGroup) AdminDeleteUser(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	username := t.GetString("cognito_username")
	if id == "" || username == "" {
		return fmt.Errorf("AdminDeleteUser: missing pool_id or username")
	}
	return awscli.Run(t.Endpoint, t.Region, "cognito-idp", "admin-delete-user",
		"--user-pool-id", id,
		"--username", username,
	)
}

func (g *cognitoCliGroup) DeleteUserPool(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "create-user-pool", "--pool-name", name)
	var id string
	if pool, _ := out["UserPool"].(map[string]interface{}); pool != nil {
		id, _ = pool["Id"].(string)
	}
	if id == "" {
		return fmt.Errorf("DeleteUserPool: could not create pool to delete")
	}
	return awscli.Run(t.Endpoint, t.Region, "cognito-idp", "delete-user-pool",
		"--user-pool-id", id,
	)
}

func (g *cognitoCliGroup) CreateUserPoolClient(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	if id == "" {
		return fmt.Errorf("CreateUserPoolClient: no pool_id from CreateUserPool")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "create-user-pool-client",
		"--user-pool-id", id,
		"--client-name", fmt.Sprintf("compat-client-%s", t.RunID),
	)
	if err != nil {
		return err
	}
	client, _ := out["UserPoolClient"].(map[string]interface{})
	clientID, _ := client["ClientId"].(string)
	if clientID == "" {
		return fmt.Errorf("CreateUserPoolClient: missing ClientId")
	}
	t.Set("cognito_client_id", clientID)
	return nil
}

func (g *cognitoCliGroup) ListUserPoolClients(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("pool_id")
	if id == "" {
		return fmt.Errorf("ListUserPoolClients: no pool_id from CreateUserPool")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "list-user-pool-clients",
		"--user-pool-id", id, "--max-results", "10",
	)
	if err != nil {
		return err
	}
	if out["UserPoolClients"] == nil {
		return fmt.Errorf("ListUserPoolClients: missing UserPoolClients")
	}
	return nil
}

// ── cognito-token-validity ─────────────────────────────────────────────────

func (g *cognitoCliGroup) setupTokenValidity(_ context.Context, _ *harness.TestContext) error {
	return nil
}

func (g *cognitoCliGroup) teardownTokenValidity(_ context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID != "" && clientID != "" {
		awscli.Run(t.Endpoint, t.Region, "cognito-idp", "delete-user-pool-client", "--user-pool-id", poolID, "--client-id", clientID) //nolint:errcheck
	}
	if poolID != "" {
		awscli.Run(t.Endpoint, t.Region, "cognito-idp", "delete-user-pool", "--user-pool-id", poolID) //nolint:errcheck
	}
	return nil
}

func (g *cognitoCliGroup) CreateClientTokenValidity(_ context.Context, t *harness.TestContext) error {
	poolName := fmt.Sprintf("compat-tv-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "create-user-pool", "--pool-name", poolName)
	if err != nil {
		return err
	}
	pool, _ := out["UserPool"].(map[string]interface{})
	poolID, _ := pool["Id"].(string)
	if poolID == "" {
		return fmt.Errorf("CreateClientTokenValidity: missing pool Id")
	}
	t.Set("tv_pool_id", poolID)

	out, err = awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "create-user-pool-client",
		"--user-pool-id", poolID,
		"--client-name", fmt.Sprintf("compat-client-%s", t.RunID),
		"--access-token-validity", "2",
		"--id-token-validity", "3",
		"--refresh-token-validity", "7",
		"--token-validity-units", `{"AccessToken":"hours","IdToken":"hours","RefreshToken":"days"}`,
	)
	if err != nil {
		return err
	}
	client, _ := out["UserPoolClient"].(map[string]interface{})
	clientID, _ := client["ClientId"].(string)
	if clientID == "" {
		return fmt.Errorf("CreateClientTokenValidity: missing ClientId")
	}
	t.Set("tv_client_id", clientID)
	return nil
}

func (g *cognitoCliGroup) DescribeClientTokenValidity(_ context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return fmt.Errorf("DescribeClientTokenValidity: missing pool/client from create")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "describe-user-pool-client",
		"--user-pool-id", poolID,
		"--client-id", clientID,
	)
	if err != nil {
		return err
	}
	client, _ := out["UserPoolClient"].(map[string]interface{})
	if client == nil {
		return fmt.Errorf("DescribeClientTokenValidity: missing UserPoolClient")
	}
	return nil
}

func (g *cognitoCliGroup) UpdateClientTokenValidity(_ context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return fmt.Errorf("UpdateClientTokenValidity: missing pool/client from create")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cognito-idp", "update-user-pool-client",
		"--user-pool-id", poolID,
		"--client-id", clientID,
		"--access-token-validity", "30",
		"--token-validity-units", `{"AccessToken":"minutes","IdToken":"hours","RefreshToken":"days"}`,
	)
	if err != nil {
		return err
	}
	client, _ := out["UserPoolClient"].(map[string]interface{})
	if client == nil {
		return fmt.Errorf("UpdateClientTokenValidity: missing UserPoolClient")
	}
	return nil
}

func (g *cognitoCliGroup) DeleteUserPoolClient(_ context.Context, t *harness.TestContext) error {
	poolID := t.GetString("tv_pool_id")
	clientID := t.GetString("tv_client_id")
	if poolID == "" || clientID == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cognito-idp", "delete-user-pool-client",
		"--user-pool-id", poolID,
		"--client-id", clientID,
	)
}
