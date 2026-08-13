package lambda

import (
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// lambdaRuntimeValues mirrors com.amazonaws.lambda#Runtime in the pinned AWS
// Smithy model (models/aws/VERSION, model date 2026-07-27). Raw models are not
// vendored and the generated operation manifest does not retain enum metadata,
// so the values live beside the Lambda service — in lambdaRuntimeCatalog, which
// carries each runtime's execution image and AWS lifecycle dates alongside its
// identifier. Model order is preserved so invalid-value responses stay
// deterministic and AWS-shaped.
var lambdaRuntimeValues = func() []string {
	values := make([]string, 0, len(lambdaRuntimeCatalog))
	for _, spec := range lambdaRuntimeCatalog {
		values = append(values, spec.ID)
	}
	return values
}()

var lambdaRuntimeValueSet = func() map[string]struct{} {
	values := make(map[string]struct{}, len(lambdaRuntimeValues))
	for _, runtime := range lambdaRuntimeValues {
		values[runtime] = struct{}{}
	}
	return values
}()

// lambdaRuntimeVersionARNPattern mirrors com.amazonaws.lambda#RuntimeVersionArn
// in the same pinned model. Lambda accepts either a modeled runtime identifier
// or a runtime-version ARN in the Runtime member.
var lambdaRuntimeVersionARNPattern = lazyRegexp(`^arn:(aws[a-zA-Z-]*):lambda:(eusc-)?[a-z]{2}((-gov)|(-iso([a-z]?)))?-[a-z]+-\d{1}::runtime:.+$`)

func validateLambdaRuntime(runtime string) *protocol.AWSError {
	if _, ok := lambdaRuntimeValueSet[runtime]; ok {
		return nil
	}
	if len(runtime) >= 26 && len(runtime) <= 2048 && lambdaRuntimeVersionARNPattern().MatchString(runtime) {
		return nil
	}
	return lambdaInvalidParameter(
		"Value " + runtime + " at 'runtime' failed to satisfy constraint: Member must satisfy enum value set: [" +
			strings.Join(lambdaRuntimeValues, ", ") + "] or be a valid ARN",
	)
}

func lambdaRuntimeExecutionSupported(runtime string) bool {
	_, supported := activeRuntimes[runtime]
	return supported
}

// lambdaDeprecatedRuntimeError returns the response AWS gives once a runtime
// has passed the relevant deprecation phase. AWS names the recommended
// successor; the newest runtime of a family has none, in which case the
// recommendation sentence is omitted rather than invented.
func lambdaDeprecatedRuntimeError(runtime string) *protocol.AWSError {
	message := "The runtime parameter of " + runtime +
		" is no longer supported for creating or updating AWS Lambda functions."
	if successor := runtimeLifecycles[runtime].successor; successor != "" {
		message += " We recommend you use the new runtime (" + successor +
			") while creating or updating functions."
	}
	return lambdaInvalidParameter(message)
}
