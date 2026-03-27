# Changelog

All notable changes to Overcast are documented here.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## Versioning rules

| Change type                                            | Version bump  | Examples                                                                     |
| ------------------------------------------------------ | ------------- | ---------------------------------------------------------------------------- |
| Breaking change to any supported API endpoint          | MAJOR (x.0.0) | Changing response format, removing a supported field, changing env var names |
| New service or new supported endpoint                  | MINOR (0.x.0) | Adding DynamoDB support, adding S3 multipart upload                          |
| Bug fix, performance improvement, new internal feature | PATCH (0.0.x) | Fixing a response field, adding a missing header, improving error messages   |
| Documentation, test, CI changes only                   | PATCH (0.0.x) | No user-facing change                                                        |

**When in doubt, bump MINOR.** We would rather ship a minor bump that didn't
need it than accidentally ship a breaking change as a patch.

### What counts as a breaking change

- Removing or renaming an environment variable
- Changing the format of a response that was previously emitting a different format
- Removing a previously supported endpoint (demoting ✅ to ❌)
- Changing the default value of a configuration option in a way that alters existing behaviour
- Changing the port default

### What does NOT count as a breaking change

- Adding new fields to a response (AWS SDKs ignore unknown fields)
- Adding new endpoints
- Adding new environment variables
- Improving error messages
- Performance improvements
- Fixing a response that was wrong (bug fix > compatibility with wrong behaviour)

---

## [Unreleased]

### Added

- S3 `ListObjectsV2`: continuation-token pagination — `continuation-token`, `max-keys`, `IsTruncated`, and `NextContinuationToken` now work correctly per the AWS spec
- Web UI: bucket object browser uses TanStack Infinite Query + TanStack Virtual — renders buckets with thousands of objects at 60 fps, fetches pages as you scroll
- Initial project scaffold with S3 (P1+P2) and SQS (P1+P2) complete
- In-memory and SQLite state backends
- Host binding (`OVERCAST_HOST`), HTTPS/TLS support
- Debug endpoints (`/_debug/*`, gated by `OVERCAST_DEBUG`)
- Health endpoint (`/_health`)
- DynamoDB, SNS, Lambda service stubs (dispatch wired, implementations in progress)
- Docker / docker-compose support
- Full test infrastructure: unit tests, integration tests, GWT pattern, shared helpers, mock store

---

<!-- New releases are prepended above this line -->
<!-- Template:

## [x.y.z] - YYYY-MM-DD

### Added
- ...

### Changed
- ...

### Deprecated
- ...

### Removed
- ...

### Fixed
- ...

### Security
- ...

[x.y.z]: https://github.com/your-org/overcast/compare/vA.B.C...vx.y.z
-->

[Unreleased]: https://github.com/your-org/overcast/compare/v0.1.0...HEAD
