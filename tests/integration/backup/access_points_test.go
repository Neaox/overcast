// access_points_test.go — AWS Backup's six Backup Access Point operations
// (#1467), at the bindings the pinned model gives them:
//
//	PUT    /backup-access-point/create                            CreateBackupAccessPoint
//	GET    /backup-access-point/{AccessPointArn}                  DescribeBackupAccessPoint
//	DELETE /backup-access-point/delete/{AccessPointArn}           DeleteBackupAccessPoint
//	GET    /backup-access-point                                   ListBackupAccessPoints
//	POST   /backup-access-point/recovery-point/{RecoveryPointArn} ListBackupAccessPointsByRecoveryPoint
//	POST   /backup-access-point/resource/{ResourceArn}            ListBackupAccessPointsByResource
//
// The three roots above are one prefix, "/backup-access-point", which is a
// third subtree alongside /backup-vaults and /backup/plans rather than a
// member of either.
//
// Shapes are the API reference's, verified 2026-09-05:
// https://docs.aws.amazon.com/aws-backup/latest/APIReference/API_CreateBackupAccessPoint.html
// and its five siblings. Two things the issue assumed are not what the docs
// say, and both are asserted here: the members are AccessPointArn/Name/Status
// (no BackupAccessPointArn, and no BackupAccessPointAlias — the S3 alias lives
// inside the AccessPointMetadata map), and an access point is created against
// a RecoveryPointArn, not against a vault.
package backup_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

const pathAccessPoints = "/backup-access-point"

// recoveryPointArn is a well-formed AWS Backup recovery point ARN. No such
// recovery point exists — Overcast runs no backup jobs — so this is an opaque
// reference the emulator stores and echoes, which is what the metadata-only
// scope of this service means.
const recoveryPointArn = "arn:aws:backup:us-east-1:000000000000:recovery-point:1eb3b5e7-9eb0-435a-a80b-108b488b0d45"

// accessPointArn is the ARN CreateBackupAccessPoint mints for name, matching
// the modeled pattern
// (arn:aws[a-z-]*:backup:[a-z-\d]+:\d{12}:accesspoint/)[\da-z][\da-z-]{1,48}[\da-z].
func accessPointArn(name string) string {
	return "arn:aws:backup:us-east-1:000000000000:accesspoint/" + name
}

// arnPath renders an ARN as one path segment, the way an SDK binds an httpLabel
// carrying one: every reserved character, the ARN's own "/" included, is
// percent-encoded so the label cannot be read as extra segments.
func arnPath(arn string) string { return url.PathEscape(arn) }

// createAccessPoint creates an access point against recoveryPointArn and
// returns the decoded CreateBackupAccessPointOutput.
func createAccessPoint(t *testing.T, srv *helpers.TestServer, name string) map[string]any {
	t.Helper()
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":             name,
		"RecoveryPointArn": recoveryPointArn,
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusCreated)
	return decodeMap(t, resp)
}

// accessPointNames reads the Name of every member of a BackupAccessPoints list.
func accessPointNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["BackupAccessPoints"].([]any)
	if !ok {
		t.Fatalf("BackupAccessPoints = %#v, want a JSON array", body["BackupAccessPoints"])
	}
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		member, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("BackupAccessPoints member = %#v, want an object", item)
		}
		name, _ := member["Name"].(string)
		names = append(names, name)
	}
	return names
}

// ─── CreateBackupAccessPoint ─────────────────────────────────────────────────

func TestCreateBackupAccessPoint_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: CreateBackupAccessPoint is called at PUT /backup-access-point/create
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":                "ap-one",
		"RecoveryPointArn":    recoveryPointArn,
		"AccessPointMetadata": map[string]any{"AccessPointInTime": "2021-11-27T03:30:27Z"},
		"AccessPointPolicy":   `{"Version":"2012-10-17","Statement":[]}`,
		"Tags":                map[string]any{"env": "dev"},
	})
	defer resp.Body.Close()

	// Then: the modeled CreateBackupAccessPointOutput comes back as a 201 —
	// the only operation in this service that answers anything but 200.
	helpers.AssertStatus(t, resp, http.StatusCreated)
	helpers.AssertRequestID(t, resp)
	body := decodeMap(t, resp)
	if got := body["AccessPointArn"]; got != accessPointArn("ap-one") {
		t.Errorf("AccessPointArn = %v, want %v", got, accessPointArn("ap-one"))
	}
	// "A newly created backup access point begins in the CREATING state and
	// becomes usable when it reaches AVAILABLE" — so CREATING is what the
	// create answers with, and DescribeBackupAccessPoint below sees AVAILABLE.
	if got := body["Status"]; got != "CREATING" {
		t.Errorf("Status = %v, want CREATING", got)
	}
	// The output shape carries these two members and no others.
	if _, present := body["Name"]; present {
		t.Errorf("CreateBackupAccessPointOutput carries Name, which it does not model: %#v", body)
	}
}

func TestCreateBackupAccessPoint_duplicateName(t *testing.T) {
	// Given: an access point of that name already exists
	srv := helpers.NewTestServer(t)
	createAccessPoint(t, srv, "ap-one")

	// When: the same name is created again
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":             "ap-one",
		"RecoveryPointArn": recoveryPointArn,
	})
	defer resp.Body.Close()

	// Then: AlreadyExistsException, which backup answers 400 — none of its
	// client errors carry an httpError trait.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "AlreadyExistsException")
	helpers.AssertRequestID(t, resp)
}

func TestCreateBackupAccessPoint_missingRequiredMembers(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When/Then: Name and RecoveryPointArn are both required members
	cases := map[string]map[string]any{
		"no Name":             {"RecoveryPointArn": recoveryPointArn},
		"no RecoveryPointArn": {"Name": "ap-one"},
	}
	for scenario, body := range cases {
		t.Run(scenario, func(t *testing.T) {
			resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, body)
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "MissingParameterValueException")
		})
	}
}

func TestCreateBackupAccessPoint_invalidValues(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When/Then: values outside the modeled patterns are rejected. The Name
	// pattern is [\da-z]{1}[\da-z-]{1,48}[\da-z]{1} with negative lookbehinds
	// for the two reserved S3 alias suffixes.
	cases := map[string]map[string]any{
		"uppercase name":         {"Name": "AP-One", "RecoveryPointArn": recoveryPointArn},
		"name too short":         {"Name": "ap", "RecoveryPointArn": recoveryPointArn},
		"name ends with hyphen":  {"Name": "ap-", "RecoveryPointArn": recoveryPointArn},
		"reserved alias suffix":  {"Name": "ap-one-s3alias", "RecoveryPointArn": recoveryPointArn},
		"reserved ext suffix":    {"Name": "ap-one-ext-s3alias", "RecoveryPointArn": recoveryPointArn},
		"recovery point not arn": {"Name": "ap-one", "RecoveryPointArn": "not-an-arn"},
	}
	for scenario, body := range cases {
		t.Run(scenario, func(t *testing.T) {
			resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, body)
			defer resp.Body.Close()

			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
		})
	}
}

// ─── DescribeBackupAccessPoint ───────────────────────────────────────────────

func TestDescribeBackupAccessPoint_success(t *testing.T) {
	// Given: an access point exists
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":                "ap-one",
		"RecoveryPointArn":    recoveryPointArn,
		"AccessPointMetadata": map[string]any{"AccessPointInTime": "2021-11-27T03:30:27Z"},
	})
	resp.Body.Close()

	// When: it is described by ARN
	resp = backupDo(t, srv, http.MethodGet, pathAccessPoints+"/"+arnPath(accessPointArn("ap-one")), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: the modeled DescribeBackupAccessPointOutput comes back
	helpers.AssertStatus(t, resp, http.StatusOK)
	helpers.AssertRequestID(t, resp)
	body := decodeMap(t, resp)
	if got := body["AccessPointArn"]; got != accessPointArn("ap-one") {
		t.Errorf("AccessPointArn = %v", got)
	}
	if got := body["Name"]; got != "ap-one" {
		t.Errorf("Name = %v, want ap-one", got)
	}
	if got := body["RecoveryPointArn"]; got != recoveryPointArn {
		t.Errorf("RecoveryPointArn = %v, want %v", got, recoveryPointArn)
	}
	// Storage is synchronous, so the access point is usable by the time a
	// describe can observe it.
	if got := body["Status"]; got != "AVAILABLE" {
		t.Errorf("Status = %v, want AVAILABLE", got)
	}
	// CreationTime is a restJson1 timestamp with no timestampFormat trait, so
	// it is epoch seconds — a JSON number, as CreationDate is on a vault.
	if _, ok := body["CreationTime"].(float64); !ok {
		t.Errorf("CreationTime = %#v, want a JSON number", body["CreationTime"])
	}
	meta, ok := body["AccessPointMetadata"].(map[string]any)
	if !ok {
		t.Fatalf("AccessPointMetadata = %#v, want an object", body["AccessPointMetadata"])
	}
	if got := meta["AccessPointInTime"]; got != "2021-11-27T03:30:27Z" {
		t.Errorf("AccessPointMetadata[AccessPointInTime] = %v", got)
	}
	// On AWS the metadata also carries S3AccessPointArn/S3AccessPointAlias once
	// the access point is AVAILABLE. Overcast creates no S3 access point, so
	// inventing an alias that resolves to nothing would be the expensive kind
	// of divergence: absent is the honest answer.
	if _, present := meta["S3AccessPointAlias"]; present {
		t.Errorf("AccessPointMetadata invents S3AccessPointAlias: %#v", meta)
	}
}

func TestDescribeBackupAccessPoint_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: an access point that was never created is described
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints+"/"+arnPath(accessPointArn("ap-missing")), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: ResourceNotFoundException, at backup's 400
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestDescribeBackupAccessPoint_malformedArn(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: the label is not an access point ARN at all
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints+"/"+arnPath("arn:aws:backup:us-east-1:000000000000:backup-vault:vault-a"), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: InvalidParameterValueException — the value violates AccessPointArn's
	// modeled pattern, which is a different fault from naming an access point
	// that does not exist.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

// ─── ListBackupAccessPoints, and the delete round trip ───────────────────────

func TestListBackupAccessPoints_lifecycle(t *testing.T) {
	// Given: a vault and two access points
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	createAccessPoint(t, srv, "ap-one")
	createAccessPoint(t, srv, "ap-two")

	// When: they are listed
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints, defaultRegion, nil)
	defer resp.Body.Close()

	// Then: both come back, ordered by name
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := decodeMap(t, resp)
	if got := accessPointNames(t, body); len(got) != 2 || got[0] != "ap-one" || got[1] != "ap-two" {
		t.Fatalf("BackupAccessPoints = %v, want [ap-one ap-two]", got)
	}

	// When: one is deleted
	del := backupDo(t, srv, http.MethodDelete, pathAccessPoints+"/delete/"+arnPath(accessPointArn("ap-one")), defaultRegion, nil)
	defer del.Body.Close()

	// Then: 204 with an empty body — DeleteBackupAccessPoint's modeled output
	// is Unit and its documented response is "HTTP/1.1 204".
	helpers.AssertStatus(t, del, http.StatusNoContent)
	helpers.AssertRequestID(t, del)
	if got := helpers.ReadBody(t, del); got != "" {
		t.Errorf("delete body = %q, want empty", got)
	}

	// And: re-listing shows only the survivor
	after := backupDo(t, srv, http.MethodGet, pathAccessPoints, defaultRegion, nil)
	defer after.Body.Close()
	helpers.AssertStatus(t, after, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, after)); len(got) != 1 || got[0] != "ap-two" {
		t.Errorf("BackupAccessPoints after delete = %v, want [ap-two]", got)
	}
}

func TestListBackupAccessPoints_empty(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: the access points are listed
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints, defaultRegion, nil)
	defer resp.Body.Close()

	// Then: an empty list, not a null — BackupAccessPoints is a modeled list
	// member and an SDK reads a missing one as nil.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, resp)); len(got) != 0 {
		t.Errorf("BackupAccessPoints = %v, want empty", got)
	}
}

func TestListBackupAccessPoints_pagination(t *testing.T) {
	// Given: three access points
	srv := helpers.NewTestServer(t)
	for i := 1; i <= 3; i++ {
		createAccessPoint(t, srv, fmt.Sprintf("ap-%d", i))
	}

	// When: a page of two is asked for. The query members are MaxResults and
	// NextToken with a leading capital, unlike ListBackupVaults' maxResults/
	// nextToken — the model spells the two families differently.
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints+"?MaxResults=2", defaultRegion, nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	first := decodeMap(t, resp)

	// Then: two come back with a continuation token
	if got := accessPointNames(t, first); len(got) != 2 {
		t.Fatalf("page one = %v, want two items", got)
	}
	token, _ := first["NextToken"].(string)
	if token == "" {
		t.Fatal("page one carries no NextToken")
	}

	// And: the token yields the rest
	next := backupDo(t, srv, http.MethodGet, pathAccessPoints+"?MaxResults=2&NextToken="+url.QueryEscape(token), defaultRegion, nil)
	defer next.Body.Close()
	helpers.AssertStatus(t, next, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, next)); len(got) != 1 || got[0] != "ap-3" {
		t.Errorf("page two = %v, want [ap-3]", got)
	}
}

func TestListBackupAccessPoints_maxResultsOutOfRange(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: MaxResults is above the modeled range of 1-100 — a hundredth of
	// ListBackupVaults' ceiling, which is 1000
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints+"?MaxResults=101", defaultRegion, nil)
	defer resp.Body.Close()

	// Then: InvalidParameterValueException
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

func TestListBackupAccessPoints_regionScoped(t *testing.T) {
	// Given: an access point in us-east-1
	srv := helpers.NewTestServer(t)
	createAccessPoint(t, srv, "ap-one")

	// When: another region lists its access points
	resp := backupDo(t, srv, http.MethodGet, pathAccessPoints, "eu-west-1", nil)
	defer resp.Body.Close()

	// Then: it sees none of them — access points are per account per Region,
	// as vaults are.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, resp)); len(got) != 0 {
		t.Errorf("eu-west-1 sees %v, want none", got)
	}
}

// ─── DeleteBackupAccessPoint ─────────────────────────────────────────────────

func TestDeleteBackupAccessPoint_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: an access point that was never created is deleted
	resp := backupDo(t, srv, http.MethodDelete, pathAccessPoints+"/delete/"+arnPath(accessPointArn("ap-missing")), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: ResourceNotFoundException rather than a silent 204
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// ─── ListBackupAccessPointsByRecoveryPoint ───────────────────────────────────

func TestListBackupAccessPointsByRecoveryPoint_matchingArn(t *testing.T) {
	// Given: an access point created against recoveryPointArn
	srv := helpers.NewTestServer(t)
	createAccessPoint(t, srv, "ap-one")

	// When: that recovery point's access points are listed
	resp := backupDo(t, srv, http.MethodPost, pathAccessPoints+"/recovery-point/"+arnPath(recoveryPointArn), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: the access point that named it comes back
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, resp)); len(got) != 1 || got[0] != "ap-one" {
		t.Errorf("BackupAccessPoints = %v, want [ap-one]", got)
	}
}

func TestListBackupAccessPointsByRecoveryPoint_noRecoveryPointsExist(t *testing.T) {
	// Given: an access point exists, created against some other recovery point
	srv := helpers.NewTestServer(t)
	createVault(t, srv, "vault-a")
	createAccessPoint(t, srv, "ap-one")

	// When: a recovery point in the vault Overcast just created is asked for
	// its access points. This is the case the issue calls out, and the empty
	// answer is deliberate rather than a gap: Overcast runs no backup jobs, so
	// no recovery point is ever created, and nothing can reference one.
	other := "arn:aws:backup:us-east-1:000000000000:recovery-point:00000000-0000-0000-0000-000000000000"
	resp := backupDo(t, srv, http.MethodPost, pathAccessPoints+"/recovery-point/"+arnPath(other), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: an empty list, not an error
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, resp)); len(got) != 0 {
		t.Errorf("BackupAccessPoints = %v, want empty", got)
	}
}

// ─── ListBackupAccessPointsByResource ────────────────────────────────────────

func TestListBackupAccessPointsByResource_noResourceIsKnown(t *testing.T) {
	// Given: an access point exists
	srv := helpers.NewTestServer(t)
	createAccessPoint(t, srv, "ap-one")

	// When: the bucket that would have been backed up is asked for its access
	// points
	resp := backupDo(t, srv, http.MethodPost, pathAccessPoints+"/resource/"+arnPath("arn:aws:s3:::my-bucket"), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: an empty list. An access point's ResourceArn is a property of the
	// recovery point it was created against, and Overcast creates no recovery
	// points, so it never learns which resource an access point belongs to —
	// the same reason ByRecoveryPoint above finds nothing for a real one. The
	// answer is truthful rather than stubbed: the filter runs, and no stored
	// access point carries a ResourceArn for it to match.
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := accessPointNames(t, decodeMap(t, resp)); len(got) != 0 {
		t.Errorf("BackupAccessPoints = %v, want empty", got)
	}
}

// ─── Tags ────────────────────────────────────────────────────────────────────

func TestListTags_backupAccessPoint(t *testing.T) {
	// Given: an access point created with tags
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":             "ap-one",
		"RecoveryPointArn": recoveryPointArn,
		"Tags":             map[string]any{"env": "dev"},
	})
	resp.Body.Close()

	// When: its tags are listed through the shared /tags dispatcher
	resp = backupDo(t, srv, http.MethodGet, "/tags/"+arnPath(accessPointArn("ap-one")), defaultRegion, nil)
	defer resp.Body.Close()

	// Then: the tags CreateBackupAccessPoint's Tags member carried come back —
	// nothing else in this operation set returns them, so without this the
	// member would be accepted and silently dropped.
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := decodeMap(t, resp)
	tags, ok := body["Tags"].(map[string]any)
	if !ok {
		t.Fatalf("Tags = %#v, want an object", body["Tags"])
	}
	if got := tags["env"]; got != "dev" {
		t.Errorf("Tags[env] = %v, want dev", got)
	}
}

func TestTagResource_backupAccessPoint(t *testing.T) {
	// Given: an untagged access point
	srv := helpers.NewTestServer(t)
	createAccessPoint(t, srv, "ap-one")
	arn := accessPointArn("ap-one")

	// When: it is tagged and then untagged through the shared dispatcher.
	// An access point ARN's resource component is "accesspoint/ap-one", a
	// slash where a vault's and a plan's carry a colon, so it is the one
	// Backup ARN shape resolveTagTarget cannot read by splitting on ":".
	tag := backupDo(t, srv, http.MethodPost, "/tags/"+arnPath(arn), defaultRegion, map[string]any{
		"Tags": map[string]any{"env": "dev", "team": "platform"},
	})
	tag.Body.Close()
	helpers.AssertStatus(t, tag, http.StatusOK)

	untag := backupDo(t, srv, http.MethodPost, "/untag/"+arnPath(arn), defaultRegion, map[string]any{
		"TagKeyList": []string{"env"},
	})
	untag.Body.Close()
	helpers.AssertStatus(t, untag, http.StatusOK)

	// Then: only the key that was not removed survives
	resp := backupDo(t, srv, http.MethodGet, "/tags/"+arnPath(arn), defaultRegion, nil)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	tags, ok := decodeMap(t, resp)["Tags"].(map[string]any)
	if !ok {
		t.Fatalf("Tags is not an object")
	}
	if len(tags) != 1 || tags["team"] != "platform" {
		t.Errorf("Tags = %v, want only team=platform", tags)
	}
}

func TestDeleteBackupAccessPoint_dropsItsTags(t *testing.T) {
	// Given: a tagged access point that is then deleted
	srv := helpers.NewTestServer(t)
	resp := backupDo(t, srv, http.MethodPut, pathAccessPoints+"/create", defaultRegion, map[string]any{
		"Name":             "ap-one",
		"RecoveryPointArn": recoveryPointArn,
		"Tags":             map[string]any{"env": "dev"},
	})
	resp.Body.Close()
	del := backupDo(t, srv, http.MethodDelete,
		pathAccessPoints+"/delete/"+arnPath(accessPointArn("ap-one")), defaultRegion, nil)
	del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusNoContent)

	// When: its tags are listed
	after := backupDo(t, srv, http.MethodGet, "/tags/"+arnPath(accessPointArn("ap-one")), defaultRegion, nil)
	defer after.Body.Close()

	// Then: the record is gone and so are the tags stored inline on it —
	// nothing outlives the record it describes.
	helpers.AssertStatus(t, after, http.StatusBadRequest)
	helpers.AssertJSONError(t, after, "ResourceNotFoundException")
}
