* [cloudformation] a resource that fails because a service call did now reports CloudFormation's own reason shape instead of the raw error body.
  the reason names the service, status, error code and request ID, then the operation's token and a HandlerErrorCode, as real CloudFormation does.
  a body that parses as neither XML nor JSON still falls back to the status and the body, so nothing is lost.
