package groups

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// CloudFront returns the CloudFront service group.
func CloudFront() ServiceGroup {
	g := &cfCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// Distributions
			"CreateDistribution": g.CreateDistribution,
			"GetDistribution":    g.GetDistribution,
			"ListDistributions":  g.ListDistributions,
			"DeleteDistribution": g.DeleteDistribution,
			// Origin Access Control
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
			// Realtime Log Config
			"CreateRealtimeLogConfig": g.CreateRealtimeLogConfig,
			"GetRealtimeLogConfig":    g.GetRealtimeLogConfig,
			"UpdateRealtimeLogConfig": g.UpdateRealtimeLogConfig,
			"ListRealtimeLogConfigs":  g.ListRealtimeLogConfigs,
			"DeleteRealtimeLogConfig": g.DeleteRealtimeLogConfig,
			// Monitoring
			"CreateMonitoringSubscription": g.CreateMonitoringSubscription,
			"GetMonitoringSubscription":    g.GetMonitoringSubscription,
			"DeleteMonitoringSubscription": g.DeleteMonitoringSubscription,
			// Field Level Encryption Config
			"CreateFieldLevelEncryptionConfig": g.CreateFieldLevelEncryptionConfig,
			"GetFieldLevelEncryption":          g.GetFieldLevelEncryption,
			"GetFieldLevelEncryptionConfig":    g.GetFieldLevelEncryptionConfig,
			"UpdateFieldLevelEncryptionConfig": g.UpdateFieldLevelEncryptionConfig,
			"ListFieldLevelEncryptionConfigs":  g.ListFieldLevelEncryptionConfigs,
			"DeleteFieldLevelEncryption":       g.DeleteFieldLevelEncryption,
			// Field Level Encryption Profile
			"CreateFieldLevelEncryptionProfile":    g.CreateFieldLevelEncryptionProfile,
			"GetFieldLevelEncryptionProfile":       g.GetFieldLevelEncryptionProfile,
			"GetFieldLevelEncryptionProfileConfig": g.GetFieldLevelEncryptionProfileConfig,
			"UpdateFieldLevelEncryptionProfile":    g.UpdateFieldLevelEncryptionProfile,
			"ListFieldLevelEncryptionProfiles":     g.ListFieldLevelEncryptionProfiles,
			"DeleteFieldLevelEncryptionProfile":    g.DeleteFieldLevelEncryptionProfile,
			// Continuous Deployment Policy
			"CreateContinuousDeploymentPolicy":    g.CreateContinuousDeploymentPolicy,
			"GetContinuousDeploymentPolicy":       g.GetContinuousDeploymentPolicy,
			"GetContinuousDeploymentPolicyConfig": g.GetContinuousDeploymentPolicyConfig,
			"UpdateContinuousDeploymentPolicy":    g.UpdateContinuousDeploymentPolicy,
			"ListContinuousDeploymentPolicies":    g.ListContinuousDeploymentPolicies,
			"DeleteContinuousDeploymentPolicy":    g.DeleteContinuousDeploymentPolicy,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"cloudfront-distributions":         g.setupNoop,
			"cloudfront-oac":                   g.setupNoop,
			"cloudfront-cache-policy":          g.setupNoop,
			"cloudfront-key-group":             g.setupNoop,
			"cloudfront-realtime-log":          g.setupNoop,
			"cloudfront-monitoring":            g.setupMonitoring,
			"cloudfront-fle-config":            g.setupNoop,
			"cloudfront-fle-profile":           g.setupNoop,
			"cloudfront-continuous-deployment": g.setupNoop,
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
			"cloudfront-continuous-deployment": g.teardownContinuousDeployment,
		},
	}
}

type cfCliGroup struct{}

func (g *cfCliGroup) setupNoop(_ context.Context, _ *harness.TestContext) error { return nil }

// ── Distributions ────────────────────────────────────────────────────────────

func (g *cfCliGroup) teardownDistributions(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("distro_id")
	if id == "" {
		return nil
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-distribution", "--id", id)
	if err != nil {
		return nil
	}
	etag, _ := out["ETag"].(string)
	distro, _ := out["Distribution"].(map[string]interface{})
	cfg, _ := distro["DistributionConfig"].(map[string]interface{})
	if enabled, _ := cfg["Enabled"].(bool); enabled {
		cfg["Enabled"] = false
		cfgBytes, err := json.Marshal(cfg)
		if err == nil {
			if upd, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-distribution",
				"--id", id, "--if-match", etag, "--distribution-config", string(cfgBytes),
			); err == nil {
				etag, _ = upd["ETag"].(string)
			}
		}
	}
	awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-distribution", //nolint:errcheck
		"--id", id, "--if-match", etag)
	return nil
}

func cfDistroConfig(callerRef string) string {
	return fmt.Sprintf(`{"CallerReference":"%s","Origins":{"Quantity":1,"Items":[{"Id":"default","DomainName":"example.com","CustomOriginConfig":{"HTTPPort":80,"HTTPSPort":443,"OriginProtocolPolicy":"https-only"}}]},"DefaultCacheBehavior":{"TargetOriginId":"default","ViewerProtocolPolicy":"redirect-to-https","TrustedSigners":{"Enabled":false,"Quantity":0},"ForwardedValues":{"QueryString":false,"Cookies":{"Forward":"none"}},"MinTTL":0},"Comment":"compat","Enabled":true}`, callerRef)
}

func (g *cfCliGroup) CreateDistribution(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-distribution",
		"--distribution-config", cfDistroConfig(t.RunID),
	)
	if err != nil {
		return err
	}
	distro, _ := out["Distribution"].(map[string]interface{})
	id, _ := distro["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateDistribution: missing Id")
	}
	etag, _ := out["ETag"].(string)
	t.Set("distro_id", id)
	t.Set("distro_etag", etag)
	return nil
}

func (g *cfCliGroup) GetDistribution(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("distro_id")
	if id == "" {
		return fmt.Errorf("GetDistribution: no distro_id from CreateDistribution")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-distribution", "--id", id)
	if err != nil {
		return err
	}
	if out["Distribution"] == nil {
		return fmt.Errorf("GetDistribution: missing Distribution")
	}
	return nil
}

func (g *cfCliGroup) ListDistributions(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-distributions")
	return err
}

func (g *cfCliGroup) DeleteDistribution(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("distro_id")
	if id == "" {
		return fmt.Errorf("DeleteDistribution: missing distro_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-distribution", "--id", id)
	if err != nil {
		return err
	}
	etag, _ := out["ETag"].(string)
	distro, _ := out["Distribution"].(map[string]interface{})
	cfg, _ := distro["DistributionConfig"].(map[string]interface{})
	cfg["Enabled"] = false
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	upd, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-distribution",
		"--id", id, "--if-match", etag, "--distribution-config", string(cfgBytes),
	)
	if err != nil {
		return err
	}
	newEtag, _ := upd["ETag"].(string)
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-distribution",
		"--id", id, "--if-match", newEtag)
}

// ── Origin Access Control ────────────────────────────────────────────────────

func (g *cfCliGroup) teardownOAC(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-origin-access-control", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-origin-access-control", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateOriginAccessControl(_ context.Context, t *harness.TestContext) error {
	cfg := fmt.Sprintf(`{"Name":"oc-oac-%s","OriginAccessControlOriginType":"s3","SigningBehavior":"always","SigningProtocol":"sigv4"}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-origin-access-control",
		"--origin-access-control-config", cfg)
	if err != nil {
		return err
	}
	oac, _ := out["OriginAccessControl"].(map[string]interface{})
	id, _ := oac["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateOriginAccessControl: missing Id")
	}
	t.Set("oac_id", id)
	t.Set("oac_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetOriginAccessControl(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	if id == "" {
		return fmt.Errorf("GetOriginAccessControl: no oac_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-origin-access-control", "--id", id)
	if err != nil {
		return err
	}
	if out["OriginAccessControl"] == nil {
		return fmt.Errorf("GetOriginAccessControl: missing OriginAccessControl")
	}
	return nil
}

func (g *cfCliGroup) UpdateOriginAccessControl(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	etag := t.GetString("oac_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateOriginAccessControl: missing prerequisite")
	}
	cfg := fmt.Sprintf(`{"Name":"oc-oac-%s","OriginAccessControlOriginType":"s3","SigningBehavior":"never","SigningProtocol":"sigv4"}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-origin-access-control",
		"--id", id, "--if-match", etag, "--origin-access-control-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("oac_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListOriginAccessControls(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-origin-access-controls")
	return err
}

func (g *cfCliGroup) DeleteOriginAccessControl(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("oac_id")
	etag := t.GetString("oac_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-origin-access-control",
		"--id", id, "--if-match", etag)
}

// ── Cache Policy ─────────────────────────────────────────────────────────────

func (g *cfCliGroup) teardownCachePolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-cache-policy", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-cache-policy", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateCachePolicy(_ context.Context, t *harness.TestContext) error {
	cfg := fmt.Sprintf(`{"Name":"oc-cp-%s","MinTTL":0,"DefaultTTL":86400,"MaxTTL":31536000,"ParametersInCacheKeyAndForwardedToOrigin":{"CookiesConfig":{"CookieBehavior":"none"},"EnableAcceptEncodingGzip":false,"HeadersConfig":{"HeaderBehavior":"none"},"QueryStringsConfig":{"QueryStringBehavior":"none"}}}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-cache-policy",
		"--cache-policy-config", cfg)
	if err != nil {
		return err
	}
	cp, _ := out["CachePolicy"].(map[string]interface{})
	id, _ := cp["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateCachePolicy: missing Id")
	}
	t.Set("cp_id", id)
	t.Set("cp_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetCachePolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return fmt.Errorf("GetCachePolicy: no cp_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-cache-policy", "--id", id)
	if err != nil {
		return err
	}
	if out["CachePolicy"] == nil {
		return fmt.Errorf("GetCachePolicy: missing CachePolicy")
	}
	return nil
}

func (g *cfCliGroup) GetCachePolicyConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	if id == "" {
		return fmt.Errorf("GetCachePolicyConfig: no cp_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-cache-policy-config", "--id", id)
	if err != nil {
		return err
	}
	if out["CachePolicyConfig"] == nil {
		return fmt.Errorf("GetCachePolicyConfig: missing CachePolicyConfig")
	}
	return nil
}

func (g *cfCliGroup) UpdateCachePolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	etag := t.GetString("cp_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateCachePolicy: missing prerequisite")
	}
	cfg := fmt.Sprintf(`{"Name":"oc-cp-%s","MinTTL":0,"DefaultTTL":3600,"MaxTTL":86400,"ParametersInCacheKeyAndForwardedToOrigin":{"CookiesConfig":{"CookieBehavior":"none"},"EnableAcceptEncodingGzip":true,"HeadersConfig":{"HeaderBehavior":"none"},"QueryStringsConfig":{"QueryStringBehavior":"none"}}}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-cache-policy",
		"--id", id, "--if-match", etag, "--cache-policy-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("cp_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListCachePolicies(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-cache-policies")
	return err
}

func (g *cfCliGroup) DeleteCachePolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cp_id")
	etag := t.GetString("cp_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-cache-policy",
		"--id", id, "--if-match", etag)
}

// ── Key Group ────────────────────────────────────────────────────────────────

func (g *cfCliGroup) teardownKeyGroup(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-key-group", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-key-group", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateKeyGroup(_ context.Context, t *harness.TestContext) error {
	cfg := fmt.Sprintf(`{"Name":"oc-kg-%s","Items":["K1234567890ABCDE"]}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-key-group",
		"--key-group-config", cfg)
	if err != nil {
		return err
	}
	kg, _ := out["KeyGroup"].(map[string]interface{})
	id, _ := kg["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateKeyGroup: missing Id")
	}
	t.Set("kg_id", id)
	t.Set("kg_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetKeyGroup(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return fmt.Errorf("GetKeyGroup: no kg_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-key-group", "--id", id)
	if err != nil {
		return err
	}
	if out["KeyGroup"] == nil {
		return fmt.Errorf("GetKeyGroup: missing KeyGroup")
	}
	return nil
}

func (g *cfCliGroup) GetKeyGroupConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	if id == "" {
		return fmt.Errorf("GetKeyGroupConfig: no kg_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-key-group-config", "--id", id)
	if err != nil {
		return err
	}
	if out["KeyGroupConfig"] == nil {
		return fmt.Errorf("GetKeyGroupConfig: missing KeyGroupConfig")
	}
	return nil
}

func (g *cfCliGroup) UpdateKeyGroup(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	etag := t.GetString("kg_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateKeyGroup: missing prerequisite")
	}
	cfg := fmt.Sprintf(`{"Name":"oc-kg-%s","Comment":"updated","Items":["K1234567890ABCDE"]}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-key-group",
		"--id", id, "--if-match", etag, "--key-group-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("kg_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListKeyGroups(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-key-groups")
	return err
}

func (g *cfCliGroup) DeleteKeyGroup(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("kg_id")
	etag := t.GetString("kg_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-key-group",
		"--id", id, "--if-match", etag)
}

// ── Realtime Log Config ──────────────────────────────────────────────────────

func (g *cfCliGroup) teardownRealtimeLog(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return nil
	}
	awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-realtime-log-config", "--name", name) //nolint:errcheck
	return nil
}

func (g *cfCliGroup) CreateRealtimeLogConfig(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("oc-rlc-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-realtime-log-config",
		"--name", name,
		"--sampling-rate", "100",
		"--end-points", `[{"StreamType":"Kinesis","KinesisStreamConfig":{"RoleARN":"arn:aws:iam::000000000000:role/test","StreamARN":"arn:aws:kinesis:us-east-1:000000000000:stream/test"}}]`,
		"--fields", "timestamp", "c-ip")
	if err != nil {
		return err
	}
	rlc, _ := out["RealtimeLogConfig"].(map[string]interface{})
	arn, _ := rlc["ARN"].(string)
	if arn == "" {
		return fmt.Errorf("CreateRealtimeLogConfig: missing ARN")
	}
	t.Set("rlc_name", name)
	t.Set("rlc_arn", arn)
	return nil
}

func (g *cfCliGroup) GetRealtimeLogConfig(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return fmt.Errorf("GetRealtimeLogConfig: no rlc_name")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-realtime-log-config", "--name", name)
	if err != nil {
		return err
	}
	if out["RealtimeLogConfig"] == nil {
		return fmt.Errorf("GetRealtimeLogConfig: missing RealtimeLogConfig")
	}
	return nil
}

func (g *cfCliGroup) UpdateRealtimeLogConfig(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	arn := t.GetString("rlc_arn")
	if name == "" || arn == "" {
		return fmt.Errorf("UpdateRealtimeLogConfig: missing prerequisite")
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-realtime-log-config",
		"--name", name,
		"--arn", arn,
		"--sampling-rate", "50",
		"--end-points", `[{"StreamType":"Kinesis","KinesisStreamConfig":{"RoleARN":"arn:aws:iam::000000000000:role/test","StreamARN":"arn:aws:kinesis:us-east-1:000000000000:stream/test"}}]`,
		"--fields", "timestamp", "c-ip", "sc-status")
	return err
}

func (g *cfCliGroup) ListRealtimeLogConfigs(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-realtime-log-configs")
	return err
}

func (g *cfCliGroup) DeleteRealtimeLogConfig(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("rlc_name")
	if name == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-realtime-log-config", "--name", name)
}

// ── Monitoring ───────────────────────────────────────────────────────────────

func (g *cfCliGroup) setupMonitoring(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-distribution",
		"--distribution-config", cfDistroConfig(t.RunID+"-monitoring"))
	if err != nil {
		return err
	}
	distro, _ := out["Distribution"].(map[string]interface{})
	id, _ := distro["Id"].(string)
	if id == "" {
		return fmt.Errorf("setupMonitoring: missing distribution Id")
	}
	t.Set("mon_distro_id", id)
	return nil
}

func (g *cfCliGroup) teardownMonitoring(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return nil
	}
	awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-monitoring-subscription", "--distribution-id", id) //nolint:errcheck
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-distribution", "--id", id)
	if err != nil {
		return nil
	}
	etag, _ := out["ETag"].(string)
	distro, _ := out["Distribution"].(map[string]interface{})
	cfg, _ := distro["DistributionConfig"].(map[string]interface{})
	if enabled, _ := cfg["Enabled"].(bool); enabled {
		cfg["Enabled"] = false
		cfgBytes, _ := json.Marshal(cfg)
		if upd, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-distribution",
			"--id", id, "--if-match", etag, "--distribution-config", string(cfgBytes)); err == nil {
			etag, _ = upd["ETag"].(string)
		}
	}
	awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-distribution", "--id", id, "--if-match", etag) //nolint:errcheck
	return nil
}

func (g *cfCliGroup) CreateMonitoringSubscription(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return fmt.Errorf("CreateMonitoringSubscription: no mon_distro_id")
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-monitoring-subscription",
		"--distribution-id", id,
		"--monitoring-subscription", `{"RealtimeMetricsSubscriptionConfig":{"RealtimeMetricsSubscriptionStatus":"Enabled"}}`)
	return err
}

func (g *cfCliGroup) GetMonitoringSubscription(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return fmt.Errorf("GetMonitoringSubscription: no mon_distro_id")
	}
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-monitoring-subscription",
		"--distribution-id", id)
	return err
}

func (g *cfCliGroup) DeleteMonitoringSubscription(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("mon_distro_id")
	if id == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-monitoring-subscription",
		"--distribution-id", id)
}

// ── Field-Level Encryption Config ────────────────────────────────────────────

func (g *cfCliGroup) teardownFLEConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-field-level-encryption-config", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateFieldLevelEncryptionConfig(_ context.Context, t *harness.TestContext) error {
	cfg := fmt.Sprintf(`{"CallerReference":"oc-fle-%s","Comment":"compat test"}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-field-level-encryption-config",
		"--field-level-encryption-config", cfg)
	if err != nil {
		return err
	}
	fle, _ := out["FieldLevelEncryption"].(map[string]interface{})
	id, _ := fle["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateFieldLevelEncryptionConfig: missing Id")
	}
	t.Set("fle_id", id)
	t.Set("fle_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetFieldLevelEncryption(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryption: no fle_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption", "--id", id)
	if err != nil {
		return err
	}
	if out["FieldLevelEncryption"] == nil {
		return fmt.Errorf("GetFieldLevelEncryption: missing FieldLevelEncryption")
	}
	return nil
}

func (g *cfCliGroup) GetFieldLevelEncryptionConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionConfig: no fle_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption-config", "--id", id)
	if err != nil {
		return err
	}
	if out["FieldLevelEncryptionConfig"] == nil {
		return fmt.Errorf("GetFieldLevelEncryptionConfig: missing FieldLevelEncryptionConfig")
	}
	return nil
}

func (g *cfCliGroup) UpdateFieldLevelEncryptionConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	etag := t.GetString("fle_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateFieldLevelEncryptionConfig: missing prerequisite")
	}
	cfg := fmt.Sprintf(`{"CallerReference":"oc-fle-%s","Comment":"compat test updated"}`, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-field-level-encryption-config",
		"--id", id, "--if-match", etag, "--field-level-encryption-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("fle_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListFieldLevelEncryptionConfigs(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-field-level-encryption-configs")
	return err
}

func (g *cfCliGroup) DeleteFieldLevelEncryption(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("fle_id")
	etag := t.GetString("fle_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-field-level-encryption-config",
		"--id", id, "--if-match", etag)
}

// ── Field-Level Encryption Profile ───────────────────────────────────────────

func (g *cfCliGroup) teardownFLEProfile(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption-profile", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-field-level-encryption-profile", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateFieldLevelEncryptionProfile(_ context.Context, t *harness.TestContext) error {
	cfg := fmt.Sprintf(`{"CallerReference":"oc-flep-%s","Name":"oc-flep-%s","Comment":"compat test","EncryptionEntities":{"Quantity":0,"Items":[]}}`, t.RunID, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-field-level-encryption-profile",
		"--field-level-encryption-profile-config", cfg)
	if err != nil {
		return err
	}
	prof, _ := out["FieldLevelEncryptionProfile"].(map[string]interface{})
	id, _ := prof["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateFieldLevelEncryptionProfile: missing Id")
	}
	t.Set("flep_id", id)
	t.Set("flep_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetFieldLevelEncryptionProfile(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionProfile: no flep_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption-profile", "--id", id)
	if err != nil {
		return err
	}
	if out["FieldLevelEncryptionProfile"] == nil {
		return fmt.Errorf("GetFieldLevelEncryptionProfile: missing FieldLevelEncryptionProfile")
	}
	return nil
}

func (g *cfCliGroup) GetFieldLevelEncryptionProfileConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	if id == "" {
		return fmt.Errorf("GetFieldLevelEncryptionProfileConfig: no flep_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-field-level-encryption-profile-config", "--id", id)
	if err != nil {
		return err
	}
	if out["FieldLevelEncryptionProfileConfig"] == nil {
		return fmt.Errorf("GetFieldLevelEncryptionProfileConfig: missing config")
	}
	return nil
}

func (g *cfCliGroup) UpdateFieldLevelEncryptionProfile(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	etag := t.GetString("flep_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateFieldLevelEncryptionProfile: missing prerequisite")
	}
	cfg := fmt.Sprintf(`{"CallerReference":"oc-flep-%s","Name":"oc-flep-%s","Comment":"compat test updated","EncryptionEntities":{"Quantity":0,"Items":[]}}`, t.RunID, t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-field-level-encryption-profile",
		"--id", id, "--if-match", etag, "--field-level-encryption-profile-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("flep_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListFieldLevelEncryptionProfiles(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-field-level-encryption-profiles")
	return err
}

func (g *cfCliGroup) DeleteFieldLevelEncryptionProfile(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("flep_id")
	etag := t.GetString("flep_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-field-level-encryption-profile",
		"--id", id, "--if-match", etag)
}

// ── Continuous Deployment Policy ─────────────────────────────────────────────

func (g *cfCliGroup) teardownContinuousDeployment(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return nil
	}
	out, _ := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-continuous-deployment-policy", "--id", id)
	etag, _ := out["ETag"].(string)
	if etag != "" {
		awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-continuous-deployment-policy", "--id", id, "--if-match", etag) //nolint:errcheck
	}
	return nil
}

func (g *cfCliGroup) CreateContinuousDeploymentPolicy(_ context.Context, t *harness.TestContext) error {
	cfg := `{"StagingDistributionDnsNames":{"Quantity":1,"Items":["d1234.cloudfront.net"]},"Enabled":true}`
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "create-continuous-deployment-policy",
		"--continuous-deployment-policy-config", cfg)
	if err != nil {
		return err
	}
	cdp, _ := out["ContinuousDeploymentPolicy"].(map[string]interface{})
	id, _ := cdp["Id"].(string)
	if id == "" {
		return fmt.Errorf("CreateContinuousDeploymentPolicy: missing Id")
	}
	t.Set("cdp_id", id)
	t.Set("cdp_etag", out["ETag"])
	return nil
}

func (g *cfCliGroup) GetContinuousDeploymentPolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return fmt.Errorf("GetContinuousDeploymentPolicy: no cdp_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-continuous-deployment-policy", "--id", id)
	if err != nil {
		return err
	}
	if out["ContinuousDeploymentPolicy"] == nil {
		return fmt.Errorf("GetContinuousDeploymentPolicy: missing ContinuousDeploymentPolicy")
	}
	return nil
}

func (g *cfCliGroup) GetContinuousDeploymentPolicyConfig(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	if id == "" {
		return fmt.Errorf("GetContinuousDeploymentPolicyConfig: no cdp_id")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "get-continuous-deployment-policy-config", "--id", id)
	if err != nil {
		return err
	}
	if out["ContinuousDeploymentPolicyConfig"] == nil {
		return fmt.Errorf("GetContinuousDeploymentPolicyConfig: missing config")
	}
	return nil
}

func (g *cfCliGroup) UpdateContinuousDeploymentPolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	etag := t.GetString("cdp_etag")
	if id == "" || etag == "" {
		return fmt.Errorf("UpdateContinuousDeploymentPolicy: missing prerequisite")
	}
	cfg := `{"StagingDistributionDnsNames":{"Quantity":1,"Items":["d5678.cloudfront.net"]},"Enabled":false}`
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "update-continuous-deployment-policy",
		"--id", id, "--if-match", etag, "--continuous-deployment-policy-config", cfg)
	if err != nil {
		return err
	}
	if e, _ := out["ETag"].(string); e != "" {
		t.Set("cdp_etag", e)
	}
	return nil
}

func (g *cfCliGroup) ListContinuousDeploymentPolicies(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "cloudfront", "list-continuous-deployment-policies")
	return err
}

func (g *cfCliGroup) DeleteContinuousDeploymentPolicy(_ context.Context, t *harness.TestContext) error {
	id := t.GetString("cdp_id")
	etag := t.GetString("cdp_etag")
	if id == "" || etag == "" {
		return nil
	}
	return awscli.Run(t.Endpoint, t.Region, "cloudfront", "delete-continuous-deployment-policy",
		"--id", id, "--if-match", etag)
}
