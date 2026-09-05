* [cognito] token validation no longer mints a signing key for the pool named in an unverified issuer claim
  a bearer token with a made-up issuer reaching an API Gateway Cognito authorizer cost an RSA-2048 generation and a permanent `cognito:sigkeys` record per issuer
