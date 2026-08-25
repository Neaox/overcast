*! [dynamodb] table descriptions no longer carry a fabricated top-level `BillingMode` member on CreateTable/DescribeTable/UpdateTable/DeleteTable responses
  migration: raw-JSON consumers reading `BillingMode` off a table description must read `BillingModeSummary.BillingMode` instead (absent for a table left on the default `PROVISIONED` mode, matching AWS); SDK clients are unaffected
