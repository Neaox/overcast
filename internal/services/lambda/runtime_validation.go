package lambda

import (
	"regexp"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

// lambdaRuntimeValues mirrors com.amazonaws.lambda#Runtime in the pinned AWS
// Smithy model (models/aws/VERSION, model date 2026-07-27). Raw models are not
// vendored and the generated operation manifest does not retain enum metadata,
// so this small request-validation set must live beside the Lambda service.
// Keep it in model order so invalid-value responses remain deterministic and
// AWS-shaped.
var lambdaRuntimeValues = []string{
	"nodejs", "nodejs4.3", "nodejs6.10", "nodejs8.10", "nodejs10.x", "nodejs12.x", "nodejs14.x", "nodejs16.x", "nodejs18.x", "nodejs20.x", "nodejs22.x", "nodejs24.x",
	"java8", "java8.al2", "java11", "java17", "java21", "java25",
	"python2.7", "python3.6", "python3.7", "python3.8", "python3.9", "python3.10", "python3.11", "python3.12", "python3.13", "python3.14",
	"dotnetcore1.0", "dotnetcore2.0", "dotnetcore2.1", "dotnetcore3.1", "dotnet6", "dotnet8", "dotnet10",
	"nodejs4.3-edge", "go1.x",
	"ruby2.5", "ruby2.7", "ruby3.2", "ruby3.3", "ruby3.4", "ruby4.0",
	"provided", "provided.al2", "provided.al2023",
	"java8.al2023", "java11.al2023", "java17.al2023",
}

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
var lambdaRuntimeVersionARNPattern = regexp.MustCompile(`^arn:(aws[a-zA-Z-]*):lambda:(eusc-)?[a-z]{2}((-gov)|(-iso([a-z]?)))?-[a-z]+-\d{1}::runtime:.+$`)

func validateLambdaRuntime(runtime string) *protocol.AWSError {
	if _, ok := lambdaRuntimeValueSet[runtime]; ok {
		return nil
	}
	if len(runtime) >= 26 && len(runtime) <= 2048 && lambdaRuntimeVersionARNPattern.MatchString(runtime) {
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
