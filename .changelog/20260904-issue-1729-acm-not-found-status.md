*! [acm] `ResourceNotFoundException` now answers HTTP 400, matching AWS docs, instead of 404
  migration: raw-HTTP or status-code-based ACM error handling should branch on 400, not 404
