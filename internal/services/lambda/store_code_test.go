package lambda

// store_code_test.go — pins the deployment-package split: function records
// persist without their zip (invoke-path reads decode no package bytes), the
// package round-trips through its own store key, and configuration updates on
// a record whose code was never loaded do not disturb the stored package.

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/state"
)

func codeSplitStore(t *testing.T) (*lambdaStore, state.Store) {
	t.Helper()
	inner := state.NewMemoryStore()
	return newLambdaStore(inner, "us-east-1", clock.New()), inner
}

func seedCodeSplitFunction(t *testing.T, ls *lambdaStore, zip []byte) *Function {
	t.Helper()
	fn := &Function{
		Name: "split-fn",
		ARN:  "arn:aws:lambda:us-east-1:000000000000:function:split-fn",
	}
	fn.setCode(zip)
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}
	return fn
}

func TestPutFunction_recordCarriesNoPackageBytes(t *testing.T) {
	// Given: a function stored with a deployment package.
	ls, inner := codeSplitStore(t)
	zip := []byte("split-zip-bytes")
	seedCodeSplitFunction(t, ls, zip)

	// Then: the persisted record does not embed the package…
	raw, found, err := inner.Get(context.Background(), nsFunctions, "us-east-1/split-fn")
	if err != nil || !found {
		t.Fatalf("record Get: found=%v err=%v", found, err)
	}
	if strings.Contains(raw, "code_zip") {
		t.Fatalf("function record still embeds code_zip — every invoke-path read decodes the package again: %s", raw)
	}
	// …and a read returns a byte-free record with hash and size intact.
	fn, aerr := ls.getFunction(context.Background(), "split-fn")
	if aerr != nil {
		t.Fatalf("getFunction: %v", aerr)
	}
	if len(fn.CodeZip) != 0 {
		t.Fatalf("getFunction returned %d package bytes, want none on the invoke path", len(fn.CodeZip))
	}
	if fn.CodeHash != sha256hex(zip) || fn.CodeSize != int64(len(zip)) {
		t.Fatalf("CodeHash/CodeSize = %q/%d, want hash of package / %d", fn.CodeHash, fn.CodeSize, len(zip))
	}

	// And: loading materializes the exact package.
	if aerr := ls.loadFunctionCode(context.Background(), fn); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if string(fn.CodeZip) != string(zip) {
		t.Fatalf("loaded package = %q, want %q", fn.CodeZip, zip)
	}
}

func TestPutFunction_configUpdateKeepsStoredPackage(t *testing.T) {
	// Given: a stored function whose record was re-read without its package.
	ls, _ := codeSplitStore(t)
	zip := []byte("keep-these-bytes")
	seedCodeSplitFunction(t, ls, zip)
	fn, _ := ls.getFunction(context.Background(), "split-fn")

	// When: a configuration-only update is persisted (CodeZip empty).
	fn.Timeout = 30
	if aerr := ls.putFunction(context.Background(), fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}

	// Then: the stored package is untouched.
	reread, _ := ls.getFunction(context.Background(), "split-fn")
	if aerr := ls.loadFunctionCode(context.Background(), reread); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}
	if string(reread.CodeZip) != string(zip) {
		t.Fatalf("package after config update = %q, want %q", reread.CodeZip, zip)
	}
	if reread.Timeout != 30 {
		t.Fatalf("Timeout = %d, want 30", reread.Timeout)
	}
}

func TestDeleteFunction_removesStoredPackage(t *testing.T) {
	// Given: a stored function with a package.
	ls, inner := codeSplitStore(t)
	seedCodeSplitFunction(t, ls, []byte("doomed-bytes"))

	// When: the function is deleted.
	if aerr := ls.deleteFunction(context.Background(), "split-fn"); aerr != nil {
		t.Fatalf("deleteFunction: %v", aerr)
	}

	// Then: the package key is gone with it.
	_, found, err := inner.Get(context.Background(), nsFunctionCode, "us-east-1/split-fn")
	if err != nil {
		t.Fatalf("package Get: %v", err)
	}
	if found {
		t.Fatal("stored package survived DeleteFunction")
	}
}

func TestPutLayerVersion_recordCarriesNoContentBytes(t *testing.T) {
	// Given: a published layer version.
	ls, inner := codeSplitStore(t)
	zip := []byte("layer-zip-bytes")
	lv := &LayerVersion{
		LayerName:       "split-layer",
		LayerARN:        "arn:aws:lambda:us-east-1:000000000000:layer:split-layer",
		LayerVersionARN: "arn:aws:lambda:us-east-1:000000000000:layer:split-layer:1",
		Version:         1,
	}
	lv.setContent(zip)
	if aerr := ls.putLayerVersion(context.Background(), lv); aerr != nil {
		t.Fatalf("putLayerVersion: %v", aerr)
	}

	// Then: the record embeds no content but keeps size and hash…
	raw, found, err := inner.Get(context.Background(), nsLayers, "us-east-1/"+layerVersionKey("split-layer", 1))
	if err != nil || !found {
		t.Fatalf("record Get: found=%v err=%v", found, err)
	}
	if strings.Contains(raw, "\"content\"") {
		t.Fatalf("layer record still embeds content — every list decodes the archive again: %s", raw)
	}
	got, aerr := ls.getLayerVersion(context.Background(), "split-layer", 1)
	if aerr != nil || got == nil {
		t.Fatalf("getLayerVersion: %v", aerr)
	}
	if len(got.Content) != 0 || got.CodeSize != int64(len(zip)) || got.CodeHash != sha256hex(zip) {
		t.Fatalf("record = %d content bytes, size %d, hash %q — want byte-free with size/hash intact", len(got.Content), got.CodeSize, got.CodeHash)
	}

	// And: loading materializes the archive; deleting removes it.
	if aerr := ls.loadLayerContent(context.Background(), got); aerr != nil {
		t.Fatalf("loadLayerContent: %v", aerr)
	}
	if string(got.Content) != string(zip) {
		t.Fatalf("loaded content = %q, want %q", got.Content, zip)
	}
	if aerr := ls.deleteLayerVersion(context.Background(), "split-layer", 1); aerr != nil {
		t.Fatalf("deleteLayerVersion: %v", aerr)
	}
	if _, found, _ := inner.Get(context.Background(), nsLayerContent, "us-east-1/"+layerVersionKey("split-layer", 1)); found {
		t.Fatal("stored layer content survived DeleteLayerVersion")
	}
}

func TestLoadLayerContent_legacyEmbeddedRecordIsLeftAsIs(t *testing.T) {
	// Given: a record persisted before the split, with the zip embedded (layer
	// versions are immutable, so such records are never rewritten).
	ls, inner := codeSplitStore(t)
	legacy := `{"layer_name":"old-layer","layer_version_arn":"arn:aws:lambda:us-east-1:000000000000:layer:old-layer:1","version":1,"content":"` +
		base64.StdEncoding.EncodeToString([]byte("legacy-zip")) + `","code_size":10}`
	if err := inner.Set(context.Background(), nsLayers, "us-east-1/"+layerVersionKey("old-layer", 1), legacy); err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}

	// When: the record is read and content loaded.
	lv, aerr := ls.getLayerVersion(context.Background(), "old-layer", 1)
	if aerr != nil || lv == nil {
		t.Fatalf("getLayerVersion: %v", aerr)
	}
	if aerr := ls.loadLayerContent(context.Background(), lv); aerr != nil {
		t.Fatalf("loadLayerContent: %v", aerr)
	}

	// Then: the embedded bytes are used untouched.
	if string(lv.Content) != "legacy-zip" {
		t.Fatalf("legacy content = %q, want the embedded bytes", lv.Content)
	}
}

func TestLoadFunctionCode_resolvesRegionFromARN(t *testing.T) {
	// Given: a function written under its request region, on a store whose
	// default region differs.
	inner := state.NewMemoryStore()
	ls := newLambdaStore(inner, "eu-west-1", clock.New())
	fn := &Function{
		Name: "regional-fn",
		ARN:  "arn:aws:lambda:us-east-1:000000000000:function:regional-fn",
	}
	fn.setCode([]byte("regional-bytes"))
	ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
	if aerr := ls.putFunction(ctx, fn); aerr != nil {
		t.Fatalf("putFunction: %v", aerr)
	}

	// When: the package is loaded on a bare context, as provisioned-concurrency
	// replenishment and ESM delivery do.
	loaded := &Function{Name: "regional-fn", ARN: fn.ARN}
	if aerr := ls.loadFunctionCode(context.Background(), loaded); aerr != nil {
		t.Fatalf("loadFunctionCode: %v", aerr)
	}

	// Then: the ARN's region wins over the store default.
	if string(loaded.CodeZip) != "regional-bytes" {
		t.Fatalf("loaded package = %q, want the region-partitioned bytes", loaded.CodeZip)
	}
}
