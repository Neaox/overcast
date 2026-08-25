/**
 * S3 account regional namespace — name shape shared by the create-bucket
 * dialog and the bucket list badge.
 *
 * AWS's account regional namespace reserves a trailing
 * "-<accountId>-<region>-an" suffix on the bucket name (mirrors
 * internal/serviceutil/validation.go's AccountRegionalBucketSuffix, the
 * single place Overcast's server knows this grammar). `CreateBucketCommand`
 * carries the namespace explicitly via `BucketNamespace`, but `ListBuckets`
 * returns no such field for an existing bucket — real AWS's console shows
 * nothing for one either. The bucket list badge is therefore an Overcast-side
 * enhancement inferred from the name, not a fact reported by AWS: same as
 * anyone reading the list and recognising the suffix themselves.
 */

/**
 * Matches a name ending "-<12 digit account id>-<region>-an". The region
 * segment is matched loosely against AWS's `xx[-gov]-word(s)-digit` shape
 * (e.g. "us-east-1", "ap-southeast-2", "us-gov-west-1") rather than a fixed
 * region list, so a region AWS adds later still gets recognised.
 *
 * This is a heuristic, not a parser: a name that merely ends in "-an" without
 * a matching account id and region ahead of it (e.g. a bucket literally named
 * "my-plan-an") must not match, which is what keeps the badge honest.
 */
const ACCOUNT_REGIONAL_NAME_PATTERN = /-\d{12}-[a-z]{2}(?:-gov)?-[a-z]+-\d-an$/

/** Reports whether `name` looks like an account regional namespace bucket. */
export function isAccountRegionalBucketName(name: string): boolean {
  return ACCOUNT_REGIONAL_NAME_PATTERN.test(name)
}

/**
 * Builds the full account-regional bucket name from a user-supplied prefix —
 * the client-side mirror of the server's AccountRegionalBucketSuffix.
 */
export function accountRegionalBucketName(
  prefix: string,
  accountId: string,
  region: string,
): string {
  return `${prefix}-${accountId}-${region}-an`
}
