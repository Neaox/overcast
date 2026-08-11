---
title: "OpenSearch — Amazon OpenSearch Service"
description: "Amazon OpenSearch Service uses the REST JSON protocol. Routes are served under the /_opensearch/ path prefix."
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

Amazon OpenSearch Service uses the REST JSON protocol. Routes are served under
the `/_opensearch/` path prefix.

---

## Notes

- REST routes are prefixed with `/_opensearch/` (e.g. `POST /_opensearch/domain`).
- Unrecognized operations return a JSON `501 Not Implemented` error response.
- Domain resources are stored in-memory but no actual OpenSearch cluster is provisioned.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | 🚧 WIP |
| -------- | ------ |
| Domains  | 5      |
| Tags     | 3      |

---

## Endpoints

### Domains

| Operation         | Status | Notes                                                                                               | AWS Docs                                                                                            |
| ----------------- | ------ | --------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `CreateDomain`    | 🚧 WIP | creates a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)       | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_CreateDomain.html)    |
| `DescribeDomain`  | 🚧 WIP | returns domain details; Overcast does not serve the binding AWS models, so no SDK reaches it (#856) | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomain.html)  |
| `DescribeDomains` | 🚧 WIP | batch describe; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)         | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DescribeDomains.html) |
| `ListDomainNames` | 🚧 WIP | lists all domain names; Overcast does not serve the binding AWS models, so no SDK reaches it (#856) | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListDomainNames.html) |
| `DeleteDomain`    | 🚧 WIP | deletes a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)       | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_DeleteDomain.html)    |

### Tags

| Operation    | Status | Notes                                                                                                | AWS Docs                                                                                       |
| ------------ | ------ | ---------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `AddTags`    | 🚧 WIP | adds tags to a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)   | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_AddTags.html)    |
| `ListTags`   | 🚧 WIP | lists tags for a domain; Overcast does not serve the binding AWS models, so no SDK reaches it (#856) | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_ListTags.html)   |
| `RemoveTags` | 🚧 WIP | removes tags; Overcast does not serve the binding AWS models, so no SDK reaches it (#856)            | [docs](https://docs.aws.amazon.com/opensearch-service/latest/APIReference/API_RemoveTags.html) |

<!-- END overcast:capabilities -->
