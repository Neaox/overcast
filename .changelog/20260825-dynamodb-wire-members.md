*! [dynamodb] table descriptions no longer carry fabricated `TTL` and `Tags` members on CreateTable/DescribeTable/UpdateTable/DeleteTable responses
  migration: raw-JSON consumers reading TTL or Tags off a table description must switch to the modeled channels, `DescribeTimeToLive` and `ListTagsOfResource` (already supported); SDK clients are unaffected
