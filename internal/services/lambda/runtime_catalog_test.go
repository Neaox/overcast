package lambda

import (
	"strings"
	"testing"
	"time"
)

// pinnedModelRuntimeEnum is com.amazonaws.lambda#Runtime from the pinned AWS
// Smithy model (models/aws/VERSION, revision 66e973ca, model date 2026-07-27),
// in declaration order. It is transcribed here rather than derived from the
// catalog so that this file can prove the catalog matches the model: if the two
// were built from the same variable the test would prove nothing.
var pinnedModelRuntimeEnum = []string{
	"nodejs", "nodejs4.3", "nodejs6.10", "nodejs8.10", "nodejs10.x", "nodejs12.x",
	"nodejs14.x", "nodejs16.x", "nodejs18.x", "nodejs20.x", "nodejs22.x", "nodejs24.x",
	"java8", "java8.al2", "java11", "java17", "java21", "java25",
	"python2.7", "python3.6", "python3.7", "python3.8", "python3.9", "python3.10",
	"python3.11", "python3.12", "python3.13", "python3.14",
	"dotnetcore1.0", "dotnetcore2.0", "dotnetcore2.1", "dotnetcore3.1",
	"dotnet6", "dotnet8", "dotnet10",
	"nodejs4.3-edge", "go1.x",
	"ruby2.5", "ruby2.7", "ruby3.2", "ruby3.3", "ruby3.4", "ruby4.0",
	"provided", "provided.al2", "provided.al2023",
	"java8.al2023", "java11.al2023", "java17.al2023",
}

// The catalog is the single source of truth for the modeled runtime set, the
// runtimes Overcast can execute, the AWS lifecycle dates, and the list the web
// UI renders. These tests are the guard that keeps those four views identical:
// a runtime added to one and forgotten in another fails here.

func TestRuntimeCatalog_matchesPinnedModelEnum(t *testing.T) {
	got := make([]string, 0, len(lambdaRuntimeCatalog))
	for _, spec := range lambdaRuntimeCatalog {
		got = append(got, spec.ID)
	}
	if len(got) != len(pinnedModelRuntimeEnum) {
		t.Fatalf("catalog has %d runtimes, pinned model enum has %d", len(got), len(pinnedModelRuntimeEnum))
	}
	for i := range got {
		if got[i] != pinnedModelRuntimeEnum[i] {
			t.Fatalf("catalog[%d] = %q, pinned model enum[%d] = %q (order must match so the enum-validation message is AWS-shaped)",
				i, got[i], i, pinnedModelRuntimeEnum[i])
		}
	}
}

func TestRuntimeCatalog_validationValuesAreDerivedFromCatalog(t *testing.T) {
	if len(lambdaRuntimeValues) != len(lambdaRuntimeCatalog) {
		t.Fatalf("lambdaRuntimeValues has %d entries, catalog has %d", len(lambdaRuntimeValues), len(lambdaRuntimeCatalog))
	}
	for i, spec := range lambdaRuntimeCatalog {
		if lambdaRuntimeValues[i] != spec.ID {
			t.Fatalf("lambdaRuntimeValues[%d] = %q, catalog[%d] = %q", i, lambdaRuntimeValues[i], i, spec.ID)
		}
		if _, ok := lambdaRuntimeValueSet[spec.ID]; !ok {
			t.Fatalf("%s missing from lambdaRuntimeValueSet", spec.ID)
		}
	}
}

func TestRuntimeCatalog_executionImagesMatchActiveRuntimes(t *testing.T) {
	withImage := map[string]string{}
	for _, spec := range lambdaRuntimeCatalog {
		if spec.Image != "" {
			withImage[spec.ID] = spec.Image
		}
	}
	if len(withImage) != len(activeRuntimes) {
		t.Fatalf("catalog maps %d runtimes to images, activeRuntimes has %d", len(withImage), len(activeRuntimes))
	}
	for id, image := range withImage {
		if activeRuntimes[id] != image {
			t.Fatalf("activeRuntimes[%q] = %q, catalog image = %q", id, activeRuntimes[id], image)
		}
		if !lambdaRuntimeExecutionSupported(id) {
			t.Fatalf("%s has an execution image but lambdaRuntimeExecutionSupported reports false", id)
		}
	}
	for _, spec := range lambdaRuntimeCatalog {
		if spec.Image == "" && lambdaRuntimeExecutionSupported(spec.ID) {
			t.Fatalf("%s has no execution image but is reported executable", spec.ID)
		}
	}
}

func TestRuntimeCatalog_imagesAreOfficialLambdaBaseImages(t *testing.T) {
	// Every mapping must be an official AWS Lambda base image. The repository
	// is derived from the runtime family, so a typo in either half shows up
	// here rather than as a pull failure at the first invoke.
	repoForFamily := map[string]string{
		"Node.js":        "nodejs",
		"Python":         "python",
		"Java":           "java",
		".NET":           "dotnet",
		"Ruby":           "ruby",
		"Custom runtime": "provided",
	}
	for _, spec := range lambdaRuntimeCatalog {
		if spec.Image == "" {
			continue
		}
		const prefix = "public.ecr.aws/lambda/"
		if !strings.HasPrefix(spec.Image, prefix) {
			t.Errorf("%s image %q is not an official Lambda base image", spec.ID, spec.Image)
			continue
		}
		repoTag := strings.TrimPrefix(spec.Image, prefix)
		repo, tag, ok := strings.Cut(repoTag, ":")
		if !ok || tag == "" {
			t.Errorf("%s image %q has no tag", spec.ID, spec.Image)
			continue
		}
		if want := repoForFamily[spec.Family]; repo != want {
			t.Errorf("%s (family %q) uses repository %q, want %q", spec.ID, spec.Family, repo, want)
		}
	}
}

func TestRuntimeCatalog_metadataIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range lambdaRuntimeCatalog {
		if seen[spec.ID] {
			t.Fatalf("duplicate catalog entry %q", spec.ID)
		}
		seen[spec.ID] = true
		if spec.Name == "" || spec.Family == "" || spec.DefaultHandler == "" {
			t.Errorf("%s: incomplete display metadata name=%q family=%q handler=%q", spec.ID, spec.Name, spec.Family, spec.DefaultHandler)
		}
		lc, ok := runtimeLifecycles[spec.ID]
		if !ok {
			// Runtimes AWS has modeled but not yet given a lifecycle entry are
			// legitimate; they simply never block.
			continue
		}
		if !lc.blockCreate.IsZero() && !lc.blockUpdate.IsZero() && lc.blockUpdate.Before(lc.blockCreate) {
			t.Errorf("%s: block-update %s precedes block-create %s", spec.ID, lc.blockUpdate, lc.blockCreate)
		}
	}
}

func TestRuntimeLifecycle_phasesFollowAWSDates(t *testing.T) {
	// AWS deprecates a runtime in three phases: end of support (deprecation
	// date), then block function create, then block function update. Between
	// the first two phases AWS still accepts CreateFunction, which is why a
	// single "deprecated" boolean is not enough.
	day := func(s string) time.Time {
		ts, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return ts
	}
	tests := []struct {
		name              string
		runtime           string
		now               string
		wantDeprecated    bool
		wantCreateBlocked bool
		wantUpdateBlocked bool
	}{
		// python3.9: deprecated 2025-12-15, create blocked 2027-02-01,
		// update blocked 2027-03-03.
		{"python3.9 before deprecation", "python3.9", "2025-01-01", false, false, false},
		{"python3.9 deprecated but creatable", "python3.9", "2026-08-08", true, false, false},
		{"python3.9 create blocked", "python3.9", "2027-02-01", true, true, false},
		{"python3.9 update blocked", "python3.9", "2027-03-03", true, true, true},
		// nodejs14.x is long past every phase.
		{"nodejs14.x fully blocked", "nodejs14.x", "2027-03-03", true, true, true},
		// python3.14 is current: no phase has been reached.
		{"python3.14 current", "python3.14", "2026-08-08", false, false, false},
		// java17.al2023 has no published lifecycle at the pinned model date.
		{"java17.al2023 no lifecycle", "java17.al2023", "2030-01-01", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := day(tc.now)
			if got := runtimeDeprecated(tc.runtime, now); got != tc.wantDeprecated {
				t.Errorf("runtimeDeprecated = %v, want %v", got, tc.wantDeprecated)
			}
			if got := runtimeCreateBlocked(tc.runtime, now); got != tc.wantCreateBlocked {
				t.Errorf("runtimeCreateBlocked = %v, want %v", got, tc.wantCreateBlocked)
			}
			if got := runtimeUpdateBlocked(tc.runtime, now); got != tc.wantUpdateBlocked {
				t.Errorf("runtimeUpdateBlocked = %v, want %v", got, tc.wantUpdateBlocked)
			}
		})
	}
}

func TestRuntimeLifecycle_blockedErrorMatchesAWS(t *testing.T) {
	aerr := lambdaDeprecatedRuntimeError("nodejs14.x")
	if aerr == nil {
		t.Fatal("lambdaDeprecatedRuntimeError returned nil for a blocked runtime")
	}
	if aerr.Code != "InvalidParameterValueException" || aerr.HTTPStatus != 400 {
		t.Fatalf("code=%q status=%d, want InvalidParameterValueException/400", aerr.Code, aerr.HTTPStatus)
	}
	want := "The runtime parameter of nodejs14.x is no longer supported for creating or updating AWS Lambda functions. " +
		"We recommend you use the new runtime (nodejs24.x) while creating or updating functions."
	if aerr.Message != want {
		t.Fatalf("message = %q, want %q", aerr.Message, want)
	}
}

func TestRuntimeCatalog_listedRuntimesReflectExecutionAndLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	catalog := runtimeCatalog(now)
	if len(catalog) != len(lambdaRuntimeCatalog) {
		t.Fatalf("catalog has %d entries, want %d", len(catalog), len(lambdaRuntimeCatalog))
	}
	byID := make(map[string]RuntimeInfo, len(catalog))
	for _, info := range catalog {
		byID[info.ID] = info
	}
	for _, spec := range lambdaRuntimeCatalog {
		info, ok := byID[spec.ID]
		if !ok {
			t.Fatalf("%s missing from the listed catalog", spec.ID)
		}
		if info.Supported != (spec.Image != "") {
			t.Errorf("%s: Supported = %v, want %v (must agree with CreateFunction)", spec.ID, info.Supported, spec.Image != "")
		}
		if info.ImageURI != spec.Image {
			t.Errorf("%s: ImageURI = %q, want %q", spec.ID, info.ImageURI, spec.Image)
		}
		if info.Deprecated != runtimeDeprecated(spec.ID, now) {
			t.Errorf("%s: Deprecated = %v, want %v", spec.ID, info.Deprecated, runtimeDeprecated(spec.ID, now))
		}
		if info.CreateBlocked != runtimeCreateBlocked(spec.ID, now) {
			t.Errorf("%s: CreateBlocked = %v, want %v", spec.ID, info.CreateBlocked, runtimeCreateBlocked(spec.ID, now))
		}
	}
	// The web UI offers Supported && !CreateBlocked. That set must be
	// non-empty and must cover every runtime family Overcast can execute.
	families := map[string]bool{}
	for _, info := range catalog {
		if info.Supported && !info.CreateBlocked {
			families[info.Family] = true
		}
	}
	for _, want := range []string{"Node.js", "Python", "Java", ".NET", "Ruby", "Custom runtime"} {
		if !families[want] {
			t.Errorf("no creatable executable runtime for family %q", want)
		}
	}
}

func TestRuntimeCatalog_newlyAddedRuntimesPerFamilyAreExecutable(t *testing.T) {
	// One newly supported runtime per family, with the official base image
	// each must resolve to. These are the mappings issue #659 adds.
	want := map[string]string{
		"python3.14":    "public.ecr.aws/lambda/python:3.14",
		"java25":        "public.ecr.aws/lambda/java:25",
		"java17.al2023": "public.ecr.aws/lambda/java:17.al2023",
		"dotnet10":      "public.ecr.aws/lambda/dotnet:10",
		"ruby3.4":       "public.ecr.aws/lambda/ruby:3.4",
		"ruby4.0":       "public.ecr.aws/lambda/ruby:4.0",
		"nodejs18.x":    "public.ecr.aws/lambda/nodejs:18",
		"provided.al2":  "public.ecr.aws/lambda/provided:al2",
	}
	for id, image := range want {
		fn := &Function{Runtime: id, PackageType: "Zip"}
		got, err := imageForFunction(fn)
		if err != nil {
			t.Errorf("imageForFunction(%s): %v", id, err)
			continue
		}
		if got != image {
			t.Errorf("imageForFunction(%s) = %q, want %q", id, got, image)
		}
	}
}
