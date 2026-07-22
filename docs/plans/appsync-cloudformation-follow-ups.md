# AppSync CloudFormation Follow-Ups

> Status: outstanding work after core AppSync CloudFormation/CDK provisioning support.

Core support for `AWS::AppSync::GraphQLApi`, `GraphQLSchema`, `ApiKey`, `DataSource`, `Resolver`, and `FunctionConfiguration` is implemented. Keep this file limited to remaining gaps that still need product or compatibility work.

## Outstanding Work

- Review AppSync API key fidelity. The emulator currently treats the API key ID as the accepted `x-api-key` value; confirm real AWS/CDK-visible behavior and add a separate key value if required.
- Review additional AppSync Events CloudFormation edge cases beyond the supported `AWS::AppSync::Api` and `AWS::AppSync::ChannelNamespace` create/delete lifecycle, including update replacement behavior and nested auth config validation.
- Add CloudFormation end-to-end coverage for AppSync DynamoDB, Lambda, and HTTP data sources.
- Consider a small topology-map enhancement for AppSync nodes to show schema, function, and API-key counts if that proves useful in real CDK workflows.

## Guardrails

- Keep CloudFormation handlers thin: translate properties, call the underlying AppSync REST API, and capture `Ref`/`Fn::GetAtt`/physical IDs.
- Add CloudFormation-specific validation or error translation only when it makes observable behavior closer to real AWS.
- Do not duplicate AppSync service validation, persistence, lifecycle, or execution behavior in the CloudFormation layer.
