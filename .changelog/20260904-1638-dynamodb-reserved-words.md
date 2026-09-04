*! [dynamodb] a DynamoDB reserved word used as a bare attribute name in an expression is rejected, as AWS rejects it (#1638).
  covers `UpdateExpression`, `ConditionExpression`, `FilterExpression`, `KeyConditionExpression` and `ProjectionExpression`, with AWS's message naming the parameter and the keyword.
  the escape hatch is unchanged and still the documented one: reach the attribute through an `ExpressionAttributeNames` alias.
  `size(...)` and the other function names are not attribute names and keep parsing; the 573-word list is generated from the AWS Developer Guide.
  migration: reach a reserved word through an `ExpressionAttributeNames` alias (`#s` mapped to `size`), which is what the same expression already needs on AWS
