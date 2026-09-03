+. [docs] `make docs-lint` now fails a published page over 6,000 characters of prose or 12,000 characters of page
  going over needs a stated reason on the page: `<!-- docs-length-review: … -->`, which fails if the reason is empty or if the page later comes inside the budget.
  both measures exclude the generated capability block, so an operations sub-page passes on its own measurements rather than by being named in an exemption list.
+. [docs] `make docs-lint` now rejects the house-style tells — "not X — it's Y", "it's not about X", "seamless", "delve", the three-adjective slogan
  a page-and-phrase allowlist beside the linter takes a genuine exception, and fails once the sentence it was granted for is rewritten.
