---
title: "OpenSearch — Amazon OpenSearch Service"
description: "Amazon OpenSearch Service uses the REST JSON protocol, served under the /2021-01-01/ API version prefix."
section: "Service Reference"
tags:
  - amazon
  - docs
  - opensearch
  - service
  - services
---

# OpenSearch — Amazon OpenSearch Service

> AWS docs: https://docs.aws.amazon.com/opensearch-service/latest/APIReference/

Amazon OpenSearch Service uses the REST JSON protocol, served under the
`/2021-01-01/` API version prefix that every OpenSearch binding carries. AWS
SDKs and `aws opensearch …` work unmodified.

---

## Notes

- Routes are AWS's own bindings. The domain surface is under
  `/2021-01-01/opensearch/` (e.g. `POST /2021-01-01/opensearch/domain`), while
  `ListDomainNames` and the tag operations sit directly under `/2021-01-01/`.
- Domains are per-region, as on AWS: the same domain name in two regions is two
  domains.
- Operations that are not emulated return a JSON `501 Not Implemented` error
  response.
- Domain records are stored, but no OpenSearch cluster is provisioned, so
  `DomainStatus.Endpoint` names a host that serves nothing.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Domains  | 5            |
| Tags     | 3            |

---

## Endpoints

### Domains

| Operation         | Status       | Notes                                                                                                                    | AWS Docs                                                                                            |
| ----------------- | ------------ | ------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `CreateDomain`    | ✅ Supported | creates a domain, immediately active; inline `TagList` applied at creation; a repeat name in the same region is rejected | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_CreateDomain.html)    |
| `DescribeDomain`  | ✅ Supported | returns the stored domain; only the members an inert domain can populate honestly                                        | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomain.html)  |
| `DescribeDomains` | ✅ Supported | batch describe; a name that matches nothing is omitted from the list                                                     | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomains.html) |
| `ListDomainNames` | ✅ Supported | lists the region's domains; the `engineType` filter is honoured, derived from each domain's `EngineVersion`              | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListDomainNames.html) |
| `DeleteDomain`    | ✅ Supported | deletes a domain and the tags attached to it                                                                             | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DeleteDomain.html)    |

### Tags

| Operation    | Status       | Notes                                                             | AWS Docs                                                                                       |
| ------------ | ------------ | ----------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `AddTags`    | ✅ Supported | adds tags to a resource, addressed by ARN in the body             | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_AddTags.html)    |
| `ListTags`   | ✅ Supported | lists tags for a resource, addressed by the `arn` query parameter | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListTags.html)   |
| `RemoveTags` | ✅ Supported | removes the named tag keys from a resource                        | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_RemoveTags.html) |

<!-- END overcast:capabilities -->
