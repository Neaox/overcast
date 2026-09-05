+ [appsync] optional verification of `AMAZON_COGNITO_USER_POOLS` bearer tokens against the local Cognito user pool
  `OVERCAST_ENFORCE_APPSYNC_COGNITO_AUTH=true` checks the RS256 signature, issuer, `token_use`, expiry and `appIdClientRegex`; anything failing gets AppSync's 401 `UnauthorizedException`
  off by default, so a resolver test can keep sending an unsigned JWT to populate `$ctx.identity`
  the primary authorization mode and any Cognito entry in `additionalAuthenticationProviders` follow the same rule
