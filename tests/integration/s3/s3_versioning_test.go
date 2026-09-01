// Object version history over HTTP: version ids on writes, version-targeted
// reads and deletes, delete markers, ListObjectVersions ordering and
// pagination, the versioning state transitions, and the compatibility
// guarantees for buckets that were never versioned.
//
// TDD contract: every test here was written against AWS's documented shapes
// before internal/services/s3/version.go existed, and failed until it did —
// version ids came back as "null", delete markers were indistinguishable from
// permanent deletes, and a second PUT overwrote the first with nothing kept.
package s3_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ---- Wire shapes -----------------------------------------------------------

// listVersionsResult mirrors AWS's ListVersionsResult. It is declared here
// rather than reused from the handler so the test asserts the wire format
// independently of the implementation's own structs.
type listVersionsResult struct {
	XMLName             xml.Name          `xml:"ListVersionsResult"`
	Name                string            `xml:"Name"`
	Prefix              string            `xml:"Prefix"`
	Delimiter           string            `xml:"Delimiter"`
	MaxKeys             int               `xml:"MaxKeys"`
	IsTruncated         bool              `xml:"IsTruncated"`
	KeyMarker           string            `xml:"KeyMarker"`
	NextKeyMarker       string            `xml:"NextKeyMarker"`
	VersionIdMarker     string            `xml:"VersionIdMarker"`
	NextVersionIdMarker string            `xml:"NextVersionIdMarker"`
	Versions            []versionEntry    `xml:"Version"`
	DeleteMarkers       []deleteMarker    `xml:"DeleteMarker"`
	CommonPrefixes      []commonPrefixXML `xml:"CommonPrefixes"`
}

type versionEntry struct {
	Key          string `xml:"Key"`
	VersionId    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type deleteMarker struct {
	Key       string `xml:"Key"`
	VersionId string `xml:"VersionId"`
	IsLatest  bool   `xml:"IsLatest"`
}

type commonPrefixXML struct {
	Prefix string `xml:"Prefix"`
}

// ---- Local helpers ---------------------------------------------------------

func enableVersioning(t *testing.T, srv *helpers.TestServer, bucket string) {
	t.Helper()
	setVersioning(t, srv, bucket, "Enabled")
}

func setVersioning(t *testing.T, srv *helpers.TestServer, bucket, status string) {
	t.Helper()
	body := "<VersioningConfiguration><Status>" + status + "</Status></VersioningConfiguration>"
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"?versioning", []byte(body), nil))
	if err != nil {
		t.Fatalf("PutBucketVersioning %s: %v", status, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// putVersioned stores an object and returns the x-amz-version-id the write
// reported, which is what a caller uses to address that version later.
func putVersioned(t *testing.T, srv *helpers.TestServer, bucket, key string, body []byte) string {
	t.Helper()
	resp, err := http.DefaultClient.Do(put(srv, "/"+bucket+"/"+key, body,
		map[string]string{"Content-Type": "text/plain"}))
	if err != nil {
		t.Fatalf("PutObject %s/%s: %v", bucket, key, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return resp.Header.Get("x-amz-version-id")
}

func getVersion(t *testing.T, srv *helpers.TestServer, bucket, key, versionID string) *http.Response {
	t.Helper()
	path := "/" + bucket + "/" + key
	if versionID != "" {
		path += "?versionId=" + versionID
	}
	resp, err := http.DefaultClient.Do(get(srv, path))
	if err != nil {
		t.Fatalf("GetObject %s: %v", path, err)
	}
	return resp
}

func deleteVersion(t *testing.T, srv *helpers.TestServer, bucket, key, versionID string) *http.Response {
	t.Helper()
	path := "/" + bucket + "/" + key
	if versionID != "" {
		path += "?versionId=" + versionID
	}
	resp, err := http.DefaultClient.Do(del(srv, path))
	if err != nil {
		t.Fatalf("DeleteObject %s: %v", path, err)
	}
	return resp
}

func listVersions(t *testing.T, srv *helpers.TestServer, bucket, query string) listVersionsResult {
	t.Helper()
	path := "/" + bucket + "?versions"
	if query != "" {
		path += "&" + query
	}
	resp, err := http.DefaultClient.Do(get(srv, path))
	if err != nil {
		t.Fatalf("ListObjectVersions: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result listVersionsResult
	body, _ := io.ReadAll(resp.Body)
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("ListObjectVersions: unmarshal XML: %v\nbody: %s", err, body)
	}
	return result
}

func assertBody(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	got := helpers.ReadBody(t, resp)
	if got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// ---- Version ids -----------------------------------------------------------

// TestPutObject_versionedBucketMintsVersionIds asserts the property AWS's
// version ids have that Overcast depends on: they are opaque, URL-safe and
// distinct per write.
func TestPutObject_versionedBucketMintsVersionIds(t *testing.T) {
	// Given: a versioning-enabled bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-mint")
	enableVersioning(t, srv, "ver-mint")

	// When: the same key is written three times
	ids := []string{
		putVersioned(t, srv, "ver-mint", "a.txt", []byte("v1")),
		putVersioned(t, srv, "ver-mint", "a.txt", []byte("v2")),
		putVersioned(t, srv, "ver-mint", "a.txt", []byte("v3")),
	}

	// Then: each write reports its own id, and none of them is "null"
	seen := map[string]bool{}
	for i, id := range ids {
		if id == "" {
			t.Fatalf("write %d reported no x-amz-version-id", i+1)
		}
		if id == "null" {
			t.Errorf("write %d reported the null version id in an enabled bucket", i+1)
		}
		if seen[id] {
			t.Errorf("write %d reused version id %q", i+1, id)
		}
		seen[id] = true
		if strings.ContainsAny(id, "/?&= +%") {
			t.Errorf("version id %q is not URL-safe", id)
		}
	}
}

// TestPutObject_unversionedBucketReportsNoVersionId is the compatibility
// guarantee: a bucket that never enables versioning behaves exactly as it did
// before version history existed, down to the absent header.
func TestPutObject_unversionedBucketReportsNoVersionId(t *testing.T) {
	// Given: a bucket with no versioning configuration
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-none")

	// When: an object is written twice
	putVersioned(t, srv, "ver-none", "a.txt", []byte("first"))
	if id := putVersioned(t, srv, "ver-none", "a.txt", []byte("second")); id != "" {
		t.Errorf("x-amz-version-id = %q on an unversioned bucket, want no header", id)
	}

	// Then: the second write replaced the first, and nothing else is kept
	resp := getVersion(t, srv, "ver-none", "a.txt", "")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if id := resp.Header.Get("x-amz-version-id"); id != "" {
		t.Errorf("GetObject x-amz-version-id = %q, want no header", id)
	}
	assertBody(t, resp, "second")

	result := listVersions(t, srv, "ver-none", "")
	if len(result.Versions) != 1 || result.Versions[0].VersionId != "null" {
		t.Errorf("want one null version, got %+v", result.Versions)
	}
}

// ---- Version-targeted reads ------------------------------------------------

func TestGetObject_byVersionIdReturnsThatVersion(t *testing.T) {
	// Given: a versioned key with two versions
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-read")
	enableVersioning(t, srv, "ver-read")
	first := putVersioned(t, srv, "ver-read", "a.txt", []byte("one"))
	second := putVersioned(t, srv, "ver-read", "a.txt", []byte("two"))

	// When + Then: the current read gives the newest version
	current := getVersion(t, srv, "ver-read", "a.txt", "")
	defer current.Body.Close()
	helpers.AssertStatus(t, current, http.StatusOK)
	if got := current.Header.Get("x-amz-version-id"); got != second {
		t.Errorf("current x-amz-version-id = %q, want %q", got, second)
	}
	assertBody(t, current, "two")

	// And: the noncurrent version is still readable by id
	old := getVersion(t, srv, "ver-read", "a.txt", first)
	defer old.Body.Close()
	helpers.AssertStatus(t, old, http.StatusOK)
	if got := old.Header.Get("x-amz-version-id"); got != first {
		t.Errorf("old x-amz-version-id = %q, want %q", got, first)
	}
	assertBody(t, old, "one")
}

func TestGetObject_unknownVersionId(t *testing.T) {
	// Given: a versioned key
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-unknown")
	enableVersioning(t, srv, "ver-unknown")
	putVersioned(t, srv, "ver-unknown", "a.txt", []byte("one"))

	// When: a version id that was never minted is requested
	resp := getVersion(t, srv, "ver-unknown", "a.txt", "NOTAREALVERSIONIDXXXXXXX")
	defer resp.Body.Close()

	// Then: AWS's NoSuchVersion
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertXMLError(t, resp, "NoSuchVersion")
}

// ---- Delete markers --------------------------------------------------------

// TestDeleteObject_versionedBucketCreatesADeleteMarker is the behaviour the
// whole feature turns on: a delete in a versioned bucket removes nothing.
func TestDeleteObject_versionedBucketCreatesADeleteMarker(t *testing.T) {
	// Given: a versioned key with one version
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-marker")
	enableVersioning(t, srv, "ver-marker")
	original := putVersioned(t, srv, "ver-marker", "a.txt", []byte("hello"))

	// When: the key is deleted with no version id
	del := deleteVersion(t, srv, "ver-marker", "a.txt", "")
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusNoContent)
	markerID := del.Header.Get("x-amz-version-id")
	if del.Header.Get("x-amz-delete-marker") != "true" {
		t.Error("delete did not report x-amz-delete-marker: true")
	}
	if markerID == "" || markerID == original {
		t.Errorf("delete marker version id = %q, want a new id (original %q)", markerID, original)
	}

	// Then: a plain GET is a 404 that says the object was deleted, not that it
	// never existed
	missing := getVersion(t, srv, "ver-marker", "a.txt", "")
	defer missing.Body.Close()
	helpers.AssertStatus(t, missing, http.StatusNotFound)
	if missing.Header.Get("x-amz-delete-marker") != "true" {
		t.Error("GET of a delete-marked key did not report x-amz-delete-marker: true")
	}

	// And: the original version is still there behind the marker
	old := getVersion(t, srv, "ver-marker", "a.txt", original)
	defer old.Body.Close()
	helpers.AssertStatus(t, old, http.StatusOK)
	assertBody(t, old, "hello")

	// And: the key is gone from ListObjectsV2, which never shows markers
	listed, err := http.DefaultClient.Do(get(srv, "/ver-marker?list-type=2"))
	if err != nil {
		t.Fatal(err)
	}
	defer listed.Body.Close()
	if body := helpers.ReadBody(t, listed); strings.Contains(body, "<Key>a.txt</Key>") {
		t.Errorf("ListObjectsV2 still lists a delete-marked key: %s", body)
	}
}

// TestGetObject_deleteMarkerVersionIdIsMethodNotAllowed covers the other of
// AWS's two "nothing to read" answers: the version exists and simply cannot be
// read, which is a 405 rather than a 404.
func TestGetObject_deleteMarkerVersionIdIsMethodNotAllowed(t *testing.T) {
	// Given: a delete-marked key
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-405")
	enableVersioning(t, srv, "ver-405")
	putVersioned(t, srv, "ver-405", "a.txt", []byte("hello"))
	marker := deleteVersion(t, srv, "ver-405", "a.txt", "")
	markerID := marker.Header.Get("x-amz-version-id")
	marker.Body.Close()

	// When: the delete marker's own version id is fetched
	resp := getVersion(t, srv, "ver-405", "a.txt", markerID)
	defer resp.Body.Close()

	// Then: 405 MethodNotAllowed, with the marker flag and an Allow header
	helpers.AssertStatus(t, resp, http.StatusMethodNotAllowed)
	helpers.AssertXMLError(t, resp, "MethodNotAllowed")
	if resp.Header.Get("x-amz-delete-marker") != "true" {
		t.Error("missing x-amz-delete-marker on a delete-marker read")
	}
	if resp.Header.Get("Allow") != "DELETE" {
		t.Errorf("Allow = %q, want DELETE", resp.Header.Get("Allow"))
	}
}

// TestDeleteObject_removingADeleteMarkerRestoresTheObject is AWS's documented
// undelete: remove the marker by version id and the previous version becomes
// current again.
func TestDeleteObject_removingADeleteMarkerRestoresTheObject(t *testing.T) {
	// Given: a delete-marked key with one version behind the marker
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-undelete")
	enableVersioning(t, srv, "ver-undelete")
	putVersioned(t, srv, "ver-undelete", "a.txt", []byte("hello"))
	marker := deleteVersion(t, srv, "ver-undelete", "a.txt", "")
	markerID := marker.Header.Get("x-amz-version-id")
	marker.Body.Close()

	// When: the marker is deleted by version id
	removed := deleteVersion(t, srv, "ver-undelete", "a.txt", markerID)
	defer removed.Body.Close()
	helpers.AssertStatus(t, removed, http.StatusNoContent)
	if removed.Header.Get("x-amz-delete-marker") != "true" {
		t.Error("removing a delete marker did not report x-amz-delete-marker: true")
	}

	// Then: the object is readable again
	resp := getVersion(t, srv, "ver-undelete", "a.txt", "")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	assertBody(t, resp, "hello")
}

// TestDeleteObject_versionTargetedDeletePromotesTheNewestSurvivor covers the
// invariant the whole current-version pointer rests on.
func TestDeleteObject_versionTargetedDeletePromotesTheNewestSurvivor(t *testing.T) {
	// Given: three versions of a key
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-promote")
	enableVersioning(t, srv, "ver-promote")
	putVersioned(t, srv, "ver-promote", "a.txt", []byte("one"))
	second := putVersioned(t, srv, "ver-promote", "a.txt", []byte("two"))
	third := putVersioned(t, srv, "ver-promote", "a.txt", []byte("three"))

	// When: the current version is permanently deleted
	resp := deleteVersion(t, srv, "ver-promote", "a.txt", third)
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusNoContent)

	// Then: the next-newest version is current, and no delete marker appeared
	current := getVersion(t, srv, "ver-promote", "a.txt", "")
	defer current.Body.Close()
	helpers.AssertStatus(t, current, http.StatusOK)
	assertBody(t, current, "two")
	if got := current.Header.Get("x-amz-version-id"); got != second {
		t.Errorf("current version = %q, want %q", got, second)
	}

	result := listVersions(t, srv, "ver-promote", "")
	if len(result.DeleteMarkers) != 0 {
		t.Errorf("a version-targeted delete created a delete marker: %+v", result.DeleteMarkers)
	}
	if len(result.Versions) != 2 {
		t.Errorf("want 2 surviving versions, got %d", len(result.Versions))
	}
}

// ---- Suspended buckets -----------------------------------------------------

// TestVersioningSuspended_writesTheNullVersion covers the state that is easiest
// to get wrong: Suspended is not "off". Existing versions stay, new writes
// become the single null version, and a delete still leaves a marker behind.
func TestVersioningSuspended_writesTheNullVersion(t *testing.T) {
	// Given: a bucket with one real version, then suspended
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-suspend")
	enableVersioning(t, srv, "ver-suspend")
	kept := putVersioned(t, srv, "ver-suspend", "a.txt", []byte("enabled"))
	setVersioning(t, srv, "ver-suspend", "Suspended")

	// When: two more writes land while suspended
	if id := putVersioned(t, srv, "ver-suspend", "a.txt", []byte("suspended-1")); id != "null" {
		t.Errorf("suspended write reported version id %q, want null", id)
	}
	putVersioned(t, srv, "ver-suspend", "a.txt", []byte("suspended-2"))

	// Then: they share the one null version, and the enabled-era version is
	// still there
	result := listVersions(t, srv, "ver-suspend", "")
	if len(result.Versions) != 2 {
		t.Fatalf("want 2 versions (null + the retained one), got %d: %+v",
			len(result.Versions), result.Versions)
	}
	if result.Versions[0].VersionId != "null" || !result.Versions[0].IsLatest {
		t.Errorf("newest version = %+v, want the null version as latest", result.Versions[0])
	}
	if result.Versions[1].VersionId != kept {
		t.Errorf("retained version = %q, want %q", result.Versions[1].VersionId, kept)
	}

	current := getVersion(t, srv, "ver-suspend", "a.txt", "")
	defer current.Body.Close()
	assertBody(t, current, "suspended-2")

	// And: a delete still leaves a marker, with the null version id
	del := deleteVersion(t, srv, "ver-suspend", "a.txt", "")
	defer del.Body.Close()
	helpers.AssertStatus(t, del, http.StatusNoContent)
	if del.Header.Get("x-amz-delete-marker") != "true" {
		t.Error("a suspended-bucket delete did not create a delete marker")
	}
	if got := del.Header.Get("x-amz-version-id"); got != "null" {
		t.Errorf("suspended delete marker version id = %q, want null", got)
	}
}

// TestVersioningEnabled_afterObjectsExistTreatsThemAsNullVersions is the
// migration story stated as a test: an object that predates versioning is the
// null version of its key, exactly as on AWS.
func TestVersioningEnabled_afterObjectsExistTreatsThemAsNullVersions(t *testing.T) {
	// Given: a bucket with an object, versioned only afterwards
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-late")
	putVersioned(t, srv, "ver-late", "a.txt", []byte("before"))
	enableVersioning(t, srv, "ver-late")

	// When: a second version is written
	newer := putVersioned(t, srv, "ver-late", "a.txt", []byte("after"))

	// Then: both are listed, newest first, with the pre-existing one as null
	result := listVersions(t, srv, "ver-late", "")
	if len(result.Versions) != 2 {
		t.Fatalf("want 2 versions, got %d: %+v", len(result.Versions), result.Versions)
	}
	if result.Versions[0].VersionId != newer || !result.Versions[0].IsLatest {
		t.Errorf("newest = %+v, want %q as latest", result.Versions[0], newer)
	}
	if result.Versions[1].VersionId != "null" || result.Versions[1].IsLatest {
		t.Errorf("oldest = %+v, want the null version, not latest", result.Versions[1])
	}

	// And: the pre-existing object is still readable, by "null" and by content
	old := getVersion(t, srv, "ver-late", "a.txt", "null")
	defer old.Body.Close()
	helpers.AssertStatus(t, old, http.StatusOK)
	assertBody(t, old, "before")
}

// ---- ListObjectVersions ----------------------------------------------------

func TestListObjectVersions_orderIsKeyThenRecency(t *testing.T) {
	// Given: two keys with interleaved writes
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-order")
	enableVersioning(t, srv, "ver-order")
	a1 := putVersioned(t, srv, "ver-order", "a.txt", []byte("a1"))
	b1 := putVersioned(t, srv, "ver-order", "b.txt", []byte("b1"))
	a2 := putVersioned(t, srv, "ver-order", "a.txt", []byte("a2"))
	b2 := putVersioned(t, srv, "ver-order", "b.txt", []byte("b2"))

	// When: versions are listed
	result := listVersions(t, srv, "ver-order", "")

	// Then: keys ascend and, within a key, the newest version comes first
	want := []struct{ key, version string }{
		{"a.txt", a2}, {"a.txt", a1}, {"b.txt", b2}, {"b.txt", b1},
	}
	if len(result.Versions) != len(want) {
		t.Fatalf("want %d versions, got %d: %+v", len(want), len(result.Versions), result.Versions)
	}
	for i, w := range want {
		got := result.Versions[i]
		if got.Key != w.key || got.VersionId != w.version {
			t.Errorf("entry %d = (%s, %s), want (%s, %s)", i, got.Key, got.VersionId, w.key, w.version)
		}
		if wantLatest := i%2 == 0; got.IsLatest != wantLatest {
			t.Errorf("entry %d IsLatest = %v, want %v", i, got.IsLatest, wantLatest)
		}
	}
}

func TestListObjectVersions_paginatesByKeyAndVersion(t *testing.T) {
	// Given: one key with three versions and one more key after it
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-page")
	enableVersioning(t, srv, "ver-page")
	putVersioned(t, srv, "ver-page", "a.txt", []byte("1"))
	putVersioned(t, srv, "ver-page", "a.txt", []byte("2"))
	putVersioned(t, srv, "ver-page", "a.txt", []byte("3"))
	putVersioned(t, srv, "ver-page", "b.txt", []byte("4"))

	// When: the listing is walked two entries at a time
	var seen []string
	query := "max-keys=2"
	for page := 0; page < 5; page++ {
		result := listVersions(t, srv, "ver-page", query)
		for _, v := range result.Versions {
			seen = append(seen, v.Key+"/"+v.VersionId)
		}
		if !result.IsTruncated {
			break
		}
		if result.NextKeyMarker == "" {
			t.Fatal("truncated page carried no NextKeyMarker")
		}
		query = "max-keys=2&key-marker=" + result.NextKeyMarker
		if result.NextVersionIdMarker != "" {
			query += "&version-id-marker=" + result.NextVersionIdMarker
		}
	}

	// Then: every version was returned exactly once, in order
	if len(seen) != 4 {
		t.Fatalf("walked %d entries, want 4: %v", len(seen), seen)
	}
	unique := map[string]bool{}
	for _, s := range seen {
		if unique[s] {
			t.Errorf("entry %s returned twice across pages: %v", s, seen)
		}
		unique[s] = true
	}
	if !strings.HasPrefix(seen[3], "b.txt/") {
		t.Errorf("last entry = %s, want b.txt's version", seen[3])
	}
}

func TestListObjectVersions_interleavesDeleteMarkers(t *testing.T) {
	// Given: a key whose history mixes versions and markers
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-mixed")
	enableVersioning(t, srv, "ver-mixed")
	putVersioned(t, srv, "ver-mixed", "a.txt", []byte("one"))
	dm := deleteVersion(t, srv, "ver-mixed", "a.txt", "")
	markerID := dm.Header.Get("x-amz-version-id")
	dm.Body.Close()

	// When: versions are listed
	result := listVersions(t, srv, "ver-mixed", "")

	// Then: the marker is reported as a DeleteMarker element, and it is latest
	if len(result.DeleteMarkers) != 1 {
		t.Fatalf("want 1 delete marker, got %d", len(result.DeleteMarkers))
	}
	if result.DeleteMarkers[0].VersionId != markerID || !result.DeleteMarkers[0].IsLatest {
		t.Errorf("delete marker = %+v, want %q as latest", result.DeleteMarkers[0], markerID)
	}
	if len(result.Versions) != 1 || result.Versions[0].IsLatest {
		t.Errorf("versions = %+v, want one non-latest version", result.Versions)
	}
}

func TestListObjectVersions_delimiterCollapsesToCommonPrefixes(t *testing.T) {
	// Given: keys under a shared prefix, plus one at the root
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-delim")
	enableVersioning(t, srv, "ver-delim")
	putVersioned(t, srv, "ver-delim", "docs/a.txt", []byte("a"))
	putVersioned(t, srv, "ver-delim", "docs/b.txt", []byte("b"))
	putVersioned(t, srv, "ver-delim", "root.txt", []byte("r"))

	// When: the listing uses a delimiter
	result := listVersions(t, srv, "ver-delim", "delimiter=/")

	// Then: the prefix collapses and only the root key is listed
	if len(result.CommonPrefixes) != 1 || result.CommonPrefixes[0].Prefix != "docs/" {
		t.Errorf("CommonPrefixes = %+v, want [docs/]", result.CommonPrefixes)
	}
	if len(result.Versions) != 1 || result.Versions[0].Key != "root.txt" {
		t.Errorf("Versions = %+v, want only root.txt", result.Versions)
	}
}

func TestListObjectVersions_versionIdMarkerWithoutKeyMarker(t *testing.T) {
	// Given: a versioned bucket
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-marker-arg")
	enableVersioning(t, srv, "ver-marker-arg")

	// When: a version-id-marker is supplied on its own
	resp, err := http.DefaultClient.Do(get(srv, "/ver-marker-arg?versions&version-id-marker=abc"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: AWS refuses it
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertXMLError(t, resp, "InvalidArgument")
}

// ---- Batch delete ----------------------------------------------------------

func TestDeleteObjects_versionedBucketReportsMarkers(t *testing.T) {
	// Given: two versioned keys
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-batch")
	enableVersioning(t, srv, "ver-batch")
	putVersioned(t, srv, "ver-batch", "a.txt", []byte("a"))
	bVersion := putVersioned(t, srv, "ver-batch", "b.txt", []byte("b"))

	// When: one key is deleted plainly and one by version id
	body := "<Delete><Object><Key>a.txt</Key></Object>" +
		"<Object><Key>b.txt</Key><VersionId>" + bVersion + "</VersionId></Object></Delete>"
	resp, err := http.DefaultClient.Do(
		mustReq(http.MethodPost, srv.URL+"/ver-batch?delete", strings.NewReader(body), nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var result struct {
		XMLName xml.Name `xml:"DeleteResult"`
		Deleted []struct {
			Key                   string `xml:"Key"`
			VersionId             string `xml:"VersionId"`
			DeleteMarker          bool   `xml:"DeleteMarker"`
			DeleteMarkerVersionId string `xml:"DeleteMarkerVersionId"`
		} `xml:"Deleted"`
	}
	raw := helpers.ReadBody(t, resp)
	if err := xml.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}

	// Then: the plain delete reports a new marker, the targeted one reports the
	// version it removed
	if len(result.Deleted) != 2 {
		t.Fatalf("want 2 Deleted entries, got %d: %s", len(result.Deleted), raw)
	}
	for _, d := range result.Deleted {
		switch d.Key {
		case "a.txt":
			if !d.DeleteMarker || d.DeleteMarkerVersionId == "" {
				t.Errorf("a.txt = %+v, want a created delete marker", d)
			}
		case "b.txt":
			if d.DeleteMarker || d.VersionId != bVersion {
				t.Errorf("b.txt = %+v, want the removed version %q", d, bVersion)
			}
		}
	}

	// And: the version-targeted delete really removed b.txt entirely
	result2 := listVersions(t, srv, "ver-batch", "prefix=b.txt")
	if len(result2.Versions) != 0 || len(result2.DeleteMarkers) != 0 {
		t.Errorf("b.txt still has history: %+v %+v", result2.Versions, result2.DeleteMarkers)
	}
}

// ---- Copy ------------------------------------------------------------------

func TestCopyObject_fromASpecificSourceVersion(t *testing.T) {
	// Given: a versioned source key with two versions
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-copy")
	enableVersioning(t, srv, "ver-copy")
	first := putVersioned(t, srv, "ver-copy", "src.txt", []byte("old"))
	putVersioned(t, srv, "ver-copy", "src.txt", []byte("new"))

	// When: the older version is copied
	resp, err := http.DefaultClient.Do(put(srv, "/ver-copy/dst.txt", nil, map[string]string{
		"x-amz-copy-source": "/ver-copy/src.txt?versionId=" + first,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	if got := resp.Header.Get("x-amz-copy-source-version-id"); got != first {
		t.Errorf("x-amz-copy-source-version-id = %q, want %q", got, first)
	}

	// Then: the destination holds that version's bytes
	dst := getVersion(t, srv, "ver-copy", "dst.txt", "")
	defer dst.Body.Close()
	helpers.AssertStatus(t, dst, http.StatusOK)
	assertBody(t, dst, "old")
}

// ---- Bucket emptiness ------------------------------------------------------

// TestDeleteBucket_notEmptyWhileADeleteMarkerRemains matches AWS: a bucket
// whose keys are all delete-marked is not empty, because the markers and the
// versions under them are still stored.
func TestDeleteBucket_notEmptyWhileADeleteMarkerRemains(t *testing.T) {
	// Given: a versioned bucket whose only key has been delete-marked
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-empty")
	enableVersioning(t, srv, "ver-empty")
	putVersioned(t, srv, "ver-empty", "a.txt", []byte("hello"))
	dm := deleteVersion(t, srv, "ver-empty", "a.txt", "")
	dm.Body.Close()

	// When: the bucket is deleted
	resp, err := http.DefaultClient.Do(del(srv, "/ver-empty"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then: BucketNotEmpty
	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertXMLError(t, resp, "BucketNotEmpty")
}

// TestListObjectVersions_wireShape asserts on the raw XML rather than a decoded
// struct, because Go's decoder is forgiving where every real SDK is not: it
// ignores element order, tolerates a missing Owner, and would happily read a
// response that emitted all the Version elements before all the DeleteMarker
// ones. AWS interleaves them in one ordered run, and a client rebuilding the
// history from the two arrays it parses them into has nothing else to sequence
// on when two entries share a timestamp.
func TestListObjectVersions_wireShape(t *testing.T) {
	// Given: a key whose history is version, marker, version
	srv := helpers.NewTestServer(t)
	createBucket(t, srv, "ver-shape")
	enableVersioning(t, srv, "ver-shape")
	putVersioned(t, srv, "ver-shape", "a.txt", []byte("one"))
	dm := deleteVersion(t, srv, "ver-shape", "a.txt", "")
	dm.Body.Close()
	putVersioned(t, srv, "ver-shape", "a.txt", []byte("two"))

	// When: the raw response is read
	resp, err := http.DefaultClient.Do(get(srv, "/ver-shape?versions"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := helpers.ReadBody(t, resp)

	// Then: the envelope carries AWS's elements
	for _, element := range []string{
		"<ListVersionsResult", "<Name>ver-shape</Name>", "<KeyMarker></KeyMarker>",
		"<VersionIdMarker></VersionIdMarker>", "<MaxKeys>1000</MaxKeys>",
		"<IsTruncated>false</IsTruncated>", "<Owner>", "<IsLatest>",
	} {
		if !strings.Contains(body, element) {
			t.Errorf("response is missing %s:\n%s", element, body)
		}
	}

	// And: the three entries are interleaved in history order — newest first,
	// so the delete marker sits between the two versions.
	newest := strings.Index(body, "<Version>")
	marker := strings.Index(body, "<DeleteMarker>")
	oldest := strings.LastIndex(body, "<Version>")
	if !(newest >= 0 && marker > newest && oldest > marker) {
		t.Errorf("Version/DeleteMarker elements are not interleaved in history order:\n%s", body)
	}
}
