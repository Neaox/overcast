~ [docs/services] the networking and monitoring service pages follow the service page template
  ec2, elb, route53, cloudfront, apigateway, appsync, autoscaling, cloudwatch, cloudwatch-logs and cloudtrail each open with a status line and a copy-pasteable quick start; the long divergence lists are split into <service>/limitations.md, and docs/services/README.md is a scannable index rather than a prose page
* [docs/cloudfront] cloudfront.md described a distribution the emulator does not have
  DomainName is minted on the host you reached Overcast on and is routable, not a synthetic {id}.cloudfront.net; deleting a distribution cascades its invalidations and purges the proxy cache; and the origin proxy caches GET responses, executes CloudFront Functions, and dials any origin Overcast answers for locally. Trusted key groups, signed URLs, origin access control, response headers policies and origin request policies are documented as stored but unenforced
* [docs/appsync] appsync.md described the wrong authorization behaviour in both directions
  the AWS_LAMBDA authorizer is fully executed rather than an accept-all stub, while Cognito and OIDC bearer tokens have their claims read without their signature or expiry being checked, and AWS_IAM is accepted unconditionally
* [docs/elb] elb.md said a redirect-only listener still forwards
  a listener carrying only a RedirectConfig or FixedResponseConfig answers 503, which is the shape of the standard CDK HTTP-to-HTTPS pair; ModifyLoadBalancerAttributes is also a 501 the page never mentioned
* [docs/apigateway] apigateway.md gave only REST v1's path-style invoke URL
  HTTP v2's is /v2/apis/{apiId}/stages/{stage}/*, and the integration types that execute are listed per version
* [docs/cloudwatch-logs] cloudwatch-logs.md omitted which operations are not served
  StartLiveTail works over the JSON protocol and 501s over CBOR, and Logs Insights, subscription filters and metric filters all return 501
* [docs/cloudtrail] cloudtrail.md omitted the support that makes it useful
  AWS::CloudTrail::Trail provisions from CloudFormation, the JSON 1.0 and CBOR protocols are served, and trails are global rather than region-partitioned
~ [cloudfront/appsync] capability notes say what the origin proxy, TestFunction and the AppSync data source types actually do
