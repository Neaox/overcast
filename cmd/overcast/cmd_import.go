package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/spf13/cobra"
)

func newImportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import resources from real AWS into Overcast",
	}
	cmd.AddCommand(newImportCognitoUsersCmd())
	return cmd
}

func newImportCognitoUsersCmd() *cobra.Command {
	var (
		fromProfile string
		fromRegion  string
		fromPoolID  string
		toPoolID    string
		userSub     string
		maxUsers    int
		batchSize   int
	)

	cmd := &cobra.Command{
		Use:   "cognito-users",
		Short: "Import Cognito users from a real AWS user pool into Overcast",
		Long: `Fetches users from a real AWS Cognito user pool and imports them
into an Overcast user pool. Imported users are placed in FORCE_CHANGE_PASSWORD
status because their passwords cannot be extracted from AWS.

Users are sent to the server in batches (default 100 per batch). Each batch
completes before the next begins, keeping individual requests lightweight.

By default all users are imported. Use --user to import a single user by sub.`,
		Example: `  overcast import cognito-users --from-pool-id us-east-1_abc123 --to-pool-id us-east-1_abc123
  overcast import cognito-users --from-pool-id us-east-1_abc123 --to-pool-id us-east-1_def456 --user a1b2c3d4-...
  overcast import cognito-users --from-pool-id us-east-1_abc123 --to-pool-id us-east-1_def456 --batch-size 50`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, _ := cmd.Flags().GetString("endpoint")

			if fromPoolID == "" {
				return fmt.Errorf("--from-pool-id is required")
			}
			if toPoolID == "" {
				return fmt.Errorf("--to-pool-id is required")
			}

			ctx := cmd.Context()

			cfg, err := loadAWSConfig(ctx, fromRegion, fromProfile)
			if err != nil {
				return fmt.Errorf("loading AWS config: %w", err)
			}
			client := cognitoidentityprovider.NewFromConfig(cfg)

			entries, err := fetchUsers(ctx, client, fromPoolID, userSub, maxUsers)
			if err != nil {
				return fmt.Errorf("fetching users: %w", err)
			}

			if len(entries) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No users to import.\n")
				return nil
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Importing %d users in batches of %d...\n",
				len(entries), batchSize)

			aggregate := importResult{}
			offset := 0
			for offset < len(entries) {
				end := offset + batchSize
				if end > len(entries) {
					end = len(entries)
				}
				batch := entries[offset:end]

				result, err := postImport(ctx, endpoint, toPoolID, batch)
				if err != nil {
					return fmt.Errorf("batch %d-%d: %w", offset, end, err)
				}
				for i := range result.Errors {
					result.Errors[i].Index += offset
				}

				aggregate.Imported += result.Imported
				aggregate.Skipped += result.Skipped
				aggregate.Errors = append(aggregate.Errors, result.Errors...)

				fmt.Fprintf(cmd.OutOrStdout(), "  batch %d-%d: %d imported, %d skipped\n",
					offset, end, result.Imported, result.Skipped)
				offset = end
			}

			fmt.Fprintf(cmd.OutOrStdout(),
				"Import complete: %d imported, %d skipped.\n",
				aggregate.Imported, aggregate.Skipped)
			for _, e := range aggregate.Errors {
				fmt.Fprintf(cmd.OutOrStdout(),
					"  [%d] %s: %s\n", e.Index, e.Username, e.Reason)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&fromProfile, "from-profile", "", "AWS profile for the source account")
	cmd.Flags().StringVar(&fromRegion, "from-region", "", "AWS region for the source user pool (auto-detected if omitted)")
	cmd.Flags().StringVar(&fromPoolID, "from-pool-id", "", "Source user pool ID in real AWS (required)")
	cmd.Flags().StringVar(&toPoolID, "to-pool-id", "", "Target user pool ID in Overcast (required)")
	cmd.Flags().StringVar(&userSub, "user", "", "Import a single user by sub (UUID)")
	cmd.Flags().IntVar(&maxUsers, "max-users", 0, "Maximum users to import (0 = unlimited)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "Users per batch sent to the server")

	return cmd
}

func loadAWSConfig(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*config.LoadOptions) error{}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

type importEntry struct {
	Username   string            `json:"username"`
	Sub        string            `json:"sub"`
	Enabled    bool              `json:"enabled"`
	Status     string            `json:"status"`
	CreatedAt  time.Time         `json:"createdAt"`
	ModifiedAt time.Time         `json:"modifiedAt"`
	Attributes []importAttribute `json:"attributes"`
	Groups     []string          `json:"groups,omitempty"`
	MFAEnabled bool              `json:"mfaEnabled,omitempty"`
}

type importAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type importResult struct {
	Imported int         `json:"imported"`
	Skipped  int         `json:"skipped"`
	Errors   []importErr `json:"errors,omitempty"`
}

type importErr struct {
	Index    int    `json:"index"`
	Username string `json:"username"`
	Reason   string `json:"reason"`
}

func fetchUsers(ctx context.Context, client *cognitoidentityprovider.Client, poolID, userSub string, maxUsers int) ([]importEntry, error) {
	var entries []importEntry
	paginator := cognitoidentityprovider.NewListUsersPaginator(client, &cognitoidentityprovider.ListUsersInput{
		UserPoolId: aws.String(poolID),
	})

	if userSub != "" {
		paginator = cognitoidentityprovider.NewListUsersPaginator(client, &cognitoidentityprovider.ListUsersInput{
			UserPoolId: aws.String(poolID),
			Filter:     aws.String(`sub = "` + userSub + `"`),
		})
	}

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing users: %w", err)
		}
		for _, u := range page.Users {
			if maxUsers > 0 && len(entries) >= maxUsers {
				return entries, nil
			}
			entry, err := buildEntry(ctx, client, poolID, u)
			if err != nil {
				return nil, fmt.Errorf("building entry for %s: %w", aws.ToString(u.Username), err)
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func buildEntry(ctx context.Context, client *cognitoidentityprovider.Client, poolID string, u types.UserType) (importEntry, error) {
	entry := importEntry{
		Username: aws.ToString(u.Username),
		Enabled:  u.Enabled,
	}

	if u.UserCreateDate != nil {
		entry.CreatedAt = *u.UserCreateDate
	}
	if u.UserLastModifiedDate != nil {
		entry.ModifiedAt = *u.UserLastModifiedDate
	}
	if u.UserStatus != "" {
		entry.Status = string(u.UserStatus)
	}

	for _, attr := range u.Attributes {
		name := aws.ToString(attr.Name)
		value := aws.ToString(attr.Value)
		entry.Attributes = append(entry.Attributes, importAttribute{Name: name, Value: value})
		if name == "sub" {
			entry.Sub = value
		}
	}

	groups, err := client.AdminListGroupsForUser(ctx, &cognitoidentityprovider.AdminListGroupsForUserInput{
		UserPoolId: aws.String(poolID),
		Username:   u.Username,
	})
	if err == nil {
		for _, g := range groups.Groups {
			entry.Groups = append(entry.Groups, aws.ToString(g.GroupName))
		}
	}

	return entry, nil
}

func postImport(ctx context.Context, endpoint, poolID string, entries []importEntry) (importResult, error) {
	body := map[string]any{"users": entries}
	payload, err := json.Marshal(body)
	if err != nil {
		return importResult{}, fmt.Errorf("marshaling request: %w", err)
	}

	url := strings.TrimRight(endpoint, "/") + "/_overcast/cognito/user-pools/" + poolID + "/import-users"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return importResult{}, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return importResult{}, fmt.Errorf("posting to overcast at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return importResult{}, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return importResult{}, fmt.Errorf("overcast returned %s: %s", resp.Status, string(respBody))
	}

	var result importResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return importResult{}, fmt.Errorf("unmarshaling response: %w", err)
	}
	return result, nil
}
