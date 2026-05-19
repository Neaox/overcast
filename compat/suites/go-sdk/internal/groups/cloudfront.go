package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

func CloudFront(c *clients.Clients) ServiceGroup {
	g := &cloudfrontGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// Distributions
			"CreateDistribution": g.CreateDistribution,
			"GetDistribution":    g.GetDistribution,
			"ListDistributions":  g.ListDistributions,
			"DeleteDistribution": g.DeleteDistribution,
			// OAC
			"CreateOriginAccessControl": g.CreateOriginAccessControl,
			"GetOriginAccessControl":    g.GetOriginAccessControl,
			"UpdateOriginAccessControl": g.UpdateOriginAccessControl,
			"ListOriginAccessControls":  g.ListOriginAccessControls,
			"DeleteOriginAccessControl": g.DeleteOriginAccessControl,
			// Cache Policy
			"CreateCachePolicy":    g.CreateCachePolicy,
			"GetCachePolicy":       g.GetCachePolicy,
			"GetCachePolicyConfig": g.GetCachePolicyConfig,
			"UpdateCachePolicy":    g.UpdateCachePolicy,
			"ListCachePolicies":    g.ListCachePolicies,
			"DeleteCachePolicy":    g.DeleteCachePolicy,
			// Key Group
			"CreateKeyGroup":    g.CreateKeyGroup,
			"GetKeyGroup":       g.GetKeyGroup,
			"GetKeyGroupConfig": g.GetKeyGroupConfig,
			"UpdateKeyGroup":    g.UpdateKeyGroup,
			"ListKeyGroups":     g.ListKeyGroups,
			"DeleteKeyGroup":    g.DeleteKeyGroup,
			// Realtime Log
			"CreateRealtimeLogConfig": g.CreateRealtimeLogConfig,
			"GetRealtimeLogConfig":    g.GetRealtimeLogConfig,
			"UpdateRealtimeLogConfig": g.UpdateRealtimeLogConfig,
			"ListRealtimeLogConfigs":  g.ListRealtimeLogConfigs,
			"DeleteRealtimeLogConfig": g.DeleteRealtimeLogConfig,
			// Monitoring
			"CreateMonitoringSubscription": g.CreateMonitoringSubscription,
			"GetMonitoringSubscription":    g.GetMonitoringSubscription,
			"DeleteMonitoringSubscription": g.DeleteMonitoringSubscription,
			// FLE Config
			"CreateFieldLevelEncryptionConfig": g.CreateFieldLevelEncryptionConfig,
			"GetFieldLevelEncryption":          g.GetFieldLevelEncryption,
			"GetFieldLevelEncryptionConfig":    g.GetFieldLevelEncryptionConfig,
			"UpdateFieldLevelEncryptionConfig": g.UpdateFieldLevelEncryptionConfig,
			"ListFieldLevelEncryptionConfigs":  g.ListFieldLevelEncryptionConfigs,
			"DeleteFieldLevelEncryption":       g.DeleteFieldLevelEncryption,
			// FLE Profile
			"CreateFieldLevelEncryptionProfile":    g.CreateFieldLevelEncryptionProfile,
			"GetFieldLevelEncryptionProfile":       g.GetFieldLevelEncryptionProfile,
			"GetFieldLevelEncryptionProfileConfig": g.GetFieldLevelEncryptionProfileConfig,
			"UpdateFieldLevelEncryptionProfile":    g.UpdateFieldLevelEncryptionProfile,
			"ListFieldLevelEncryptionProfiles":     g.ListFieldLevelEncryptionProfiles,
			"DeleteFieldLevelEncryptionProfile":    g.DeleteFieldLevelEncryptionProfile,
			// Continuous Deployment
			"CreateContinuousDeploymentPolicy":    g.CreateContinuousDeploymentPolicy,
			"GetContinuousDeploymentPolicy":       g.GetContinuousDeploymentPolicy,
			"GetContinuousDeploymentPolicyConfig": g.GetContinuousDeploymentPolicyConfig,
			"UpdateContinuousDeploymentPolicy":    g.UpdateContinuousDeploymentPolicy,
			"ListContinuousDeploymentPolicies":    g.ListContinuousDeploymentPolicies,
			"DeleteContinuousDeploymentPolicy":    g.DeleteContinuousDeploymentPolicy,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cloudfront-distributions":         g.noop,
			"cloudfront-oac":                   g.noop,
			"cloudfront-cache-policy":          g.noop,
			"cloudfront-key-group":             g.noop,
			"cloudfront-realtime-log":          g.noop,
			"cloudfront-monitoring":            g.setupMonitoring,
			"cloudfront-fle-config":            g.noop,
			"cloudfront-fle-profile":           g.noop,
			"cloudfront-continuous-deployment": g.noop,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"cloudfront-distributions":         g.teardownDistributions,
			"cloudfront-oac":                   g.teardownOAC,
			"cloudfront-cache-policy":          g.teardownCachePolicy,
			"cloudfront-key-group":             g.teardownKeyGroup,
			"cloudfront-realtime-log":          g.teardownRealtimeLog,
			"cloudfront-monitoring":            g.teardownMonitoring,
			"cloudfront-fle-config":            g.teardownFLEConfig,
			"cloudfront-fle-profile":           g.teardownFLEProfile,
			"cloudfront-continuous-deployment": g.teardownCDP,
		},
	}
}

type cloudfrontGroup struct{ c *clients.Clients }

func (g *cloudfrontGroup) cl() *cloudfront.Client                               { return g.c.CloudFront() }
func (g *cloudfrontGroup) noop(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *cloudfrontGroup) distroConfig(ref string) *cftypes.DistributionConfig {
	return &cftypes.DistributionConfig{
		CallerReference: aws.String(ref),
		Comment:         aws.String("compat test distribution"),
		Enabled:         aws.Bool(true),
		Origins: &cftypes.Origins{
			Quantity: aws.Int32(1),
			Items: []cftypes.Origin{{
				Id:             aws.String("origin-1"),
				DomainName:     aws.String("example.com"),
				S3OriginConfig: &cftypes.S3OriginConfig{OriginAccessIdentity: aws.String("")},
			}},
		},
		DefaultCacheBehavior: &cftypes.DefaultCacheBehavior{
			TargetOriginId:       aws.String("origin-1"),
			ViewerProtocolPolicy: cftypes.ViewerProtocolPolicyRedirectToHttps,
			ForwardedValues: &cftypes.ForwardedValues{
				QueryString: aws.Bool(false),
				Cookies:     &cftypes.CookiePreference{Forward: cftypes.ItemSelectionNone},
			},
			MinTTL:         aws.Int64(0),
			TrustedSigners: &cftypes.TrustedSigners{Enabled: aws.Bool(false), Quantity: aws.Int32(0)},
		},
	}
}

// ── Distributions ────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownDistributions(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cf_distro_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	cfg := get.Distribution.DistributionConfig
	if aws.ToBool(cfg.Enabled) {
		cfg.Enabled = aws.Bool(false)
		upd, err := g.cl().UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
			Id: aws.String(id), IfMatch: get.ETag, DistributionConfig: cfg,
		})
		if err != nil {
			return nil
		}
		g.cl().DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: aws.String(id), IfMatch: upd.ETag}) //nolint:errcheck
		return nil
	}
	g.cl().DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateDistribution(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateDistribution(ctx, &cloudfront.CreateDistributionInput{
		DistributionConfig: g.distroConfig(fmt.Sprintf("compat-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	if resp.Distribution == nil || resp.Distribution.Id == nil {
		return fmt.Errorf("CreateDistribution: missing Id")
	}
	t.Set("cf_distro_id", *resp.Distribution.Id)
	if resp.ETag != nil {
		t.Set("cf_distro_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetDistribution(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cf_distro_id")
	if id == "" {
		return fmt.Errorf("GetDistribution: no distribution from CreateDistribution")
	}
	resp, err := g.cl().GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.Distribution == nil || resp.Distribution.Id == nil {
		return fmt.Errorf("GetDistribution: missing Id")
	}
	return nil
}

func (g *cloudfrontGroup) ListDistributions(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListDistributions(ctx, &cloudfront.ListDistributionsInput{})
	return err
}

func (g *cloudfrontGroup) DeleteDistribution(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cf_distro_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	cfg := get.Distribution.DistributionConfig
	cfg.Enabled = aws.Bool(false)
	upd, err := g.cl().UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
		Id: aws.String(id), IfMatch: get.ETag, DistributionConfig: cfg,
	})
	if err != nil {
		return err
	}
	_, err = g.cl().DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: aws.String(id), IfMatch: upd.ETag})
	return err
}

// ── OAC ──────────────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownOAC(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetOriginAccessControl(ctx, &cloudfront.GetOriginAccessControlInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteOriginAccessControl(ctx, &cloudfront.DeleteOriginAccessControlInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateOriginAccessControl(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateOriginAccessControl(ctx, &cloudfront.CreateOriginAccessControlInput{
		OriginAccessControlConfig: &cftypes.OriginAccessControlConfig{
			Name:                          aws.String(fmt.Sprintf("oc-oac-%s", t.RunID)),
			OriginAccessControlOriginType: cftypes.OriginAccessControlOriginTypesS3,
			SigningBehavior:               cftypes.OriginAccessControlSigningBehaviorsAlways,
			SigningProtocol:               cftypes.OriginAccessControlSigningProtocolsSigv4,
		},
	})
	if err != nil {
		return err
	}
	if resp.OriginAccessControl == nil || resp.OriginAccessControl.Id == nil {
		return fmt.Errorf("CreateOriginAccessControl: missing Id")
	}
	t.Set("oac_id", *resp.OriginAccessControl.Id)
	if resp.ETag != nil {
		t.Set("oac_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetOriginAccessControl(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	if id == "" {
		return fmt.Errorf("GetOriginAccessControl: no OAC from Create")
	}
	resp, err := g.cl().GetOriginAccessControl(ctx, &cloudfront.GetOriginAccessControlInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.OriginAccessControl == nil {
		return fmt.Errorf("GetOriginAccessControl: missing OriginAccessControl")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateOriginAccessControl(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	etag := t.GetString("oac_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateOriginAccessControl: missing prerequisite")
	}
	resp, err := g.cl().UpdateOriginAccessControl(ctx, &cloudfront.UpdateOriginAccessControlInput{
		Id:      aws.String(id),
		IfMatch: aws.String(etag),
		OriginAccessControlConfig: &cftypes.OriginAccessControlConfig{
			Name:                          aws.String(fmt.Sprintf("oc-oac-%s", t.RunID)),
			OriginAccessControlOriginType: cftypes.OriginAccessControlOriginTypesS3,
			SigningBehavior:               cftypes.OriginAccessControlSigningBehaviorsNever,
			SigningProtocol:               cftypes.OriginAccessControlSigningProtocolsSigv4,
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("oac_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListOriginAccessControls(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListOriginAccessControls(ctx, &cloudfront.ListOriginAccessControlsInput{})
	return err
}

func (g *cloudfrontGroup) DeleteOriginAccessControl(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	etag := t.GetString("oac_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteOriginAccessControl(ctx, &cloudfront.DeleteOriginAccessControlInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
	})
	return err
}

// ── Cache Policy ─────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownCachePolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetCachePolicy(ctx, &cloudfront.GetCachePolicyInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteCachePolicy(ctx, &cloudfront.DeleteCachePolicyInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateCachePolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateCachePolicy(ctx, &cloudfront.CreateCachePolicyInput{
		CachePolicyConfig: &cftypes.CachePolicyConfig{
			Name:       aws.String(fmt.Sprintf("oc-cp-%s", t.RunID)),
			MinTTL:     aws.Int64(0),
			DefaultTTL: aws.Int64(86400),
			MaxTTL:     aws.Int64(31536000),
			ParametersInCacheKeyAndForwardedToOrigin: &cftypes.ParametersInCacheKeyAndForwardedToOrigin{
				CookiesConfig:            &cftypes.CachePolicyCookiesConfig{CookieBehavior: cftypes.CachePolicyCookieBehaviorNone},
				EnableAcceptEncodingGzip: aws.Bool(false),
				HeadersConfig:            &cftypes.CachePolicyHeadersConfig{HeaderBehavior: cftypes.CachePolicyHeaderBehaviorNone},
				QueryStringsConfig:       &cftypes.CachePolicyQueryStringsConfig{QueryStringBehavior: cftypes.CachePolicyQueryStringBehaviorNone},
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.CachePolicy == nil || resp.CachePolicy.Id == nil {
		return fmt.Errorf("CreateCachePolicy: missing Id")
	}
	t.Set("cp_id", *resp.CachePolicy.Id)
	if resp.ETag != nil {
		t.Set("cp_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetCachePolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return fmt.Errorf("GetCachePolicy: no cp_id")
	}
	_, err := g.cl().GetCachePolicy(ctx, &cloudfront.GetCachePolicyInput{Id: aws.String(id)})
	return err
}

func (g *cloudfrontGroup) GetCachePolicyConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return fmt.Errorf("GetCachePolicyConfig: no cp_id")
	}
	resp, err := g.cl().GetCachePolicyConfig(ctx, &cloudfront.GetCachePolicyConfigInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.CachePolicyConfig == nil {
		return fmt.Errorf("GetCachePolicyConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateCachePolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	etag := t.GetString("cp_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateCachePolicy: missing prerequisite")
	}
	resp, err := g.cl().UpdateCachePolicy(ctx, &cloudfront.UpdateCachePolicyInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
		CachePolicyConfig: &cftypes.CachePolicyConfig{
			Name:       aws.String(fmt.Sprintf("oc-cp-%s", t.RunID)),
			MinTTL:     aws.Int64(0),
			DefaultTTL: aws.Int64(3600),
			MaxTTL:     aws.Int64(86400),
			ParametersInCacheKeyAndForwardedToOrigin: &cftypes.ParametersInCacheKeyAndForwardedToOrigin{
				CookiesConfig:            &cftypes.CachePolicyCookiesConfig{CookieBehavior: cftypes.CachePolicyCookieBehaviorNone},
				EnableAcceptEncodingGzip: aws.Bool(true),
				HeadersConfig:            &cftypes.CachePolicyHeadersConfig{HeaderBehavior: cftypes.CachePolicyHeaderBehaviorNone},
				QueryStringsConfig:       &cftypes.CachePolicyQueryStringsConfig{QueryStringBehavior: cftypes.CachePolicyQueryStringBehaviorNone},
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("cp_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListCachePolicies(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListCachePolicies(ctx, &cloudfront.ListCachePoliciesInput{})
	return err
}

func (g *cloudfrontGroup) DeleteCachePolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	etag := t.GetString("cp_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteCachePolicy(ctx, &cloudfront.DeleteCachePolicyInput{Id: aws.String(id), IfMatch: aws.String(etag)})
	return err
}

// ── Key Group ────────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownKeyGroup(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetKeyGroup(ctx, &cloudfront.GetKeyGroupInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteKeyGroup(ctx, &cloudfront.DeleteKeyGroupInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateKeyGroup(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateKeyGroup(ctx, &cloudfront.CreateKeyGroupInput{
		KeyGroupConfig: &cftypes.KeyGroupConfig{
			Name:  aws.String(fmt.Sprintf("oc-kg-%s", t.RunID)),
			Items: []string{"K1234567890ABCDE"},
		},
	})
	if err != nil {
		return err
	}
	if resp.KeyGroup == nil || resp.KeyGroup.Id == nil {
		return fmt.Errorf("CreateKeyGroup: missing Id")
	}
	t.Set("kg_id", *resp.KeyGroup.Id)
	if resp.ETag != nil {
		t.Set("kg_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetKeyGroup(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return fmt.Errorf("GetKeyGroup: no kg_id")
	}
	_, err := g.cl().GetKeyGroup(ctx, &cloudfront.GetKeyGroupInput{Id: aws.String(id)})
	return err
}

func (g *cloudfrontGroup) GetKeyGroupConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return fmt.Errorf("GetKeyGroupConfig: no kg_id")
	}
	resp, err := g.cl().GetKeyGroupConfig(ctx, &cloudfront.GetKeyGroupConfigInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.KeyGroupConfig == nil {
		return fmt.Errorf("GetKeyGroupConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateKeyGroup(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	etag := t.GetString("kg_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateKeyGroup: missing prerequisite")
	}
	resp, err := g.cl().UpdateKeyGroup(ctx, &cloudfront.UpdateKeyGroupInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
		KeyGroupConfig: &cftypes.KeyGroupConfig{
			Name:    aws.String(fmt.Sprintf("oc-kg-%s", t.RunID)),
			Comment: aws.String("updated"),
			Items:   []string{"K1234567890ABCDE"},
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("kg_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListKeyGroups(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListKeyGroups(ctx, &cloudfront.ListKeyGroupsInput{})
	return err
}

func (g *cloudfrontGroup) DeleteKeyGroup(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	etag := t.GetString("kg_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteKeyGroup(ctx, &cloudfront.DeleteKeyGroupInput{Id: aws.String(id), IfMatch: aws.String(etag)})
	return err
}

// ── Realtime Log ─────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownRealtimeLog(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return nil
	}
	g.cl().DeleteRealtimeLogConfig(ctx, &cloudfront.DeleteRealtimeLogConfigInput{Name: aws.String(name)}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateRealtimeLogConfig(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-rlc-%s", t.RunID)
	resp, err := g.cl().CreateRealtimeLogConfig(ctx, &cloudfront.CreateRealtimeLogConfigInput{
		Name:         aws.String(name),
		SamplingRate: aws.Int64(100),
		EndPoints: []cftypes.EndPoint{{
			StreamType: aws.String("Kinesis"),
			KinesisStreamConfig: &cftypes.KinesisStreamConfig{
				RoleARN:   aws.String("arn:aws:iam::000000000000:role/test"),
				StreamARN: aws.String("arn:aws:kinesis:us-east-1:000000000000:stream/test"),
			},
		}},
		Fields: []string{"timestamp", "c-ip"},
	})
	if err != nil {
		return err
	}
	if resp.RealtimeLogConfig == nil || resp.RealtimeLogConfig.ARN == nil {
		return fmt.Errorf("CreateRealtimeLogConfig: missing ARN")
	}
	t.Set("rlc_name", name)
	t.Set("rlc_arn", *resp.RealtimeLogConfig.ARN)
	return nil
}

func (g *cloudfrontGroup) GetRealtimeLogConfig(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return fmt.Errorf("GetRealtimeLogConfig: no rlc_name")
	}
	resp, err := g.cl().GetRealtimeLogConfig(ctx, &cloudfront.GetRealtimeLogConfigInput{Name: aws.String(name)})
	if err != nil {
		return err
	}
	if resp.RealtimeLogConfig == nil {
		return fmt.Errorf("GetRealtimeLogConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateRealtimeLogConfig(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	arn := t.GetString("rlc_arn")
	if name == "" || arn == "" {
		return fmt.Errorf("UpdateRealtimeLogConfig: missing prerequisite")
	}
	_, err := g.cl().UpdateRealtimeLogConfig(ctx, &cloudfront.UpdateRealtimeLogConfigInput{
		Name: aws.String(name), ARN: aws.String(arn),
		SamplingRate: aws.Int64(50),
		EndPoints: []cftypes.EndPoint{{
			StreamType: aws.String("Kinesis"),
			KinesisStreamConfig: &cftypes.KinesisStreamConfig{
				RoleARN:   aws.String("arn:aws:iam::000000000000:role/test"),
				StreamARN: aws.String("arn:aws:kinesis:us-east-1:000000000000:stream/test"),
			},
		}},
		Fields: []string{"timestamp", "c-ip", "sc-status"},
	})
	return err
}

func (g *cloudfrontGroup) ListRealtimeLogConfigs(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListRealtimeLogConfigs(ctx, &cloudfront.ListRealtimeLogConfigsInput{})
	return err
}

func (g *cloudfrontGroup) DeleteRealtimeLogConfig(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return nil
	}
	_, err := g.cl().DeleteRealtimeLogConfig(ctx, &cloudfront.DeleteRealtimeLogConfigInput{Name: aws.String(name)})
	return err
}

// ── Monitoring ───────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) setupMonitoring(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateDistribution(ctx, &cloudfront.CreateDistributionInput{
		DistributionConfig: g.distroConfig(fmt.Sprintf("compat-mon-%s", t.RunID)),
	})
	if err != nil {
		return err
	}
	if resp.Distribution == nil || resp.Distribution.Id == nil {
		return fmt.Errorf("setupMonitoring: missing distribution Id")
	}
	t.Set("mon_distro_id", *resp.Distribution.Id)
	return nil
}

func (g *cloudfrontGroup) teardownMonitoring(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return nil
	}
	g.cl().DeleteMonitoringSubscription(ctx, &cloudfront.DeleteMonitoringSubscriptionInput{DistributionId: aws.String(id)}) //nolint:errcheck
	get, err := g.cl().GetDistribution(ctx, &cloudfront.GetDistributionInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	cfg := get.Distribution.DistributionConfig
	cfg.Enabled = aws.Bool(false)
	upd, err := g.cl().UpdateDistribution(ctx, &cloudfront.UpdateDistributionInput{
		Id: aws.String(id), IfMatch: get.ETag, DistributionConfig: cfg,
	})
	if err != nil {
		return nil
	}
	g.cl().DeleteDistribution(ctx, &cloudfront.DeleteDistributionInput{Id: aws.String(id), IfMatch: upd.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateMonitoringSubscription(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return fmt.Errorf("CreateMonitoringSubscription: no mon_distro_id")
	}
	_, err := g.cl().CreateMonitoringSubscription(ctx, &cloudfront.CreateMonitoringSubscriptionInput{
		DistributionId: aws.String(id),
		MonitoringSubscription: &cftypes.MonitoringSubscription{
			RealtimeMetricsSubscriptionConfig: &cftypes.RealtimeMetricsSubscriptionConfig{
				RealtimeMetricsSubscriptionStatus: cftypes.RealtimeMetricsSubscriptionStatusEnabled,
			},
		},
	})
	return err
}

func (g *cloudfrontGroup) GetMonitoringSubscription(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return fmt.Errorf("GetMonitoringSubscription: no mon_distro_id")
	}
	resp, err := g.cl().GetMonitoringSubscription(ctx, &cloudfront.GetMonitoringSubscriptionInput{
		DistributionId: aws.String(id),
	})
	if err != nil {
		return err
	}
	if resp.MonitoringSubscription == nil {
		return fmt.Errorf("GetMonitoringSubscription: missing subscription")
	}
	return nil
}

func (g *cloudfrontGroup) DeleteMonitoringSubscription(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return nil
	}
	_, err := g.cl().DeleteMonitoringSubscription(ctx, &cloudfront.DeleteMonitoringSubscriptionInput{
		DistributionId: aws.String(id),
	})
	return err
}

// ── FLE Config ───────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownFLEConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetFieldLevelEncryption(ctx, &cloudfront.GetFieldLevelEncryptionInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteFieldLevelEncryptionConfig(ctx, &cloudfront.DeleteFieldLevelEncryptionConfigInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateFieldLevelEncryptionConfig(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateFieldLevelEncryptionConfig(ctx, &cloudfront.CreateFieldLevelEncryptionConfigInput{
		FieldLevelEncryptionConfig: &cftypes.FieldLevelEncryptionConfig{
			CallerReference: aws.String(fmt.Sprintf("oc-fle-%s", t.RunID)),
			Comment:         aws.String("compat test"),
		},
	})
	if err != nil {
		return err
	}
	if resp.FieldLevelEncryption == nil || resp.FieldLevelEncryption.Id == nil {
		return fmt.Errorf("CreateFieldLevelEncryptionConfig: missing Id")
	}
	t.Set("fle_id", *resp.FieldLevelEncryption.Id)
	if resp.ETag != nil {
		t.Set("fle_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetFieldLevelEncryption(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryption: no fle_id")
	}
	_, err := g.cl().GetFieldLevelEncryption(ctx, &cloudfront.GetFieldLevelEncryptionInput{Id: aws.String(id)})
	return err
}

func (g *cloudfrontGroup) GetFieldLevelEncryptionConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionConfig: no fle_id")
	}
	resp, err := g.cl().GetFieldLevelEncryptionConfig(ctx, &cloudfront.GetFieldLevelEncryptionConfigInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.FieldLevelEncryptionConfig == nil {
		return fmt.Errorf("GetFieldLevelEncryptionConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateFieldLevelEncryptionConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	etag := t.GetString("fle_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateFieldLevelEncryptionConfig: missing prerequisite")
	}
	resp, err := g.cl().UpdateFieldLevelEncryptionConfig(ctx, &cloudfront.UpdateFieldLevelEncryptionConfigInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
		FieldLevelEncryptionConfig: &cftypes.FieldLevelEncryptionConfig{
			CallerReference: aws.String(fmt.Sprintf("oc-fle-%s", t.RunID)),
			Comment:         aws.String("compat test updated"),
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("fle_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListFieldLevelEncryptionConfigs(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListFieldLevelEncryptionConfigs(ctx, &cloudfront.ListFieldLevelEncryptionConfigsInput{})
	return err
}

func (g *cloudfrontGroup) DeleteFieldLevelEncryption(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	etag := t.GetString("fle_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteFieldLevelEncryptionConfig(ctx, &cloudfront.DeleteFieldLevelEncryptionConfigInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
	})
	return err
}

// ── FLE Profile ──────────────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownFLEProfile(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetFieldLevelEncryptionProfile(ctx, &cloudfront.GetFieldLevelEncryptionProfileInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteFieldLevelEncryptionProfile(ctx, &cloudfront.DeleteFieldLevelEncryptionProfileInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateFieldLevelEncryptionProfile(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateFieldLevelEncryptionProfile(ctx, &cloudfront.CreateFieldLevelEncryptionProfileInput{
		FieldLevelEncryptionProfileConfig: &cftypes.FieldLevelEncryptionProfileConfig{
			CallerReference:    aws.String(fmt.Sprintf("oc-flep-%s", t.RunID)),
			Name:               aws.String(fmt.Sprintf("oc-flep-%s", t.RunID)),
			Comment:            aws.String("compat test"),
			EncryptionEntities: &cftypes.EncryptionEntities{Quantity: aws.Int32(0), Items: []cftypes.EncryptionEntity{}},
		},
	})
	if err != nil {
		return err
	}
	if resp.FieldLevelEncryptionProfile == nil || resp.FieldLevelEncryptionProfile.Id == nil {
		return fmt.Errorf("CreateFieldLevelEncryptionProfile: missing Id")
	}
	t.Set("flep_id", *resp.FieldLevelEncryptionProfile.Id)
	if resp.ETag != nil {
		t.Set("flep_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetFieldLevelEncryptionProfile(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionProfile: no flep_id")
	}
	_, err := g.cl().GetFieldLevelEncryptionProfile(ctx, &cloudfront.GetFieldLevelEncryptionProfileInput{Id: aws.String(id)})
	return err
}

func (g *cloudfrontGroup) GetFieldLevelEncryptionProfileConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionProfileConfig: no flep_id")
	}
	resp, err := g.cl().GetFieldLevelEncryptionProfileConfig(ctx, &cloudfront.GetFieldLevelEncryptionProfileConfigInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.FieldLevelEncryptionProfileConfig == nil {
		return fmt.Errorf("GetFieldLevelEncryptionProfileConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateFieldLevelEncryptionProfile(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	etag := t.GetString("flep_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateFieldLevelEncryptionProfile: missing prerequisite")
	}
	resp, err := g.cl().UpdateFieldLevelEncryptionProfile(ctx, &cloudfront.UpdateFieldLevelEncryptionProfileInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
		FieldLevelEncryptionProfileConfig: &cftypes.FieldLevelEncryptionProfileConfig{
			CallerReference:    aws.String(fmt.Sprintf("oc-flep-%s", t.RunID)),
			Name:               aws.String(fmt.Sprintf("oc-flep-%s", t.RunID)),
			Comment:            aws.String("compat test updated"),
			EncryptionEntities: &cftypes.EncryptionEntities{Quantity: aws.Int32(0), Items: []cftypes.EncryptionEntity{}},
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("flep_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListFieldLevelEncryptionProfiles(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListFieldLevelEncryptionProfiles(ctx, &cloudfront.ListFieldLevelEncryptionProfilesInput{})
	return err
}

func (g *cloudfrontGroup) DeleteFieldLevelEncryptionProfile(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	etag := t.GetString("flep_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteFieldLevelEncryptionProfile(ctx, &cloudfront.DeleteFieldLevelEncryptionProfileInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
	})
	return err
}

// ── Continuous Deployment ────────────────────────────────────────────────────

func (g *cloudfrontGroup) teardownCDP(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return nil
	}
	get, err := g.cl().GetContinuousDeploymentPolicy(ctx, &cloudfront.GetContinuousDeploymentPolicyInput{Id: aws.String(id)})
	if err != nil {
		return nil
	}
	g.cl().DeleteContinuousDeploymentPolicy(ctx, &cloudfront.DeleteContinuousDeploymentPolicyInput{Id: aws.String(id), IfMatch: get.ETag}) //nolint:errcheck
	return nil
}

func (g *cloudfrontGroup) CreateContinuousDeploymentPolicy(ctx context.Context, t *harness.TestContext) error {
	resp, err := g.cl().CreateContinuousDeploymentPolicy(ctx, &cloudfront.CreateContinuousDeploymentPolicyInput{
		ContinuousDeploymentPolicyConfig: &cftypes.ContinuousDeploymentPolicyConfig{
			StagingDistributionDnsNames: &cftypes.StagingDistributionDnsNames{
				Quantity: aws.Int32(1),
				Items:    []string{"d1234.cloudfront.net"},
			},
			Enabled: aws.Bool(true),
		},
	})
	if err != nil {
		return err
	}
	if resp.ContinuousDeploymentPolicy == nil || resp.ContinuousDeploymentPolicy.Id == nil {
		return fmt.Errorf("CreateContinuousDeploymentPolicy: missing Id")
	}
	t.Set("cdp_id", *resp.ContinuousDeploymentPolicy.Id)
	if resp.ETag != nil {
		t.Set("cdp_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) GetContinuousDeploymentPolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return fmt.Errorf("GetContinuousDeploymentPolicy: no cdp_id")
	}
	_, err := g.cl().GetContinuousDeploymentPolicy(ctx, &cloudfront.GetContinuousDeploymentPolicyInput{Id: aws.String(id)})
	return err
}

func (g *cloudfrontGroup) GetContinuousDeploymentPolicyConfig(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return fmt.Errorf("GetContinuousDeploymentPolicyConfig: no cdp_id")
	}
	resp, err := g.cl().GetContinuousDeploymentPolicyConfig(ctx, &cloudfront.GetContinuousDeploymentPolicyConfigInput{Id: aws.String(id)})
	if err != nil {
		return err
	}
	if resp.ContinuousDeploymentPolicyConfig == nil {
		return fmt.Errorf("GetContinuousDeploymentPolicyConfig: missing config")
	}
	return nil
}

func (g *cloudfrontGroup) UpdateContinuousDeploymentPolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	etag := t.GetString("cdp_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateContinuousDeploymentPolicy: missing prerequisite")
	}
	resp, err := g.cl().UpdateContinuousDeploymentPolicy(ctx, &cloudfront.UpdateContinuousDeploymentPolicyInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
		ContinuousDeploymentPolicyConfig: &cftypes.ContinuousDeploymentPolicyConfig{
			StagingDistributionDnsNames: &cftypes.StagingDistributionDnsNames{
				Quantity: aws.Int32(1),
				Items:    []string{"d5678.cloudfront.net"},
			},
			Enabled: aws.Bool(false),
		},
	})
	if err != nil {
		return err
	}
	if resp.ETag != nil {
		t.Set("cdp_etag", *resp.ETag)
	}
	return nil
}

func (g *cloudfrontGroup) ListContinuousDeploymentPolicies(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListContinuousDeploymentPolicies(ctx, &cloudfront.ListContinuousDeploymentPoliciesInput{})
	return err
}

func (g *cloudfrontGroup) DeleteContinuousDeploymentPolicy(ctx context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	etag := t.GetString("cdp_etag")
	if id == "" || etag == "" {
		return nil
	}
	_, err := g.cl().DeleteContinuousDeploymentPolicy(ctx, &cloudfront.DeleteContinuousDeploymentPolicyInput{
		Id: aws.String(id), IfMatch: aws.String(etag),
	})
	return err
}
