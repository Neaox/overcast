*! [secretsmanager] `BlockPublicPolicy` refuses a `*` principal whose `Condition` is an empty object, as it already did for an omitted one
  AWS defines a public statement as one with no condition keys, so `Condition: {}` narrows nothing; the check is now shared with Lambda through `serviceutil`
  migration: give a policy that relied on `Condition: {}` a real condition key or a specific principal, or send `BlockPublicPolicy: false`
