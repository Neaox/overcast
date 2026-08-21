package cloudformation_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// Issue #531: kinesisStreamHandler.Create sent only StreamName and
// ShardCount, so a CDK stream with `retentionPeriod: Duration.days(7)`
// provisioned fine and reported Kinesis' 24-hour default back — the
// RetentionPeriodHours CDK sent never reached the emulator. Tags,
// StreamModeDetails and StreamEncryption had the same problem: the Kinesis
// service already supported all four, CloudFormation just never called the
// operations that carry them.
func kinesisJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "Kinesis_20131202.", action, "application/x-amz-json-1.1", body)
}

func describeKinesisStreamSummary(t *testing.T, srv *helpers.TestServer, streamName string) map[string]any {
	t.Helper()
	resp := kinesisJSONCall(t, srv, "DescribeStreamSummary", map[string]any{"StreamName": streamName})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		StreamDescriptionSummary map[string]any `json:"StreamDescriptionSummary"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.StreamDescriptionSummary
}

func listKinesisStreamTags(t *testing.T, srv *helpers.TestServer, streamName string) map[string]string {
	t.Helper()
	resp := kinesisJSONCall(t, srv, "ListTagsForStream", map[string]any{"StreamName": streamName})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		Tags []struct {
			Key   string `json:"Key"`
			Value string `json:"Value"`
		} `json:"Tags"`
	}
	helpers.DecodeJSON(t, resp, &result)
	tags := make(map[string]string, len(result.Tags))
	for _, tag := range result.Tags {
		tags[tag.Key] = tag.Value
	}
	return tags
}

const kinesisPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Stream": {
      "Type": "AWS::Kinesis::Stream",
      "Properties": {
        "Name": "cfn-props-stream",
        "RetentionPeriodHours": 168,
        "StreamModeDetails": {"StreamMode": "ON_DEMAND"},
        "StreamEncryption": {"EncryptionType": "KMS", "KeyId": "alias/cfn-props-key"},
        "Tags": [
          {"Key": "team", "Value": "platform"},
          {"Key": "tier", "Value": "streaming"}
        ]
      }
    }
  },
  "Outputs": {
    "StreamName": {"Value": {"Ref": "Stream"}}
  }
}`

// TestCreateStack_KinesisStreamPropertiesThreaded is the Definition of Done
// from #531: RetentionPeriodHours applied at create, and StreamModeDetails/
// StreamEncryption/Tags round-tripping through DescribeStreamSummary.
func TestCreateStack_KinesisStreamPropertiesThreaded(t *testing.T) {
	// Given: a CDK-shaped stack whose stream sets every property
	// kinesisStreamHandler.Create used to drop.
	srv := helpers.NewTestServer(t)

	// When: the stack is deployed.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"kinesis-props-stack"},
		"TemplateBody": {kinesisPropertiesTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-props-stack", "CREATE_COMPLETE")

	// Then: DescribeStreamSummary reports the retention period, mode and
	// encryption CDK sent, not Kinesis' defaults.
	summary := describeKinesisStreamSummary(t, srv, "cfn-props-stream")
	if got, want := summary["RetentionPeriodHours"], float64(168); got != want {
		t.Errorf("RetentionPeriodHours = %v, want %v (CDK's Duration.days(7) must not report Kinesis' 24-hour default)", got, want)
	}
	mode, _ := summary["StreamModeDetails"].(map[string]any)
	if got, want := mode["StreamMode"], "ON_DEMAND"; got != want {
		t.Errorf("StreamModeDetails.StreamMode = %v, want %v", got, want)
	}
	if got, want := summary["EncryptionType"], "KMS"; got != want {
		t.Errorf("EncryptionType = %v, want %v", got, want)
	}
	if got, want := summary["KeyId"], "alias/cfn-props-key"; got != want {
		t.Errorf("KeyId = %v, want %v", got, want)
	}

	// And: ListTagsForStream reports the Tags CDK sent, not silence.
	tags := listKinesisStreamTags(t, srv, "cfn-props-stream")
	want := map[string]string{"team": "platform", "tier": "streaming"}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("stream tags[%q] = %q, want %q (full: %#v)", k, tags[k], v, tags)
		}
	}
}

// TestUpdateStack_KinesisStreamPropertiesReconciled covers the other half of
// #531's Definition of Done: RetentionPeriodHours (and the same-shaped
// StreamEncryption/StreamModeDetails/Tags properties) reconciled on update,
// not just threaded at create.
func TestUpdateStack_KinesisStreamPropertiesReconciled(t *testing.T) {
	// Given: a stream created with Kinesis' defaults — no RetentionPeriodHours,
	// StreamModeDetails or StreamEncryption set, and two tags.
	srv := helpers.NewTestServer(t)
	template := func(retentionHours, mode, encryption, tags string) string {
		props := `"Name": "cfn-reconcile-stream", "Tags": ` + tags
		if retentionHours != "" {
			props += `, "RetentionPeriodHours": ` + retentionHours
		}
		if mode != "" {
			props += `, "StreamModeDetails": {"StreamMode": "` + mode + `"}`
		}
		if encryption != "" {
			props += `, "StreamEncryption": ` + encryption
		}
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Stream": {
      "Type": "AWS::Kinesis::Stream",
      "Properties": {` + props + `}
    }
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"kinesis-reconcile-stack"},
		"TemplateBody": {template("", "", "", `[{"Key": "keep", "Value": "same"}, {"Key": "drop", "Value": "gone-after-update"}]`)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-reconcile-stack", "CREATE_COMPLETE")

	before := describeKinesisStreamSummary(t, srv, "cfn-reconcile-stream")
	if got, want := before["RetentionPeriodHours"], float64(24); got != want {
		t.Fatalf("RetentionPeriodHours after create = %v, want %v (Kinesis' default)", got, want)
	}
	beforeMode, _ := before["StreamModeDetails"].(map[string]any)
	if got, want := beforeMode["StreamMode"], "PROVISIONED"; got != want {
		t.Fatalf("StreamModeDetails.StreamMode after create = %v, want %v (Kinesis' default)", got, want)
	}
	if got, want := before["EncryptionType"], "NONE"; got != want {
		t.Fatalf("EncryptionType after create = %v, want %v", got, want)
	}

	// When: the template raises retention, switches to ON_DEMAND, turns on
	// KMS encryption, and reshapes the tag set (drop "drop", keep "keep",
	// add "add").
	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {"kinesis-reconcile-stack"},
		"TemplateBody": {template(
			"72",
			"ON_DEMAND",
			`{"EncryptionType": "KMS", "KeyId": "alias/reconcile-key"}`,
			`[{"Key": "keep", "Value": "same"}, {"Key": "add", "Value": "new-after-update"}]`,
		)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-reconcile-stack", "UPDATE_COMPLETE")

	// Then: DescribeStreamSummary reflects every reconciled property.
	after := describeKinesisStreamSummary(t, srv, "cfn-reconcile-stream")
	if got, want := after["RetentionPeriodHours"], float64(72); got != want {
		t.Errorf("RetentionPeriodHours after update = %v, want %v", got, want)
	}
	afterMode, _ := after["StreamModeDetails"].(map[string]any)
	if got, want := afterMode["StreamMode"], "ON_DEMAND"; got != want {
		t.Errorf("StreamModeDetails.StreamMode after update = %v, want %v", got, want)
	}
	if got, want := after["EncryptionType"], "KMS"; got != want {
		t.Errorf("EncryptionType after update = %v, want %v", got, want)
	}
	if got, want := after["KeyId"], "alias/reconcile-key"; got != want {
		t.Errorf("KeyId after update = %v, want %v", got, want)
	}

	tags := listKinesisStreamTags(t, srv, "cfn-reconcile-stream")
	if tags["keep"] != "same" {
		t.Errorf("tags[keep] = %q, want same (unchanged key must survive)", tags["keep"])
	}
	if tags["add"] != "new-after-update" {
		t.Errorf("tags[add] = %q, want new-after-update", tags["add"])
	}
	if _, stillPresent := tags["drop"]; stillPresent {
		t.Errorf("tags still contain %q after it was removed from the template: %#v", "drop", tags)
	}

	// And: a second update that lowers retention and turns encryption back
	// off exercises the Decrease/Stop side of both reconciliations.
	ur2 := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {"kinesis-reconcile-stack"},
		"TemplateBody": {template(
			"24",
			"ON_DEMAND",
			"",
			`[{"Key": "keep", "Value": "same"}, {"Key": "add", "Value": "new-after-update"}]`,
		)},
	})
	defer ur2.Body.Close()
	helpers.AssertStatus(t, ur2, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-reconcile-stack", "UPDATE_COMPLETE")

	final := describeKinesisStreamSummary(t, srv, "cfn-reconcile-stream")
	if got, want := final["RetentionPeriodHours"], float64(24); got != want {
		t.Errorf("RetentionPeriodHours after second update = %v, want %v", got, want)
	}
	if got, want := final["EncryptionType"], "NONE"; got != want {
		t.Errorf("EncryptionType after second update = %v, want %v (StreamEncryption removed from template must stop encryption)", got, want)
	}
}

// TestUpdateStack_KinesisShardCountRequiresReplacement locks in the explicit
// disposition for ShardCount: Overcast does not implement UpdateShardCount
// (#1096), so a template that changes it must still force replacement rather
// than silently keep the old shard count — unlike RetentionPeriodHours,
// Tags, StreamEncryption and StreamModeDetails, which #531 made reconcile in
// place.
func TestUpdateStack_KinesisShardCountRequiresReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	// Name is deliberately left unset: a pinned name would make the
	// replacement's CreateStream collide with the still-live original
	// (ResourceInUseException) before the old stream is torn down — the same
	// reason SNS topics, SQS queues and Lambda functions all fall back to a
	// generated per-instance name instead of a deterministic one.
	template := func(shardCount int) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Stream": {
      "Type": "AWS::Kinesis::Stream",
      "Properties": {"ShardCount": ` + strconv.Itoa(shardCount) + `}
    }
  },
  "Outputs": {
    "StreamName": {"Value": {"Ref": "Stream"}}
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"kinesis-shardcount-stack"},
		"TemplateBody": {template(1)},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-shardcount-stack", "CREATE_COMPLETE")

	originalName := stackOutput(t, srv, "kinesis-shardcount-stack", "StreamName")
	before := describeKinesisStreamSummary(t, srv, originalName)
	if got, want := before["OpenShardCount"], float64(1); got != want {
		t.Fatalf("OpenShardCount after create = %v, want %v", got, want)
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"kinesis-shardcount-stack"},
		"TemplateBody": {template(2)},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "kinesis-shardcount-stack", "UPDATE_COMPLETE")

	newName := stackOutput(t, srv, "kinesis-shardcount-stack", "StreamName")
	if newName == originalName {
		t.Fatalf("stream name unchanged (%q) after a ShardCount update — expected a fresh, differently-named stream from replacement", newName)
	}
	after := describeKinesisStreamSummary(t, srv, newName)
	if got, want := after["OpenShardCount"], float64(2); got != want {
		t.Errorf("OpenShardCount after ShardCount update = %v, want %v (replacement should have provisioned a fresh stream at the new count)", got, want)
	}
}
