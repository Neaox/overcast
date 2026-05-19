package serviceutil_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// ---- DecodeJSON ------------------------------------------------------------

func TestDecodeJSON_success(t *testing.T) {
	// Given: a valid JSON request body
	body := `{"QueueName":"my-queue"}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	w := httptest.NewRecorder()

	// When: we decode it
	var dst struct {
		QueueName string `json:"QueueName"`
	}
	ok := serviceutil.DecodeJSON(w, req, &dst)

	// Then: decode succeeds and the value is populated
	if !ok {
		t.Error("expected DecodeJSON to return true for valid JSON")
	}
	if dst.QueueName != "my-queue" {
		t.Errorf("expected QueueName my-queue, got %q", dst.QueueName)
	}
}

func TestDecodeJSON_invalidJSON_writesError(t *testing.T) {
	// Given: an invalid JSON body
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("{not json"))
	w := httptest.NewRecorder()

	// When: we try to decode it
	var dst struct{ Name string }
	ok := serviceutil.DecodeJSON(w, req, &dst)

	// Then: returns false and writes a 400 error response
	if ok {
		t.Error("expected DecodeJSON to return false for invalid JSON")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var errResp struct {
		Type string `json:"__type"`
	}
	json.NewDecoder(w.Body).Decode(&errResp)
	if errResp.Type != "InvalidArgument" {
		t.Errorf("expected InvalidArgument error, got %q", errResp.Type)
	}
}

// ---- RequireString ---------------------------------------------------------

func TestRequireString_present(t *testing.T) {
	// Given: a non-empty string
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	// When + Then: returns true without writing a response
	if !serviceutil.RequireString(w, req, "my-value", "MyParam") {
		t.Error("expected true for non-empty string")
	}
	if w.Code != 200 {
		t.Errorf("expected no response written, got status %d", w.Code)
	}
}

func TestRequireString_empty_writesError(t *testing.T) {
	// Given: an empty string
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()

	// When + Then: returns false and writes MissingParameter error
	if serviceutil.RequireString(w, req, "", "QueueName") {
		t.Error("expected false for empty string")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---- QueryInt --------------------------------------------------------------

func TestQueryInt_present(t *testing.T) {
	// Given: a URL with the parameter set
	req := httptest.NewRequest(http.MethodGet, "/?max-keys=50", nil)

	// When + Then: returns the parsed value
	if got := serviceutil.QueryInt(req, "max-keys", 1000); got != 50 {
		t.Errorf("expected 50, got %d", got)
	}
}

func TestQueryInt_absent_returnsDefault(t *testing.T) {
	// Given: a URL without the parameter
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	// When + Then: returns the default
	if got := serviceutil.QueryInt(req, "max-keys", 1000); got != 1000 {
		t.Errorf("expected default 1000, got %d", got)
	}
}

func TestQueryInt_invalid_returnsDefault(t *testing.T) {
	// Given: a URL with a non-numeric parameter value
	req := httptest.NewRequest(http.MethodGet, "/?max-keys=notanumber", nil)

	// When + Then: returns the default (graceful degradation)
	if got := serviceutil.QueryInt(req, "max-keys", 1000); got != 1000 {
		t.Errorf("expected default 1000, got %d", got)
	}
}

// ---- HasQueryParam ---------------------------------------------------------

func TestHasQueryParam_present(t *testing.T) {
	// Given: a URL with the parameter (no value)
	req := httptest.NewRequest(http.MethodGet, "/bucket?location", nil)
	if !serviceutil.HasQueryParam(req, "location") {
		t.Error("expected location param to be present")
	}
}

func TestHasQueryParam_absent(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bucket", nil)
	if serviceutil.HasQueryParam(req, "location") {
		t.Error("expected location param to be absent")
	}
}

// ---- ClampInt --------------------------------------------------------------

func TestClampInt(t *testing.T) {
	cases := []struct{ v, min, max, want int }{
		{5, 1, 10, 5},   // within range
		{0, 1, 10, 1},   // below min
		{15, 1, 10, 10}, // above max
		{1, 1, 10, 1},   // at min
		{10, 1, 10, 10}, // at max
	}
	for _, tc := range cases {
		if got := serviceutil.ClampInt(tc.v, tc.min, tc.max); got != tc.want {
			t.Errorf("ClampInt(%d, %d, %d) = %d, want %d", tc.v, tc.min, tc.max, got, tc.want)
		}
	}
}

// ---- Pagination ------------------------------------------------------------

func TestPaginate_firstPage(t *testing.T) {
	// Given: 25 items, max 10 per page
	items := make([]int, 25)
	for i := range items {
		items[i] = i
	}

	// When: we request the first page
	page := serviceutil.Paginate(items, 10, "")

	// Then: we get the first 10 items and a continuation token
	if len(page.Items) != 10 {
		t.Errorf("expected 10 items, got %d", len(page.Items))
	}
	if !page.IsTruncated {
		t.Error("expected IsTruncated to be true")
	}
	if page.NextToken == "" {
		t.Error("expected NextToken to be set")
	}
}

func TestPaginate_lastPage(t *testing.T) {
	// Given: 15 items, max 10, fetching page 2
	items := make([]int, 15)
	firstPage := serviceutil.Paginate(items, 10, "")

	// When: we request the second page using the continuation token
	secondPage := serviceutil.Paginate(items, 10, firstPage.NextToken)

	// Then: we get the remaining 5 items with no continuation token
	if len(secondPage.Items) != 5 {
		t.Errorf("expected 5 items on last page, got %d", len(secondPage.Items))
	}
	if secondPage.IsTruncated {
		t.Error("expected IsTruncated to be false on last page")
	}
	if secondPage.NextToken != "" {
		t.Error("expected no NextToken on last page")
	}
}

func TestPaginate_allItemsFitOnOnePage(t *testing.T) {
	// Given: 5 items, max 100
	items := []string{"a", "b", "c", "d", "e"}

	// When + Then: single page with no truncation
	page := serviceutil.Paginate(items, 100, "")
	if len(page.Items) != 5 {
		t.Errorf("expected 5 items, got %d", len(page.Items))
	}
	if page.IsTruncated {
		t.Error("expected IsTruncated false for single page")
	}
}

func TestPaginate_invalidToken_treatsAsFirstPage(t *testing.T) {
	// Given: an invalid/corrupt continuation token
	items := []int{1, 2, 3}
	page := serviceutil.Paginate(items, 10, "!!not-valid-base64!!")

	// Then: graceful degradation — returns from the beginning
	if len(page.Items) != 3 {
		t.Errorf("expected all 3 items for invalid token, got %d", len(page.Items))
	}
}

// ---- BucketName validation -------------------------------------------------

func TestBucketName_valid(t *testing.T) {
	valid := []string{"my-bucket", "bucket123", "a-b-c", "abc"}
	for _, name := range valid {
		if err := serviceutil.BucketName(name); err != nil {
			t.Errorf("expected %q to be valid, got error: %v", name, err)
		}
	}
}

func TestBucketName_invalid(t *testing.T) {
	invalid := []string{
		"ab",                 // too short
		"A-BUCKET",           // uppercase
		"my--bucket",         // consecutive hyphens
		"192.168.1.1",        // IP address format
		"-bucket",            // starts with hyphen
		"bucket-",            // ends with hyphen
		"bucket with spaces", // spaces
	}
	for _, name := range invalid {
		if err := serviceutil.BucketName(name); err == nil {
			t.Errorf("expected %q to be invalid, but got no error", name)
		}
	}
}

// ---- LazyInit --------------------------------------------------------------

func TestLazyInit_runsOnce(t *testing.T) {
	// Given: a LazyInit and a counter
	var li serviceutil.LazyInit
	callCount := 0

	// When: Do is called multiple times concurrently
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			li.Do(func() error {
				callCount++
				return nil
			})
		}()
	}
	wg.Wait()

	// Then: the function ran exactly once
	if callCount != 1 {
		t.Errorf("expected init function to run exactly once, ran %d times", callCount)
	}
	if !li.Done() {
		t.Error("expected Done() to return true after successful init")
	}
}

func TestLazyInit_retriesOnError(t *testing.T) {
	// Given: an init function that fails the first time
	var li serviceutil.LazyInit
	attempts := 0

	// When: first call fails
	err1 := li.Do(func() error {
		attempts++
		return http.ErrServerClosed // any error
	})
	// Then: error is returned
	if err1 == nil {
		t.Error("expected error on first attempt")
	}

	// When: second call succeeds
	err2 := li.Do(func() error {
		attempts++
		return nil
	})

	// Then: no error, and it ran twice (retried after failure)
	if err2 != nil {
		t.Errorf("expected success on second attempt, got: %v", err2)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestLazyInit_reset(t *testing.T) {
	// Given: a successfully initialised LazyInit
	var li serviceutil.LazyInit
	li.Do(func() error { return nil })

	// When: we reset it
	li.Reset()

	// Then: Done returns false and the next Do runs the function again
	if li.Done() {
		t.Error("expected Done() to be false after Reset()")
	}

	ran := false
	li.Do(func() error {
		ran = true
		return nil
	})
	if !ran {
		t.Error("expected init function to run again after Reset()")
	}
}

// ---- HeaderPrefix ----------------------------------------------------------

func TestHeaderPrefix_extractsAndLowercases(t *testing.T) {
	// Given: a request with x-amz-meta-* headers
	req := httptest.NewRequest(http.MethodPut, "/", nil)
	req.Header.Set("X-Amz-Meta-Author", "alice")
	req.Header.Set("X-Amz-Meta-Version", "2.0")
	req.Header.Set("Content-Type", "text/plain") // should be excluded

	// When: we extract the meta headers
	meta := serviceutil.HeaderPrefix(req, "X-Amz-Meta-")

	// Then: only the meta headers are returned, keys lowercased
	if meta["author"] != "alice" {
		t.Errorf("expected author=alice, got %q", meta["author"])
	}
	if meta["version"] != "2.0" {
		t.Errorf("expected version=2.0, got %q", meta["version"])
	}
	if _, ok := meta["content-type"]; ok {
		t.Error("content-type should not be included in meta")
	}
}

// ---- QueueName -------------------------------------------------------------

func TestQueueName_valid(t *testing.T) {
	cases := []string{"my-queue", "my_queue", "MyQueue123", "q"}
	for _, name := range cases {
		if aerr := serviceutil.QueueName(name); aerr != nil {
			t.Errorf("QueueName(%q): unexpected error: %v", name, aerr)
		}
	}
}

func TestQueueName_tooLong(t *testing.T) {
	name := string(make([]byte, 81)) // 81 chars
	for i := range name {
		name = name[:i] + "a" + name[i+1:]
	}
	if aerr := serviceutil.QueueName(name); aerr == nil {
		t.Error("expected error for name >80 chars")
	}
}

func TestQueueName_empty(t *testing.T) {
	if aerr := serviceutil.QueueName(""); aerr == nil {
		t.Error("expected error for empty queue name")
	}
}

func TestQueueName_invalidChars(t *testing.T) {
	if aerr := serviceutil.QueueName("bad!name"); aerr == nil {
		t.Error("expected error for queue name with invalid chars")
	}
}

// ---- TableName -------------------------------------------------------------

func TestTableName_valid(t *testing.T) {
	cases := []string{"Users", "my-table", "my_table.v2"}
	for _, name := range cases {
		if aerr := serviceutil.TableName(name); aerr != nil {
			t.Errorf("TableName(%q): unexpected error: %v", name, aerr)
		}
	}
}

func TestTableName_tooShort(t *testing.T) {
	if aerr := serviceutil.TableName("ab"); aerr == nil {
		t.Error("expected error for table name < 3 chars")
	}
}

func TestTableName_invalidChars(t *testing.T) {
	if aerr := serviceutil.TableName("bad!table"); aerr == nil {
		t.Error("expected error for table name with invalid chars")
	}
}

// ---- ValidateAndRespond ----------------------------------------------------

func TestValidateAndRespond_noError_returnsTrue(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	got := serviceutil.ValidateAndRespond(w, req, nil)
	if !got {
		t.Error("expected true when aerr is nil")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected no response written (200), got %d", w.Code)
	}
}

func TestValidateAndRespond_withError_writeErrorAndReturnsFalse(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	aerr := &protocol.AWSError{
		Code:       "InvalidParameterValue",
		Message:    "bad input",
		HTTPStatus: http.StatusBadRequest,
	}
	got := serviceutil.ValidateAndRespond(w, req, aerr)
	if got {
		t.Error("expected false when aerr is non-nil")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ---- ServiceLogger ---------------------------------------------------------

func TestServiceLogger_allLevels(t *testing.T) {
	// Use a no-op logger — we just verify calling these methods doesn't panic.
	logger := zap.NewNop()
	slog := serviceutil.NewServiceLogger(logger, "test-service")

	slog.Debug("debug message", zap.String("key", "val"))
	slog.Info("info message")
	slog.Warn("warn message")
	slog.Error("error message", zap.Error(errors.New("something broke")))
}

func TestServiceLogger_With(t *testing.T) {
	logger := zap.NewNop()
	slog := serviceutil.NewServiceLogger(logger, "test-service")

	child := slog.With(zap.String("op", "get-object"), zap.String("bucket", "my-bucket"))
	if child == nil {
		t.Error("With returned nil")
	}
	// Should not panic.
	child.Debug("child log")
}

func TestServiceLogger_Logger(t *testing.T) {
	logger := zap.NewNop()
	slog := serviceutil.NewServiceLogger(logger, "svc")

	if got := slog.Logger(); got == nil {
		t.Error("Logger() returned nil")
	}
}

func TestServiceLogger_LogStateError(t *testing.T) {
	logger := zap.NewNop()
	slog := serviceutil.NewServiceLogger(logger, "test-service")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	aerr := &protocol.AWSError{
		Code:       "InternalError",
		Message:    "store failed",
		HTTPStatus: http.StatusInternalServerError,
	}
	// Should not panic.
	slog.LogStateError(req, "get-object", aerr, zap.String("bucket", "test"))
}
