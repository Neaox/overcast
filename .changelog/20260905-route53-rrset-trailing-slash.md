* [route53] `ChangeResourceRecordSets` no longer 404s for boto3 and the AWS CLI — the trailing-slash URI they build is registered
  botocore binds the operation to `POST /2013-04-01/hostedzone/{Id}/rrset/`, the Smithy model Overcast pins binds it without the slash, and chi treats the two as separate routes
  `ListResourceRecordSets` answers both spellings too, and the reachability sweep now probes a modeled trailing slash instead of normalising it away
