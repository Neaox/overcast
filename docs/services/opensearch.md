# OpenSearch — Amazon OpenSearch Service

> AWS docs: https://docs.aws.amazon.com/opensearch-service/latest/APIReference/

Amazon OpenSearch Service uses the REST JSON protocol. Routes are served under
the `/_opensearch/` path prefix.

---

## Notes

- REST routes are prefixed with `/_opensearch/` (e.g. `POST /_opensearch/domain`).
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Domain resources are stored in-memory but no actual OpenSearch cluster is provisioned.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Domains  | 5            |
| Tags     | 3            |

---

## Endpoints

### Domains

| Operation         | Status       | Notes                                       | AWS Docs                                                                                            |
| ----------------- | ------------ | ------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `CreateDomain`    | ✅ Supported | POST /domain — creates a domain             | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_CreateDomain.html)    |
| `DescribeDomain`  | ✅ Supported | GET /domain/{name} — returns domain details | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomain.html)  |
| `DescribeDomains` | ✅ Supported | POST /domain/describe — batch describe      | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomains.html) |
| `ListDomainNames` | ✅ Supported | GET /domain — lists all domain names        | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListDomainNames.html) |
| `DeleteDomain`    | ✅ Supported | DELETE /domain/{name} — deletes a domain    | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DeleteDomain.html)    |

### Tags

| Operation    | Status       | Notes                               | AWS Docs                                                                                       |
| ------------ | ------------ | ----------------------------------- | ---------------------------------------------------------------------------------------------- |
| `AddTags`    | ✅ Supported | POST /tags — adds tags to a domain  | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_AddTags.html)    |
| `ListTags`   | ✅ Supported | GET /tags — lists tags for a domain | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListTags.html)   |
| `RemoveTags` | ✅ Supported | POST /tags-removal — removes tags   | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_RemoveTags.html) |

<!-- END overcast:capabilities -->
