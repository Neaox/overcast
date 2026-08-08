package lambda

// runtime_catalog.go — the single source of truth for Lambda runtimes.
//
// Four views of "which runtimes exist" used to be maintained separately: the
// modeled enum used for request validation, the runtime→image map used to
// execute functions, the deprecation set used to reject creates, and the list
// the web UI renders. They drifted. Everything below is derived from one table
// so they cannot drift again; runtime_catalog_test.go fails if any derived view
// stops agreeing with it.
//
// Two upstream sources feed the table, and neither is guessed:
//
//   - Runtime identifiers and their order come from com.amazonaws.lambda#Runtime
//     in the pinned AWS Smithy model (models/aws/VERSION, revision 66e973ca,
//     model date 2026-07-27). Order matters: it is reproduced verbatim in the
//     enum-validation error message AWS returns for an unmodeled value.
//   - Deprecation, block-create and block-update dates and the recommended
//     successor come from AWS's own published Lambda runtime lifecycle table, as
//     mirrored machine-readably in aws-cloudformation/cfn-lint
//     (src/cfnlint/data/AdditionalSpecs/LmbdRuntimeLifecycle.json). Runtimes the
//     model declares but that table does not yet cover carry no dates and are
//     never blocked.
//
// Image mappings are official AWS Lambda base images on public.ecr.aws/lambda.
// A runtime carries an image only when Overcast can actually execute it; a
// modeled runtime with no image is an honest emulator gap and answers 501,
// never an invented 400. Runtimes AWS has already blocked from CreateFunction
// carry no image because Overcast will never create one: the 400 comes first.

import (
	"time"
)

// runtimeSpec is one row of the Lambda runtime catalog.
type runtimeSpec struct {
	// ID is the modeled runtime identifier (com.amazonaws.lambda#Runtime).
	ID string
	// Name is the human-readable name shown in the web UI.
	Name string
	// Family groups runtimes in the UI and picks the base-image repository.
	Family string
	// DefaultHandler seeds the handler field for new functions.
	DefaultHandler string
	// Image is the official Lambda base image backing this runtime, or "" when
	// Overcast cannot execute it.
	Image string
	// Deprecated, BlockCreate and BlockUpdate are AWS's published lifecycle
	// dates (YYYY-MM-DD, UTC), empty when AWS has not published one.
	Deprecated  string
	BlockCreate string
	BlockUpdate string
	// Successor is the runtime AWS recommends in its deprecation error, empty
	// for the newest runtime of a family.
	Successor string
}

const (
	familyNodeJS = "Node.js"
	familyPython = "Python"
	familyJava   = "Java"
	familyDotnet = ".NET"
	familyRuby   = "Ruby"
	familyGo     = "Go"
	familyCustom = "Custom runtime"
)

const (
	handlerNodeJS = "index.handler"
	handlerPython = "lambda_function.handler"
	handlerJava   = "example.Handler::handleRequest"
	handlerDotnet = "Function::FunctionHandler"
	handlerRuby   = "lambda_function.lambda_handler"
	handlerBinary = "bootstrap"
)

// lambdaRuntimeCatalog is the table. Order is the pinned model's enum order.
var lambdaRuntimeCatalog = []runtimeSpec{
	{ID: "nodejs", Name: "Node.js 0.10", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2016-08-30", BlockCreate: "2016-09-30", BlockUpdate: "2016-10-31", Successor: "nodejs24.x"},
	{ID: "nodejs4.3", Name: "Node.js 4.3", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2020-03-05", BlockCreate: "2020-02-03", BlockUpdate: "2020-03-05", Successor: "nodejs24.x"},
	{ID: "nodejs6.10", Name: "Node.js 6.10", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2019-08-12", BlockCreate: "2019-07-12", BlockUpdate: "2019-08-12", Successor: "nodejs24.x"},
	{ID: "nodejs8.10", Name: "Node.js 8.10", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2020-03-06", BlockCreate: "2020-02-04", BlockUpdate: "2020-03-06", Successor: "nodejs24.x"},
	{ID: "nodejs10.x", Name: "Node.js 10", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2021-07-30", BlockCreate: "2021-07-30", BlockUpdate: "2022-02-14", Successor: "nodejs24.x"},
	{ID: "nodejs12.x", Name: "Node.js 12", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2023-03-31", BlockCreate: "2023-03-31", BlockUpdate: "2023-04-30", Successor: "nodejs24.x"},
	{ID: "nodejs14.x", Name: "Node.js 14", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2023-12-04", BlockCreate: "2024-01-09", BlockUpdate: "2027-03-03", Successor: "nodejs24.x"},
	{ID: "nodejs16.x", Name: "Node.js 16", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Image:      "public.ecr.aws/lambda/nodejs:16",
		Deprecated: "2024-06-12", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "nodejs24.x"},
	{ID: "nodejs18.x", Name: "Node.js 18", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Image:      "public.ecr.aws/lambda/nodejs:18",
		Deprecated: "2025-09-01", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "nodejs24.x"},
	{ID: "nodejs20.x", Name: "Node.js 20", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Image:      "public.ecr.aws/lambda/nodejs:20",
		Deprecated: "2026-04-30", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "nodejs24.x"},
	{ID: "nodejs22.x", Name: "Node.js 22", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Image:      "public.ecr.aws/lambda/nodejs:22",
		Deprecated: "2027-04-30", BlockCreate: "2027-06-01", BlockUpdate: "2027-07-01", Successor: "nodejs24.x"},
	{ID: "nodejs24.x", Name: "Node.js 24", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Image:      "public.ecr.aws/lambda/nodejs:24",
		Deprecated: "2028-04-30", BlockCreate: "2028-06-01", BlockUpdate: "2028-07-01"},

	{ID: "java8", Name: "Java 8", Family: familyJava, DefaultHandler: handlerJava,
		Deprecated: "2024-01-08", BlockCreate: "2024-02-08", BlockUpdate: "2027-03-03", Successor: "java25"},
	{ID: "java8.al2", Name: "Java 8 (AL2)", Family: familyJava, DefaultHandler: handlerJava,
		Image:      "public.ecr.aws/lambda/java:8.al2",
		Deprecated: "2027-06-30", BlockCreate: "2027-07-31", BlockUpdate: "2027-08-31", Successor: "java25"},
	{ID: "java11", Name: "Java 11", Family: familyJava, DefaultHandler: handlerJava,
		Image:      "public.ecr.aws/lambda/java:11",
		Deprecated: "2027-06-30", BlockCreate: "2027-07-31", BlockUpdate: "2027-08-31", Successor: "java25"},
	{ID: "java17", Name: "Java 17", Family: familyJava, DefaultHandler: handlerJava,
		Image:      "public.ecr.aws/lambda/java:17",
		Deprecated: "2027-06-30", BlockCreate: "2027-07-31", BlockUpdate: "2027-08-31", Successor: "java25"},
	{ID: "java21", Name: "Java 21", Family: familyJava, DefaultHandler: handlerJava,
		Image:      "public.ecr.aws/lambda/java:21",
		Deprecated: "2029-06-30", BlockCreate: "2029-07-31", BlockUpdate: "2029-08-31", Successor: "java25"},
	{ID: "java25", Name: "Java 25", Family: familyJava, DefaultHandler: handlerJava,
		Image:      "public.ecr.aws/lambda/java:25",
		Deprecated: "2029-06-30", BlockCreate: "2029-07-31", BlockUpdate: "2029-08-31"},

	{ID: "python2.7", Name: "Python 2.7", Family: familyPython, DefaultHandler: handlerPython,
		Deprecated: "2021-07-15", BlockCreate: "2021-07-15", BlockUpdate: "2022-05-30", Successor: "python3.14"},
	{ID: "python3.6", Name: "Python 3.6", Family: familyPython, DefaultHandler: handlerPython,
		Deprecated: "2022-07-18", BlockCreate: "2022-07-18", BlockUpdate: "2022-08-29", Successor: "python3.14"},
	{ID: "python3.7", Name: "Python 3.7", Family: familyPython, DefaultHandler: handlerPython,
		Deprecated: "2023-12-04", BlockCreate: "2024-01-09", BlockUpdate: "2027-03-03", Successor: "python3.14"},
	{ID: "python3.8", Name: "Python 3.8", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.8",
		Deprecated: "2024-10-14", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "python3.14"},
	{ID: "python3.9", Name: "Python 3.9", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.9",
		Deprecated: "2025-12-15", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "python3.14"},
	{ID: "python3.10", Name: "Python 3.10", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.10",
		Deprecated: "2026-10-31", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "python3.14"},
	{ID: "python3.11", Name: "Python 3.11", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.11",
		Deprecated: "2027-06-30", BlockCreate: "2027-07-31", BlockUpdate: "2027-08-31", Successor: "python3.14"},
	{ID: "python3.12", Name: "Python 3.12", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.12",
		Deprecated: "2028-10-31", BlockCreate: "2028-11-30", BlockUpdate: "2029-01-10", Successor: "python3.14"},
	{ID: "python3.13", Name: "Python 3.13", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.13",
		Deprecated: "2029-06-30", BlockCreate: "2029-07-31", BlockUpdate: "2029-08-31", Successor: "python3.14"},
	{ID: "python3.14", Name: "Python 3.14", Family: familyPython, DefaultHandler: handlerPython,
		Image:      "public.ecr.aws/lambda/python:3.14",
		Deprecated: "2029-06-30", BlockCreate: "2029-07-31", BlockUpdate: "2029-08-31"},

	{ID: "dotnetcore1.0", Name: ".NET Core 1.0", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Deprecated: "2019-06-27", BlockCreate: "2019-06-30", BlockUpdate: "2019-07-30", Successor: "dotnet10"},
	{ID: "dotnetcore2.0", Name: ".NET Core 2.0", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Deprecated: "2019-05-30", BlockCreate: "2019-04-30", BlockUpdate: "2019-05-30", Successor: "dotnet10"},
	{ID: "dotnetcore2.1", Name: ".NET Core 2.1", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Deprecated: "2022-01-05", BlockCreate: "2022-01-05", BlockUpdate: "2022-04-13", Successor: "dotnet10"},
	{ID: "dotnetcore3.1", Name: ".NET Core 3.1", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Deprecated: "2023-04-03", BlockCreate: "2023-04-03", BlockUpdate: "2023-05-03", Successor: "dotnet10"},
	{ID: "dotnet6", Name: ".NET 6", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Image:      "public.ecr.aws/lambda/dotnet:6",
		Deprecated: "2024-12-20", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "dotnet10"},
	{ID: "dotnet8", Name: ".NET 8", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Image:      "public.ecr.aws/lambda/dotnet:8",
		Deprecated: "2026-11-10", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "dotnet10"},
	{ID: "dotnet10", Name: ".NET 10", Family: familyDotnet, DefaultHandler: handlerDotnet,
		Image:      "public.ecr.aws/lambda/dotnet:10",
		Deprecated: "2028-11-14", BlockCreate: "2028-12-14", BlockUpdate: "2029-01-15"},

	{ID: "nodejs4.3-edge", Name: "Node.js 4.3 (edge)", Family: familyNodeJS, DefaultHandler: handlerNodeJS,
		Deprecated: "2020-03-05", BlockCreate: "2019-03-31", BlockUpdate: "2019-04-30", Successor: "nodejs24.x"},
	{ID: "go1.x", Name: "Go 1.x", Family: familyGo, DefaultHandler: handlerBinary,
		Deprecated: "2024-01-08", BlockCreate: "2024-02-08", BlockUpdate: "2027-03-03", Successor: "provided.al2023"},

	{ID: "ruby2.5", Name: "Ruby 2.5", Family: familyRuby, DefaultHandler: handlerRuby,
		Deprecated: "2021-07-30", BlockCreate: "2021-07-30", BlockUpdate: "2022-03-31", Successor: "ruby4.0"},
	{ID: "ruby2.7", Name: "Ruby 2.7", Family: familyRuby, DefaultHandler: handlerRuby,
		Deprecated: "2023-12-07", BlockCreate: "2024-01-09", BlockUpdate: "2027-03-03", Successor: "ruby4.0"},
	{ID: "ruby3.2", Name: "Ruby 3.2", Family: familyRuby, DefaultHandler: handlerRuby,
		Image:      "public.ecr.aws/lambda/ruby:3.2",
		Deprecated: "2026-03-31", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "ruby4.0"},
	{ID: "ruby3.3", Name: "Ruby 3.3", Family: familyRuby, DefaultHandler: handlerRuby,
		Image:      "public.ecr.aws/lambda/ruby:3.3",
		Deprecated: "2027-03-31", BlockCreate: "2027-04-30", BlockUpdate: "2027-05-31", Successor: "ruby4.0"},
	{ID: "ruby3.4", Name: "Ruby 3.4", Family: familyRuby, DefaultHandler: handlerRuby,
		Image:      "public.ecr.aws/lambda/ruby:3.4",
		Deprecated: "2028-03-31", BlockCreate: "2028-04-30", BlockUpdate: "2028-05-31", Successor: "ruby4.0"},
	{ID: "ruby4.0", Name: "Ruby 4.0", Family: familyRuby, DefaultHandler: handlerRuby,
		Image:      "public.ecr.aws/lambda/ruby:4.0",
		Deprecated: "2029-03-31", BlockCreate: "2029-04-30", BlockUpdate: "2029-05-31"},

	{ID: "provided", Name: "Custom (Amazon Linux 1)", Family: familyCustom, DefaultHandler: handlerBinary,
		Deprecated: "2024-01-08", BlockCreate: "2024-02-08", BlockUpdate: "2027-03-03", Successor: "provided.al2023"},
	{ID: "provided.al2", Name: "Custom (AL2)", Family: familyCustom, DefaultHandler: handlerBinary,
		Image:      "public.ecr.aws/lambda/provided:al2",
		Deprecated: "2026-07-31", BlockCreate: "2027-02-01", BlockUpdate: "2027-03-03", Successor: "provided.al2023"},
	{ID: "provided.al2023", Name: "Custom (AL2023)", Family: familyCustom, DefaultHandler: handlerBinary,
		Image:      "public.ecr.aws/lambda/provided:al2023",
		Deprecated: "2029-06-30", BlockCreate: "2029-07-31", BlockUpdate: "2029-08-31"},

	// AWS models the Amazon Linux 2023 Java runtimes and publishes their base
	// images, but has not published lifecycle dates for them at the pinned
	// model date. No dates means no blocking — inventing one is not an option.
	{ID: "java8.al2023", Name: "Java 8 (AL2023)", Family: familyJava, DefaultHandler: handlerJava,
		Image: "public.ecr.aws/lambda/java:8.al2023"},
	{ID: "java11.al2023", Name: "Java 11 (AL2023)", Family: familyJava, DefaultHandler: handlerJava,
		Image: "public.ecr.aws/lambda/java:11.al2023"},
	{ID: "java17.al2023", Name: "Java 17 (AL2023)", Family: familyJava, DefaultHandler: handlerJava,
		Image: "public.ecr.aws/lambda/java:17.al2023"},
}

// runtimeLifecycle holds one runtime's parsed AWS lifecycle dates.
type runtimeLifecycle struct {
	deprecated  time.Time
	blockCreate time.Time
	blockUpdate time.Time
	successor   string
}

var (
	// runtimeSpecByID indexes the catalog by runtime identifier.
	runtimeSpecByID map[string]runtimeSpec
	// runtimeLifecycles holds the parsed lifecycle dates, one entry per
	// runtime AWS has published dates for.
	runtimeLifecycles map[string]runtimeLifecycle
	// activeRuntimes maps every runtime Overcast can execute to its official
	// Lambda base image. ContainerRuntime dispatches on it.
	activeRuntimes map[string]string
)

func init() {
	runtimeSpecByID = make(map[string]runtimeSpec, len(lambdaRuntimeCatalog))
	runtimeLifecycles = make(map[string]runtimeLifecycle, len(lambdaRuntimeCatalog))
	activeRuntimes = make(map[string]string, len(lambdaRuntimeCatalog))
	for _, spec := range lambdaRuntimeCatalog {
		runtimeSpecByID[spec.ID] = spec
		if spec.Image != "" {
			activeRuntimes[spec.ID] = spec.Image
		}
		if spec.Deprecated == "" && spec.BlockCreate == "" && spec.BlockUpdate == "" {
			continue
		}
		runtimeLifecycles[spec.ID] = runtimeLifecycle{
			deprecated:  mustParseRuntimeDate(spec.ID, spec.Deprecated),
			blockCreate: mustParseRuntimeDate(spec.ID, spec.BlockCreate),
			blockUpdate: mustParseRuntimeDate(spec.ID, spec.BlockUpdate),
			successor:   spec.Successor,
		}
	}
}

// mustParseRuntimeDate parses a catalog lifecycle date. Its only inputs are the
// literals in this file, so a failure is a typo in the table and must not be
// allowed to reach a running emulator as a silently-ignored date.
func mustParseRuntimeDate(runtimeID, date string) time.Time {
	if date == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic("lambda: runtime " + runtimeID + " has malformed lifecycle date " + date)
	}
	return parsed.UTC()
}

// runtimeDeprecated reports whether AWS has ended support for the runtime as of
// now. Deprecation alone does not stop CreateFunction — see runtimeCreateBlocked.
func runtimeDeprecated(runtimeID string, now time.Time) bool {
	return reachedRuntimePhase(runtimeLifecycles[runtimeID].deprecated, now)
}

// runtimeCreateBlocked reports whether AWS refuses to create new functions with
// the runtime as of now (AWS's "block function create" phase).
func runtimeCreateBlocked(runtimeID string, now time.Time) bool {
	return reachedRuntimePhase(runtimeLifecycles[runtimeID].blockCreate, now)
}

// runtimeUpdateBlocked reports whether AWS refuses to update functions using
// the runtime as of now (AWS's "block function update" phase).
func runtimeUpdateBlocked(runtimeID string, now time.Time) bool {
	return reachedRuntimePhase(runtimeLifecycles[runtimeID].blockUpdate, now)
}

func reachedRuntimePhase(phase, now time.Time) bool {
	return !phase.IsZero() && !now.UTC().Before(phase)
}

// runtimeCatalog renders the catalog for the emulator-only runtime listing the
// web UI reads. Supported mirrors exactly what CreateFunction will accept for
// execution, so the UI cannot offer a runtime the API answers 501 for.
func runtimeCatalog(now time.Time) []RuntimeInfo {
	out := make([]RuntimeInfo, 0, len(lambdaRuntimeCatalog))
	for _, spec := range lambdaRuntimeCatalog {
		out = append(out, RuntimeInfo{
			ID:             spec.ID,
			Name:           spec.Name,
			Family:         spec.Family,
			DefaultHandler: spec.DefaultHandler,
			ImageURI:       spec.Image,
			Deprecated:     runtimeDeprecated(spec.ID, now),
			CreateBlocked:  runtimeCreateBlocked(spec.ID, now),
			UpdateBlocked:  runtimeUpdateBlocked(spec.ID, now),
			Supported:      spec.Image != "",
		})
	}
	return out
}
