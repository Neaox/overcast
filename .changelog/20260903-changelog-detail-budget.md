~ [ci] `changelog.py check` budgets an entry's detail: three continuation lines, 200 chars each
  a breaking entry's `migration:` note does not count towards the three, and is held to the same length
  `<!-- changelog-detail-review: why -->` above an entry buys it more, and fails once that entry is back inside the budget
