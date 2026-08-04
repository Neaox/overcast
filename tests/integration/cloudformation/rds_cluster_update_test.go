package cloudformation_test

// rds_cluster_update_test.go — updating an AWS::RDS::DBCluster.
//
// The handler built a ModifyDBCluster call carrying MasterUserPassword,
// BackupRetentionPeriod, Port, PreferredBackupWindow,
// PreferredMaintenanceWindow, DBClusterParameterGroupName,
// VpcSecurityGroupIds, EnableCloudwatchLogsExports and DeletionProtection —
// and the RDS side decoded none of them, because modifyDBClusterReq had two
// fields. Every one was dropped between the wire and the handler, so a stack
// update that changed any of them reported UPDATE_COMPLETE having changed
// nothing at all.
//
// The same file covers the other half of the decision: the properties AWS
// documents as "Update requires: Replacement" must not be quietly applied in
// place either.

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// clusterProps is the mutable half of the cluster template, so a test can
// state only what it is changing.
type clusterProps struct {
	ID                         string
	MasterUsername             string
	MasterUserPassword         string
	DatabaseName               string
	BackupRetentionPeriod      string
	Port                       string
	PreferredBackupWindow      string
	PreferredMaintenanceWindow string
	DeletionProtection         string
	LogExports                 []string
	SecurityGroupIDs           []string
}

func rdsClusterTemplate(p clusterProps) string {
	var b strings.Builder
	add := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "        %q: %q,\n", name, value)
		}
	}
	add("DBClusterIdentifier", p.ID)
	add("DatabaseName", p.DatabaseName)
	add("BackupRetentionPeriod", p.BackupRetentionPeriod)
	add("Port", p.Port)
	add("PreferredBackupWindow", p.PreferredBackupWindow)
	add("PreferredMaintenanceWindow", p.PreferredMaintenanceWindow)
	add("DeletionProtection", p.DeletionProtection)
	if len(p.LogExports) > 0 {
		fmt.Fprintf(&b, "        \"EnableCloudwatchLogsExports\": [%s],\n", quoteList(p.LogExports))
	}
	if len(p.SecurityGroupIDs) > 0 {
		fmt.Fprintf(&b, "        \"VpcSecurityGroupIds\": [%s],\n", quoteList(p.SecurityGroupIDs))
	}

	return fmt.Sprintf(`{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Cluster": {
      "Type": "AWS::RDS::DBCluster",
      "Properties": {
%s        "Engine": "aurora-mysql",
        "MasterUsername": %q,
        "MasterUserPassword": %q
      }
    }
  }
}`, b.String(), p.MasterUsername, p.MasterUserPassword)
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// describedCluster is the subset of DescribeDBClusters this file asserts on.
type describedCluster struct {
	ID                         string   `xml:"DBClusterIdentifier"`
	MasterUsername             string   `xml:"MasterUsername"`
	EngineVersion              string   `xml:"EngineVersion"`
	Port                       int      `xml:"Port"`
	BackupRetentionPeriod      int      `xml:"BackupRetentionPeriod"`
	PreferredBackupWindow      string   `xml:"PreferredBackupWindow"`
	PreferredMaintenanceWindow string   `xml:"PreferredMaintenanceWindow"`
	DBClusterParameterGroup    string   `xml:"DBClusterParameterGroup"`
	DeletionProtection         bool     `xml:"DeletionProtection"`
	LogExports                 []string `xml:"EnabledCloudwatchLogsExports>member"`
	SecurityGroupIDs           []string `xml:"VpcSecurityGroups>VpcSecurityGroupMembership>VpcSecurityGroupId"`
}

func describeClusters(t *testing.T, srv *helpers.TestServer) map[string]describedCluster {
	t.Helper()
	params := url.Values{
		"Action":  []string{"DescribeDBClusters"},
		"Version": []string{"2014-10-31"},
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build DescribeDBClusters request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeDBClusters: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	body := readBody(t, resp)
	var result struct {
		Clusters []describedCluster `xml:"DescribeDBClustersResult>DBClusters>DBCluster"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal DescribeDBClustersResponse: %v\nbody: %s", err, body)
	}
	out := make(map[string]describedCluster, len(result.Clusters))
	for _, c := range result.Clusters {
		out[c.ID] = c
	}
	return out
}

// The whole bug in one test: change every in-place property at once and check
// that the stack's claim of UPDATE_COMPLETE is backed by the cluster.
func TestUpdateStack_rdsClusterInPlacePropertiesAreApplied(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-cluster-props-stack"
	const clusterID = "props-cluster"

	before := clusterProps{
		ID:                         clusterID,
		MasterUsername:             "admin",
		MasterUserPassword:         "correct-horse-battery",
		BackupRetentionPeriod:      "1",
		Port:                       "3306",
		PreferredBackupWindow:      "01:00-02:00",
		PreferredMaintenanceWindow: "mon:03:00-mon:04:00",
	}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(before)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	after := before
	after.BackupRetentionPeriod = "14"
	after.Port = "3307"
	after.PreferredBackupWindow = "02:00-03:00"
	after.PreferredMaintenanceWindow = "sun:05:00-sun:06:00"
	after.LogExports = []string{"audit", "error"}
	after.SecurityGroupIDs = []string{"sg-11111111", "sg-22222222"}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(after)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE")

	got, ok := describeClusters(t, srv)[clusterID]
	if !ok {
		t.Fatalf("cluster %s is gone after an in-place update", clusterID)
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
	if strings.Join(got.LogExports, ",") != "audit,error" {
		t.Errorf("EnabledCloudwatchLogsExports = %v, want [audit error]", got.LogExports)
	}
	if strings.Join(got.SecurityGroupIDs, ",") != "sg-11111111,sg-22222222" {
		t.Errorf("VpcSecurityGroups = %v, want [sg-11111111 sg-22222222]", got.SecurityGroupIDs)
	}
}

// A master password change is applied rather than dropped. There is no engine
// container in this test server, so the record is the whole truth — but the
// record has to actually change.
func TestUpdateStack_rdsClusterMasterPasswordIsApplied(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-cluster-password-stack"
	const clusterID = "password-cluster"

	before := clusterProps{ID: clusterID, MasterUsername: "admin", MasterUserPassword: "correct-horse-battery"}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(before)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	after := before
	after.MasterUserPassword = "rotated-horse-battery"
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(after)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE")

	// AWS never returns a master password, so the check is indirect: the
	// cluster survived in place and the stack did not report a replacement.
	if _, ok := describeClusters(t, srv)[clusterID]; !ok {
		t.Fatalf("cluster %s is gone after a password rotation, which is an in-place change", clusterID)
	}
}

// MasterUsername carries "Update requires: Replacement" on AWS. With the
// identifier pinned in the template, replacement cannot succeed — the new
// cluster collides with the one still standing — so the stack must roll back
// rather than report success over an unapplied change.
func TestUpdateStack_rdsClusterMasterUsernameForcesReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-cluster-username-stack"
	const clusterID = "username-cluster"

	before := clusterProps{ID: clusterID, MasterUsername: "olduser", MasterUserPassword: "correct-horse-battery"}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(before)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	after := before
	after.MasterUsername = "newuser"
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(after)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	got := waitForStackStatusIn(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_COMPLETE")
	if got == "UPDATE_COMPLETE" {
		t.Fatalf("stack reported %s for a replacement that cannot succeed on a pinned identifier", got)
	}

	after2 := describeClusters(t, srv)
	if c, ok := after2[clusterID]; !ok {
		t.Errorf("original cluster %s is gone after a rolled-back update; clusters: %v", clusterID, after2)
	} else if c.MasterUsername != "olduser" {
		t.Errorf("original cluster master username = %q, want it left at %q", c.MasterUsername, "olduser")
	}
}

// DatabaseName is the other "Update requires: Replacement" property the
// handler used to ignore entirely.
func TestUpdateStack_rdsClusterDatabaseNameForcesReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-cluster-dbname-stack"
	const clusterID = "dbname-cluster"

	before := clusterProps{
		ID: clusterID, MasterUsername: "admin",
		MasterUserPassword: "correct-horse-battery", DatabaseName: "olddb",
	}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(before)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	after := before
	after.DatabaseName = "newdb"
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(after)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)

	if got := waitForStackStatusIn(t, srv, stackName,
		"UPDATE_ROLLBACK_COMPLETE", "UPDATE_FAILED", "UPDATE_COMPLETE"); got == "UPDATE_COMPLETE" {
		t.Fatalf("stack reported %s for a DatabaseName change, which AWS documents as Replacement", got)
	}
}

// Turning DeletionProtection on must be applied and must then be enforced: a
// stack that tries to delete a protected cluster fails, as it does on AWS.
func TestDeleteStack_rdsClusterDeletionProtectionBlocksTheDelete(t *testing.T) {
	srv := helpers.NewTestServer(t)
	const stackName = "rds-cluster-protected-stack"
	const clusterID = "protected-cluster"

	before := clusterProps{ID: clusterID, MasterUsername: "admin", MasterUserPassword: "correct-horse-battery"}
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(before)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	after := before
	after.DeletionProtection = "true"
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{rdsClusterTemplate(after)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, stackName, "UPDATE_COMPLETE")

	if got := describeClusters(t, srv)[clusterID]; !got.DeletionProtection {
		t.Fatal("DeletionProtection was not applied by the update")
	}

	dr := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{stackName}})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)

	got := waitForStackStatusIn(t, srv, stackName, "DELETE_FAILED", "DELETE_COMPLETE")
	if got == "DELETE_COMPLETE" {
		t.Fatal("stack deleted a cluster with deletion protection enabled")
	}

	// The cluster itself must still be there — that is the point of the flag.
	helpers.Eventually(t, 5*time.Second, 50*time.Millisecond, func() bool {
		_, ok := describeClusters(t, srv)[clusterID]
		return ok
	}, "protected cluster was removed despite the stack delete failing")
}
