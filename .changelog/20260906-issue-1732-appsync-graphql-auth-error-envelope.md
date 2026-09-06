*! [appsync] a GraphQL authorization failure now returns AppSync's GraphQL error envelope instead of the AWS JSON envelope
  status code and errorType/message are unchanged; only errors[0].errorType replaces the top-level __type
  migration: read errors[0].errorType instead of __type when detecting an auth failure on POST .../graphql
