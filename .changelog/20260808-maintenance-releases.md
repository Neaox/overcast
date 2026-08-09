+ [release] Docker line tags `:1` and `:1.2`, so a user can pin a release line rather than a single version or the moving `:latest`
~ [release] the floating image tags (`:latest`, `:alpha`, the new line tags) only ever move forward — a maintenance release of an older line no longer takes `:latest` back with it
+ [release] a maintenance-release path for patching an older minor after 1.0: `support/<major>.<minor>` branches cut from their tag, backports cherry-picked out of `main`, and the full test and compat suites running on them
