package lambda

// code_identity_test.go — pins the CodeHash invariant: every mutation path
// that changes a function's deployment package must go through setCode, and
// identity/CodeSha256 reads trust the stored hash instead of rehashing the
// package on the invoke path.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestSetCode_keepsDerivedFieldsInStep(t *testing.T) {
	// Given: a function and a deployment package.
	fn := &Function{Name: "fn"}
	zip := []byte("zip-bytes")

	// When: the package is set through the central setter.
	fn.setCode(zip)

	// Then: size and hash always match the package.
	if fn.CodeSize != int64(len(zip)) {
		t.Fatalf("CodeSize = %d, want %d", fn.CodeSize, len(zip))
	}
	if fn.CodeHash != sha256hex(zip) {
		t.Fatalf("CodeHash = %q, want sha256 of the package", fn.CodeHash)
	}
	// And: the CodeSha256 read for API responses is the stored digest in
	// AWS's wire encoding — base64, not hex. CDK compares this against the
	// base64 hash it computes locally, so the encoding decides whether a
	// no-op deploy sees code drift.
	sum := sha256.Sum256(zip)
	if got, want := codeSha256(fn), base64.StdEncoding.EncodeToString(sum[:]); got != want {
		t.Fatalf("codeSha256 = %q, want base64 digest %q", got, want)
	}
}

func TestFunctionCodeIdentity_storedHashAndLegacyFallbackAgree(t *testing.T) {
	// Given: the same package on a record written through setCode and on a
	// legacy record that predates the CodeHash field.
	zip := []byte("same-zip")
	current := &Function{Name: "fn"}
	current.setCode(zip)
	legacy := &Function{Name: "fn", CodeZip: zip}

	// Then: identities agree, so warm containers survive the upgrade instead
	// of being retired by a hash-scheme change.
	if functionInstanceIdentity(current) != functionInstanceIdentity(legacy) {
		t.Fatal("identity differs between setCode record and legacy record for identical code")
	}
}

func TestFunctionCodeIdentity_trustsStoredHash(t *testing.T) {
	// Given: a record whose CodeZip was mutated without going through setCode.
	fn := &Function{Name: "fn"}
	fn.setCode([]byte("original"))
	id := functionInstanceIdentity(fn)
	fn.CodeZip = []byte("mutated-behind-the-setters-back")

	// Then: identity does not move — this is the documented contract that
	// makes bare CodeZip assignments a bug (see the CodeZip field comment).
	if functionInstanceIdentity(fn) != id {
		t.Fatal("identity moved on a bare CodeZip assignment — identity no longer trusts CodeHash")
	}
}

func TestCreateFunction_storesCodeHash(t *testing.T) {
	// Given: a handler and an inline deployment package.
	clk := clock.NewMock()
	ls := newLambdaStore(state.NewMemoryStore(), "us-east-1", clk)
	h := &Handler{
		cfg: &config.Config{Region: "us-east-1"},
		log: serviceutil.NewServiceLogger(zap.NewNop(), "lambda"),
		clk: clk,
		ls:  ls,
	}
	zip := []byte("create-zip-bytes")
	body, _ := json.Marshal(map[string]any{
		"FunctionName": "hash-fn",
		"Runtime":      "nodejs22.x",
		"Handler":      "index.handler",
		"Role":         "arn:aws:iam::000000000000:role/lambda-role",
		"Code":         map[string]any{"ZipFile": zip},
	})
	req := httptest.NewRequest(http.MethodPost, "/2015-03-31/functions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// When: the function is created.
	h.CreateFunction(rec, req)

	// Then: the stored record carries the package hash.
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateFunction status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	fn, aerr := ls.getFunction(context.Background(), "hash-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if fn.CodeHash != sha256hex(zip) {
		t.Fatalf("CodeHash = %q, want sha256 of the uploaded package", fn.CodeHash)
	}
}

func TestUpdateFunctionCode_movesCodeHashAndIdentity(t *testing.T) {
	// Given: a stored function (legacy record — no CodeHash) and its identity.
	h, _ := lifecycleTestHandler(t)
	seedLifecycleFunction(t, h, nil)
	before, _ := h.ls.getFunction(context.Background(), "lifecycle-fn")
	beforeID := functionInstanceIdentity(before)

	// When: the code is replaced via UpdateFunctionCode.
	newZip := []byte("updated-zip-bytes")
	body, _ := json.Marshal(map[string]any{"ZipFile": newZip})
	req := httptest.NewRequest(http.MethodPut, "/2015-03-31/functions/lifecycle-fn/code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withFunctionNameParam(req, "lifecycle-fn")
	rec := httptest.NewRecorder()
	h.UpdateFunctionCode(rec, req)

	// Then: the stored hash matches the new package and the instance identity
	// moved, so warm containers running the old code get retired.
	if rec.Code != http.StatusOK {
		t.Fatalf("UpdateFunctionCode status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	after, aerr := h.ls.getFunction(context.Background(), "lifecycle-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if after.CodeHash != sha256hex(newZip) {
		t.Fatalf("CodeHash = %q, want sha256 of the new package", after.CodeHash)
	}
	if functionInstanceIdentity(after) == beforeID {
		t.Fatal("identity unchanged after code update — warm instances would serve stale code")
	}
}
