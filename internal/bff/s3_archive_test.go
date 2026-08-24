package bff

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// archiveForm is the body the console posts: one "key" field per ticked row,
// plus the folder those rows were listed under.
func archiveForm(prefix string, keys ...string) *strings.Reader {
	form := url.Values{}
	if prefix != "" {
		form.Set("prefix", prefix)
	}
	for _, k := range keys {
		form.Add("key", k)
	}
	return strings.NewReader(form.Encode())
}

func archiveRequest(target string, body *strings.Reader) *http.Request {
	req := httptest.NewRequest(http.MethodPost, target, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// objectStub serves each key of `objects` as its own object body and 404s
// anything else, standing in for the emulator's GetObject.
func objectStub(objects map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := objects[strings.TrimPrefix(r.URL.Path, "/demo/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.Write([]byte(body))
	}
}

// readArchive reads a zip out of a recorded response, returning entry name →
// content in the order the entries were written.
func readArchive(t *testing.T, raw []byte) (map[string]string, []string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("response is not a readable zip: %v", err)
	}
	contents := make(map[string]string, len(zr.File))
	order := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("opening %q: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %q: %v", f.Name, err)
		}
		contents[f.Name] = string(b)
		order = append(order, f.Name)
	}
	return contents, order
}

func TestHandleS3Archive_zipsTheSelectedObjects(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(map[string]string{
		"logs/2026/a.txt": "first",
		"logs/2026/b.txt": "second",
		"logs/2026/c.txt": "not selected",
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("logs/2026/", "logs/2026/a.txt", "logs/2026/b.txt")))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %q", got)
	}

	contents, order := readArchive(t, rec.Body.Bytes())
	// Names are relative to the folder the selection was made in: a download
	// from logs/2026/ unpacks as a.txt, not as logs/2026/a.txt.
	want := map[string]string{"a.txt": "first", "b.txt": "second"}
	if len(contents) != len(want) {
		t.Fatalf("expected %d entries, got %v", len(want), order)
	}
	for name, body := range want {
		if contents[name] != body {
			t.Errorf("entry %q: expected %q, got %q", name, body, contents[name])
		}
	}
}

func TestHandleS3Archive_keepsPathsBeneathTheBrowsedPrefix(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(map[string]string{
		"logs/2026/01/a.txt": "nested",
	}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("logs/", "logs/2026/01/a.txt")))

	contents, order := readArchive(t, rec.Body.Bytes())
	if contents["2026/01/a.txt"] != "nested" {
		t.Errorf("expected 2026/01/a.txt to carry the object, got entries %v", order)
	}
}

func TestHandleS3Archive_namesTheFileAfterTheBucketAndFolder(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(map[string]string{"logs/a.txt": "x"}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("", "logs/a.txt")))
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="demo.zip"` {
		t.Errorf("bucket root: unexpected Content-Disposition %q", got)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("logs/2026/", "logs/a.txt")))
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="demo-logs-2026.zip"` {
		t.Errorf("folder: unexpected Content-Disposition %q", got)
	}
}

// An object that has gone missing between the listing and the download must
// not cost the user the rest of the archive — the download has already begun
// and its status code is spent, so the failure rides along inside the zip.
func TestHandleS3Archive_recordsPerObjectFailuresInsideTheArchive(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(map[string]string{"a.txt": "kept"}))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("", "a.txt", "vanished.txt")))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	contents, order := readArchive(t, rec.Body.Bytes())
	if contents["a.txt"] != "kept" {
		t.Errorf("the readable object should still be in the archive, got %v", order)
	}
	note, ok := contents[archiveErrorsEntry]
	if !ok {
		t.Fatalf("expected a %s entry, got %v", archiveErrorsEntry, order)
	}
	if !strings.Contains(note, "vanished.txt") {
		t.Errorf("expected the note to name the failed key, got %q", note)
	}
}

func TestHandleS3Archive_rejectsAnEmptySelection(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(nil))
	defer restore()

	handler := NewHandler(nil, nil, UIConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive", archiveForm("")))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleS3Archive_rejectsMoreKeysThanItWillArchive(t *testing.T) {
	_, restore := stubEmulatorForProxyTests(t, objectStub(nil))
	defer restore()

	keys := make([]string, maxArchiveKeys+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}

	handler := NewHandler(nil, nil, UIConfig{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest("/api/s3/buckets/demo/objects/archive",
		archiveForm("", keys...)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	// Named exactly, not just "an error mentioning 5000": the parse failure
	// the runtime raises past its own parameter ceiling quotes the same number,
	// and this has to be the count check that answered.
	if !strings.Contains(rec.Body.String(), fmt.Sprintf("too many objects: %d", maxArchiveKeys+1)) {
		t.Errorf("expected the count check to answer, got %s", rec.Body.String())
	}
}

// A form POST cannot carry the x-overcast-endpoint header, which is why the
// action URL carries the endpoint as a query parameter instead.
//
// A remote endpoint is what makes the difference observable: normalizeEndpoint
// deliberately rewrites every loopback endpoint back to the local API, so a
// header-only handler and a query-aware one dial the same place in every other
// test here. The upstream request is caught at the transport rather than by a
// server, because a remote host is exactly what must not be dialed for real.
func TestHandleS3Archive_resolvesTheEndpointFromTheQuery(t *testing.T) {
	// The handler is built first: NewHandler configures the proxy clients'
	// transports, which would drop the stub installed before it.
	handler := NewHandler(nil, nil, UIConfig{})

	var dialed string
	origClient := bffHTTPClient
	bffHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		dialed = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("x")),
		}, nil
	})}
	defer func() { bffHTTPClient = origClient }()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, archiveRequest(
		"/api/s3/buckets/demo/objects/archive?ep="+url.QueryEscape("http://emulator.example:4566"),
		archiveForm("", "a.txt")))

	if dialed != "http://emulator.example:4566/demo/a.txt" {
		t.Errorf("expected the endpoint from the query to be dialed, got %q", dialed)
	}
	if contents, order := readArchive(t, rec.Body.Bytes()); contents["a.txt"] != "x" {
		t.Errorf("expected the object in the archive, got entries %v", order)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Zip entry names are paths an extractor creates, and an S3 key may contain
// "..": a key that climbs out of the archive root has to be defused here.
func TestArchiveEntryName(t *testing.T) {
	tests := []struct {
		key, prefix, want string
	}{
		{"logs/2026/a.txt", "logs/2026/", "a.txt"},
		{"logs/2026/a.txt", "logs/", "2026/a.txt"},
		{"a.txt", "", "a.txt"},
		// The prefix is what the browser was listing; a key from elsewhere
		// keeps its whole path rather than being silently re-rooted.
		{"other/a.txt", "logs/", "other/a.txt"},
		{"logs/../../etc/passwd", "logs/", "etc/passwd"},
		{"../escape.txt", "", "escape.txt"},
		{"a/./b.txt", "", "a/b.txt"},
		{"/leading.txt", "", "leading.txt"},
		// Nothing left of the key once the prefix is off: the folder marker
		// object S3 tools write for an empty folder.
		{"logs/", "logs/", "logs"},
	}
	for _, tc := range tests {
		if got := archiveEntryName(tc.key, tc.prefix); got != tc.want {
			t.Errorf("archiveEntryName(%q, %q) = %q, want %q", tc.key, tc.prefix, got, tc.want)
		}
	}
}

func TestArchiveFilename(t *testing.T) {
	tests := []struct {
		bucket, prefix, want string
	}{
		{"demo", "", "demo.zip"},
		{"demo", "logs/", "demo-logs.zip"},
		{"demo", "logs/2026/01/", "demo-logs-2026-01.zip"},
		// A prefix is a key fragment, so it can hold anything a key can. Only
		// what survives as a filename is kept.
		{"demo", `we"ird/pa th/`, "demo-we-ird-pa-th.zip"},
		{"demo", "///", "demo.zip"},
		// The bucket goes through the same reduction as the prefix: it too
		// reaches a response header, straight off the URL.
		{`we"ird`, "", "we-ird.zip"},
		{"/", "", "objects.zip"},
	}
	for _, tc := range tests {
		if got := archiveFilename(tc.bucket, tc.prefix); got != tc.want {
			t.Errorf("archiveFilename(%q, %q) = %q, want %q", tc.bucket, tc.prefix, got, tc.want)
		}
	}
}

// The archive is written to the wire as it is built, not assembled in memory
// and sent at the end: bytes for the objects already read have to reach the
// client while a later object is still being fetched. That is what keeps a
// multi-gigabyte selection from costing multi-gigabyte memory here.
func TestHandleS3Archive_streamsWhileLaterObjectsAreStillBeingRead(t *testing.T) {
	release := make(chan struct{})
	reached := make(chan struct{})

	emulator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "slow.txt") {
			close(reached)
			<-release
		}
		w.Write([]byte("body"))
	}))
	defer emulator.Close()

	origAPIURL, origClient := defaultAPIURL, bffHTTPClient
	defaultAPIURL, bffHTTPClient = emulator.URL, emulator.Client()
	defer func() { defaultAPIURL, bffHTTPClient = origAPIURL, origClient }()

	// A real server, not a recorder: a recorder collects the whole body before
	// the handler returns, which is exactly the property under test.
	bff := httptest.NewServer(NewHandler(nil, nil, UIConfig{}))
	defer bff.Close()

	resp, err := http.Post(bff.URL+"/api/s3/buckets/demo/objects/archive",
		"application/x-www-form-urlencoded", archiveForm("", "quick.txt", "slow.txt"))
	if err != nil {
		t.Fatalf("posting: %v", err)
	}
	defer resp.Body.Close()

	// Chunked, with no Content-Length — the length of an archive that has not
	// been built yet is not known, and claiming one would mean buffering it.
	if resp.ContentLength != -1 {
		t.Errorf("expected no Content-Length, got %d", resp.ContentLength)
	}

	<-reached // the second object is now blocked mid-fetch

	first := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(resp.Body, make([]byte, 1))
		first <- err
	}()

	select {
	case err := <-first:
		if err != nil {
			t.Fatalf("reading the first byte: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("no archive bytes reached the client while a later object was still being read")
	}

	close(release)
	io.Copy(io.Discard, resp.Body)
}
