package cloudformation_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// efsStackTemplate provisions a file system with policy/lifecycle/backup
// sub-configuration, a mount target, and an access point — the shape a CDK
// "shared storage" stack produces.
const efsStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "SharedFS": {
      "Type": "AWS::EFS::FileSystem",
      "Properties": {
        "Encrypted": true,
        "ThroughputMode": "elastic",
        "BackupPolicy": {"Status": "ENABLED"},
        "FileSystemTags": [{"Key": "Name", "Value": "cfn-shared-fs"}],
        "LifecyclePolicies": [{"TransitionToIA": "AFTER_30_DAYS"}],
        "FileSystemPolicy": {"Version": "2012-10-17", "Statement": []}
      }
    },
    "FSMountTarget": {
      "Type": "AWS::EFS::MountTarget",
      "Properties": {
        "FileSystemId": {"Ref": "SharedFS"},
        "SubnetId": "subnet-cfn0001",
        "SecurityGroups": ["sg-cfn0001"]
      }
    },
    "FSAccessPoint": {
      "Type": "AWS::EFS::AccessPoint",
      "Properties": {
        "FileSystemId": {"Ref": "SharedFS"},
        "PosixUser": {"Uid": "1000", "Gid": "1000"},
        "RootDirectory": {
          "Path": "/app",
          "CreationInfo": {"OwnerUid": "1000", "OwnerGid": "1000", "Permissions": "0755"}
        },
        "AccessPointTags": [{"Key": "Name", "Value": "cfn-shared-ap"}]
      }
    }
  },
  "Outputs": {
    "FileSystemId": {"Value": {"Ref": "SharedFS"}},
    "FileSystemArn": {"Value": {"Fn::GetAtt": ["SharedFS", "Arn"]}},
    "MountTargetId": {"Value": {"Ref": "FSMountTarget"}},
    "MountTargetIp": {"Value": {"Fn::GetAtt": ["FSMountTarget", "IpAddress"]}},
    "AccessPointArn": {"Value": {"Fn::GetAtt": ["FSAccessPoint", "Arn"]}}
  }
}`

func efsGet(t *testing.T, srv *helpers.TestServer, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/2015-02-01" + path)
	if err != nil {
		t.Fatalf("EFS GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("EFS GET %s: decode: %v", path, err)
	}
	return resp.StatusCode, body
}

func TestCreateStack_EFSFileSystemMountTargetAccessPoint(t *testing.T) {
	// Given: a CloudFormation stack with EFS file system, mount target, and
	// access point resources.
	srv := helpers.NewTestServer(t)

	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"efs-stack"},
		"TemplateBody": []string{efsStackTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the stack completes.
	waitForStackStatus(t, srv, "efs-stack", "CREATE_COMPLETE")

	// And: the file system exists, is available, tagged, and configured.
	status, fsResp := efsGet(t, srv, "/file-systems")
	if status != http.StatusOK {
		t.Fatalf("DescribeFileSystems: HTTP %d: %#v", status, fsResp)
	}
	fsList, _ := fsResp["FileSystems"].([]any)
	if len(fsList) != 1 {
		t.Fatalf("expected 1 file system, got %#v", fsResp)
	}
	fs := fsList[0].(map[string]any)
	fsID := fs["FileSystemId"].(string)
	fsArn := fs["FileSystemArn"].(string)
	if fs["LifeCycleState"] != "available" {
		t.Fatalf("expected available, got %v", fs["LifeCycleState"])
	}
	if fs["Name"] != "cfn-shared-fs" {
		t.Fatalf("expected FileSystemTags applied, got Name=%v", fs["Name"])
	}
	if fs["Encrypted"] != true || fs["ThroughputMode"] != "elastic" {
		t.Fatalf("expected Encrypted elastic file system, got %#v", fs)
	}

	if status, backup := efsGet(t, srv, "/file-systems/"+fsID+"/backup-policy"); status != http.StatusOK {
		t.Fatalf("DescribeBackupPolicy: HTTP %d", status)
	} else if bp, _ := backup["BackupPolicy"].(map[string]any); bp["Status"] != "ENABLED" {
		t.Fatalf("expected backup ENABLED, got %#v", backup)
	}
	if status, lc := efsGet(t, srv, "/file-systems/"+fsID+"/lifecycle-configuration"); status != http.StatusOK {
		t.Fatalf("DescribeLifecycleConfiguration: HTTP %d", status)
	} else if policies, _ := lc["LifecyclePolicies"].([]any); len(policies) != 1 {
		t.Fatalf("expected 1 lifecycle policy, got %#v", lc)
	}
	if status, _ := efsGet(t, srv, "/file-systems/"+fsID+"/policy"); status != http.StatusOK {
		t.Fatalf("expected file system policy applied, HTTP %d", status)
	}

	// And: the mount target exists with the template's subnet + groups.
	status, mtResp := efsGet(t, srv, "/mount-targets?FileSystemId="+fsID)
	if status != http.StatusOK {
		t.Fatalf("DescribeMountTargets: HTTP %d", status)
	}
	mtList, _ := mtResp["MountTargets"].([]any)
	if len(mtList) != 1 {
		t.Fatalf("expected 1 mount target, got %#v", mtResp)
	}
	mt := mtList[0].(map[string]any)
	if mt["SubnetId"] != "subnet-cfn0001" || mt["LifeCycleState"] != "available" {
		t.Fatalf("unexpected mount target: %#v", mt)
	}
	mtID := mt["MountTargetId"].(string)
	mtIP := mt["IpAddress"].(string)

	// And: the access point exists with normalized numeric posix identities.
	status, apResp := efsGet(t, srv, "/access-points?FileSystemId="+fsID)
	if status != http.StatusOK {
		t.Fatalf("DescribeAccessPoints: HTTP %d", status)
	}
	apList, _ := apResp["AccessPoints"].([]any)
	if len(apList) != 1 {
		t.Fatalf("expected 1 access point, got %#v", apResp)
	}
	ap := apList[0].(map[string]any)
	apArn := ap["AccessPointArn"].(string)
	if posix, _ := ap["PosixUser"].(map[string]any); posix["Uid"] != float64(1000) {
		t.Fatalf("expected numeric Uid 1000, got %#v", ap["PosixUser"])
	}
	if root, _ := ap["RootDirectory"].(map[string]any); root["Path"] != "/app" {
		t.Fatalf("expected root directory /app, got %#v", ap["RootDirectory"])
	}

	// And: stack outputs carry Ref and GetAtt values through.
	dsResp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": []string{"efs-stack"}})
	defer dsResp.Body.Close()
	stackBody := string(readBody(t, dsResp))
	for name, want := range map[string]string{
		"FileSystemId":   fsID,
		"FileSystemArn":  fsArn,
		"MountTargetId":  mtID,
		"MountTargetIp":  mtIP,
		"AccessPointArn": apArn,
	} {
		if want == "" || !strings.Contains(stackBody, want) {
			t.Fatalf("output %s: expected %q in DescribeStacks output, body=%s", name, want, stackBody)
		}
	}

	// When: the stack is deleted.
	del := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": []string{"efs-stack"}})
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusOK)

	// Then: mount target, access point, and file system are all removed
	// (deletion runs in reverse dependency order, so FileSystemInUse never fires).
	helpers.Eventually(t, 5*time.Second, 20*time.Millisecond, func() bool {
		_, body := efsGet(t, srv, "/file-systems")
		list, _ := body["FileSystems"].([]any)
		return len(list) == 0
	}, "timed out waiting for EFS resources to be deleted with the stack")
}
