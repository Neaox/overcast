package rds

// handler_cluster_modify_test.go — ModifyDBCluster applying what it is sent.
//
// modifyDBClusterReq carried DBClusterIdentifier and EngineVersion and nothing
// else, so every other parameter the CloudFormation handler sent —
// BackupRetentionPeriod, Port, PreferredBackupWindow,
// PreferredMaintenanceWindow, DBClusterParameterGroupName,
// VpcSecurityGroupIds, EnableCloudwatchLogsExports, DeletionProtection — was
// decoded into nothing. A stack update that changed any of them reported
// UPDATE_COMPLETE having changed nothing, and DescribeDBClusters went on
// reporting the old values with no indication that an update had been asked
// for at all.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func newClusterTestHandler(t *testing.T) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	return New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New()).handler
}

func seedCluster(t *testing.T, h *Handler, id string) {
	t.Helper()
	if _, aerr := h.createDBClusterTyped(context.Background(), &createDBClusterReq{
		DBClusterIdentifier: id,
		Engine:              "aurora-mysql",
		MasterUsername:      "admin",
		MasterUserPassword:  "oldpassword1",
	}); aerr != nil {
		t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
}

// Every property the CloudFormation handler sends must survive the round trip
// into the stored cluster and back out through DescribeDBClusters.
func TestModifyDBCluster_appliesEveryProperty(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:         "cl",
		BackupRetentionPeriod:       intPtr(14),
		Port:                        3307,
		PreferredBackupWindow:       "02:00-03:00",
		PreferredMaintenanceWindow:  "sun:05:00-sun:06:00",
		DBClusterParameterGroupName: "custom-cluster-pg",
		VpcSecurityGroupIds:         []string{"sg-1111", "sg-2222"},
		CloudwatchLogsExportConfiguration: &cloudwatchLogsExportConfiguration{
			EnableLogTypes: []string{"audit", "error"},
		},
		DeletionProtection: boolPtr(true),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s: %s", aerr.Code, aerr.Message)
	}

	got, aerr := h.store.getDBCluster(ctx, "cl")
	if aerr != nil {
		t.Fatalf("getDBCluster: %s", aerr.Message)
	}
	if got.BackupRetentionPeriod != 14 {
		t.Errorf("BackupRetentionPeriod = %d, want 14", got.BackupRetentionPeriod)
	}
	if got.Port != 3307 {
		t.Errorf("Port = %d, want 3307", got.Port)
	}
	if got.PreferredBackupWindow != "02:00-03:00" {
		t.Errorf("PreferredBackupWindow = %q, want %q", got.PreferredBackupWindow, "02:00-03:00")
	}
	if got.PreferredMaintenanceWindow != "sun:05:00-sun:06:00" {
		t.Errorf("PreferredMaintenanceWindow = %q, want %q", got.PreferredMaintenanceWindow, "sun:05:00-sun:06:00")
	}
	if got.DBClusterParameterGroup != "custom-cluster-pg" {
		t.Errorf("DBClusterParameterGroup = %q, want %q", got.DBClusterParameterGroup, "custom-cluster-pg")
	}
	if strings.Join(got.VpcSecurityGroupIds, ",") != "sg-1111,sg-2222" {
		t.Errorf("VpcSecurityGroupIds = %v, want [sg-1111 sg-2222]", got.VpcSecurityGroupIds)
	}
	if strings.Join(got.EnabledCloudwatchLogsExports, ",") != "audit,error" {
		t.Errorf("EnabledCloudwatchLogsExports = %v, want [audit error]", got.EnabledCloudwatchLogsExports)
	}
	if !got.DeletionProtection {
		t.Error("DeletionProtection = false, want true")
	}
}

// An absent property leaves the stored value alone — a modify request names
// what to change, and AWS does not reset everything else to its default.
func TestModifyDBCluster_absentPropertiesAreLeftAlone(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:   "cl",
		BackupRetentionPeriod: intPtr(14),
		PreferredBackupWindow: "02:00-03:00",
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster (first): %s", aerr.Message)
	}
	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "cl",
		EngineVersion:       "3.05",
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster (second): %s", aerr.Message)
	}

	got, _ := h.store.getDBCluster(ctx, "cl")
	if got.EngineVersion != "3.05" {
		t.Errorf("EngineVersion = %q, want %q", got.EngineVersion, "3.05")
	}
	if got.BackupRetentionPeriod != 14 {
		t.Errorf("BackupRetentionPeriod = %d after an unrelated modify, want it left at 14", got.BackupRetentionPeriod)
	}
	if got.PreferredBackupWindow != "02:00-03:00" {
		t.Errorf("PreferredBackupWindow = %q after an unrelated modify, want it left alone", got.PreferredBackupWindow)
	}
}

// BackupRetentionPeriod=0 and DeletionProtection=false are requests, not
// absences: a plain int and a plain bool cannot tell "turn it off" from "did
// not mention it", which is the same trap MultiAZ fell into on ModifyDBInstance.
func TestModifyDBCluster_zeroValuesAreRequestsNotAbsences(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:   "cl",
		BackupRetentionPeriod: intPtr(14),
		DeletionProtection:    boolPtr(true),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster (enable): %s", aerr.Message)
	}
	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:   "cl",
		BackupRetentionPeriod: intPtr(0),
		DeletionProtection:    boolPtr(false),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster (disable): %s", aerr.Message)
	}

	got, _ := h.store.getDBCluster(ctx, "cl")
	if got.BackupRetentionPeriod != 0 {
		t.Errorf("BackupRetentionPeriod = %d, want 0 — an explicit zero was ignored", got.BackupRetentionPeriod)
	}
	if got.DeletionProtection {
		t.Error("DeletionProtection = true, want false — an explicit false was ignored")
	}
}

// DescribeDBClusters must report the applied values, or "applied" is a claim
// no caller can check.
func TestDescribeDBClusters_reportsModifiedProperties(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:         "cl",
		BackupRetentionPeriod:       intPtr(7),
		PreferredBackupWindow:       "02:00-03:00",
		DBClusterParameterGroupName: "custom-cluster-pg",
		VpcSecurityGroupIds:         []string{"sg-1111"},
		CloudwatchLogsExportConfiguration: &cloudwatchLogsExportConfiguration{
			EnableLogTypes: []string{"error"},
		},
		DeletionProtection: boolPtr(true),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s", aerr.Message)
	}

	resp, aerr := h.describeDBClustersTyped(ctx, &describeDBClustersReq{DBClusterIdentifier: "cl"})
	if aerr != nil {
		t.Fatalf("DescribeDBClusters: %s", aerr.Message)
	}
	items := resp.Result.DBClusters.Items
	if len(items) != 1 {
		t.Fatalf("expected one cluster, got %d", len(items))
	}
	c := items[0]
	if c.BackupRetentionPeriod != 7 {
		t.Errorf("wire BackupRetentionPeriod = %d, want 7", c.BackupRetentionPeriod)
	}
	if c.PreferredBackupWindow != "02:00-03:00" {
		t.Errorf("wire PreferredBackupWindow = %q, want %q", c.PreferredBackupWindow, "02:00-03:00")
	}
	if c.DBClusterParameterGroup != "custom-cluster-pg" {
		t.Errorf("wire DBClusterParameterGroup = %q, want %q", c.DBClusterParameterGroup, "custom-cluster-pg")
	}
	if len(c.VpcSecurityGroups.Items) != 1 || c.VpcSecurityGroups.Items[0].VpcSecurityGroupId != "sg-1111" {
		t.Errorf("wire VpcSecurityGroups = %+v, want one membership for sg-1111", c.VpcSecurityGroups.Items)
	}
	if len(c.EnabledCloudwatchLogsExports.Items) != 1 || c.EnabledCloudwatchLogsExports.Items[0] != "error" {
		t.Errorf("wire EnabledCloudwatchLogsExports = %v, want [error]", c.EnabledCloudwatchLogsExports.Items)
	}
	if !c.DeletionProtection {
		t.Error("wire DeletionProtection = false, want true")
	}
}

// A protected cluster must refuse to be deleted, as AWS does. Recording the
// flag and then deleting anyway would be worse than not recording it.
func TestDeleteDBCluster_deletionProtectionIsEnforced(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "cl",
		DeletionProtection:  boolPtr(true),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster: %s", aerr.Message)
	}

	_, aerr := h.deleteDBClusterTyped(ctx, &deleteDBClusterReq{DBClusterIdentifier: "cl"})
	if aerr == nil {
		t.Fatal("DeleteDBCluster deleted a cluster with deletion protection enabled")
	}
	if aerr.Code != "InvalidParameterCombination" {
		t.Errorf("error code = %q, want InvalidParameterCombination", aerr.Code)
	}
	if _, gerr := h.store.getDBCluster(ctx, "cl"); gerr != nil {
		t.Errorf("protected cluster is gone after a refused delete: %s", gerr.Message)
	}

	// Turning protection off must let the delete through.
	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier: "cl",
		DeletionProtection:  boolPtr(false),
	}); aerr != nil {
		t.Fatalf("ModifyDBCluster (disable protection): %s", aerr.Message)
	}
	if _, aerr := h.deleteDBClusterTyped(ctx, &deleteDBClusterReq{DBClusterIdentifier: "cl"}); aerr != nil {
		t.Fatalf("DeleteDBCluster after disabling protection: %s: %s", aerr.Code, aerr.Message)
	}
}

// ModifyDBCluster is reachable both through typed dispatch and through the raw
// Query path, and the two must not be able to drift — which they can only be
// guaranteed not to do if there is one implementation. StopDBInstance,
// StartDBInstance and ModifyDBInstance were collapsed onto invokeTypedAsQuery
// for exactly this reason; the cluster operations were left behind.
func TestModifyDBCluster_rawQueryPathAppliesTheSameProperties(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()
	seedCluster(t, h, "cl")

	form := url.Values{
		"Action":                []string{"ModifyDBCluster"},
		"Version":               []string{"2014-10-31"},
		"DBClusterIdentifier":   []string{"cl"},
		"BackupRetentionPeriod": []string{"21"},
		"PreferredBackupWindow": []string{"04:00-05:00"},
		"DeletionProtection":    []string{"true"},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.dispatch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("raw ModifyDBCluster: HTTP %d: %s", rec.Code, rec.Body.String())
	}

	got, aerr := h.store.getDBCluster(ctx, "cl")
	if aerr != nil {
		t.Fatalf("getDBCluster: %s", aerr.Message)
	}
	if got.BackupRetentionPeriod != 21 {
		t.Errorf("BackupRetentionPeriod = %d after a raw-path modify, want 21", got.BackupRetentionPeriod)
	}
	if got.PreferredBackupWindow != "04:00-05:00" {
		t.Errorf("PreferredBackupWindow = %q after a raw-path modify, want %q", got.PreferredBackupWindow, "04:00-05:00")
	}
	if !got.DeletionProtection {
		t.Error("DeletionProtection = false after a raw-path modify, want true")
	}
}

// AWS's CloudwatchLogsExportConfiguration is a delta, not a replacement:
// enabling one log type must not silently turn the others off, and disabling
// one must leave the rest.
func TestApplyLogExportConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		current []string
		cfg     cloudwatchLogsExportConfiguration
		want    string
	}{
		{"enable onto empty", nil,
			cloudwatchLogsExportConfiguration{EnableLogTypes: []string{"error"}}, "error"},
		{"enable adds without replacing", []string{"error"},
			cloudwatchLogsExportConfiguration{EnableLogTypes: []string{"audit"}}, "error,audit"},
		{"enabling what is already on is a no-op", []string{"error", "audit"},
			cloudwatchLogsExportConfiguration{EnableLogTypes: []string{"error"}}, "error,audit"},
		{"disable removes only its own", []string{"error", "audit", "slowquery"},
			cloudwatchLogsExportConfiguration{DisableLogTypes: []string{"audit"}}, "error,slowquery"},
		{"enable and disable in one request", []string{"error"},
			cloudwatchLogsExportConfiguration{
				EnableLogTypes:  []string{"slowquery"},
				DisableLogTypes: []string{"error"},
			}, "slowquery"},
		{"disabling everything leaves nothing", []string{"error"},
			cloudwatchLogsExportConfiguration{DisableLogTypes: []string{"error"}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(applyLogExportConfiguration(tc.current, &tc.cfg), ",")
			if got != tc.want {
				t.Errorf("applyLogExportConfiguration(%v, %+v) = %q, want %q",
					tc.current, tc.cfg, got, tc.want)
			}
		})
	}
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }
