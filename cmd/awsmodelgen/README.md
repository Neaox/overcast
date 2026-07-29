# AWS model generator

`awsmodelgen` converts a pinned checkout of AWS's public
[`aws/api-models-aws`](https://github.com/aws/api-models-aws) Smithy JSON AST
models into Overcast's committed operation manifest and immutable routing
indexes.

It is a build and maintenance tool. The Overcast daemon never reads Smithy
models, contacts GitHub, or constructs model-sized maps at startup. Runtime
routing uses only the compact generated tables in
`internal/awsapi/manifest.gen.go`.

## Source and provenance

`models/aws/VERSION` records the official source URL, exact Git revision, model
date, license, and model format. The generator verifies that the parent
checkout of `-models` is at exactly `-source-revision`; a branch name, stale
checkout, or different commit is rejected.

Raw model files are not vendored. Keep a local checkout for manual regeneration:

```sh
git clone https://github.com/aws/api-models-aws
git -C api-models-aws checkout <revision-from-models/aws/VERSION>
make generate-aws-operations \
  AWS_MODELS_DIR=/path/to/api-models-aws/models
```

Never hand-edit or hand-merge `internal/awsapi/manifest.gen.go`.

## Validation

Normal pull requests run the no-network checks against the committed corpus,
including a check that the committed manifest still hashes to the
`manifest-sha256` recorded in `models/aws/VERSION`. That catches a hand-edit or
a partial merge without needing the models:

```sh
make aws-models-check
```

When the pinned checkout is available, add `AWS_MODELS_DIR` to regenerate the
manifest in memory and compare it byte-for-byte without overwriting the
committed file:

```sh
make aws-models-check \
  AWS_MODELS_DIR=/path/to/api-models-aws/models
```

The underlying check mode is:

```sh
go run ./cmd/awsmodelgen \
  -models /path/to/api-models-aws/models \
  -source-revision <revision> \
  -output internal/awsapi/manifest.gen.go \
  -check
```

## Refresh inventories

The scheduled model-refresh workflow generates temporary JSON inventories for
the old and new revisions, then writes a Markdown review summary:

```sh
# Generate the baseline while the model checkout is at the old revision.
go run ./cmd/awsmodelgen \
  -models /path/to/api-models-aws/models \
  -source-revision <old-revision> \
  -output <temporary-directory>/baseline-manifest.go \
  -inventory-output <temporary-directory>/baseline-inventory.json

# Generate the committed manifest and compare after checking out the new revision.
go run ./cmd/awsmodelgen \
  -models /path/to/api-models-aws/models \
  -source-revision <new-revision> \
  -output internal/awsapi/manifest.gen.go \
  -inventory-output <temporary-directory>/current-inventory.json \
  -baseline-inventory <temporary-directory>/baseline-inventory.json \
  -summary-output <temporary-directory>/aws-model-summary.md \
  -version-file models/aws/VERSION \
  -model-date YYYY-MM-DD
```

The summary reports:

- added and removed services and operations;
- protocol-trait changes;
- HTTP, target, signing-name, and RPC service-shape binding changes;
- added and removed routing collisions;
- claimable and explicitly ambiguous non-S3 fallback coverage; and
- newly uncovered or resolved operations.

Inventories and summaries are temporary review artifacts, not runtime inputs.
Output is deterministic for a given source revision.

## Automated refresh

`.github/workflows/aws-model-refresh.yml` checks upstream weekly and on manual
dispatch. When AWS publishes a new revision, it:

1. restores and verifies a cached upstream Git mirror;
2. asserts the committed manifest still matches the *pinned* revision, so an
   already-stale manifest cannot be carried forward silently;
3. generates old and new inventories and the manifest;
4. runs `make aws-models-check` with full regeneration enabled, which at that
   point asserts determinism rather than staleness;
5. force-with-lease updates only `automation/aws-api-models`; and
6. creates or updates one pull request without merging it.

See `CONTRIBUTING.md` and
`docs/plans/aws-api-operation-coverage.md` for maintainer configuration and the
larger operation-coverage design.
