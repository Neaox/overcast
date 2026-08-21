* [router/backup/apigateway] a `/tags/{resourceArn}` request whose ARN belongs to a service whose
  tag operations are not implemented answers the generated 501 with the `x-emulator-unsupported`
  marker. API Gateway's ARN-keyed tag store was the dispatcher's fallback for every ARN no other
  service claimed, so `aws backup list-tags` read HTTP 200 `{"tags":{}}` from a service the caller
  never addressed — and Backup implements no tag operations at all. The store still answers its own
  ARNs and AppRegistry's `servicecatalog` ones, whose SDK shares the endpoint.
