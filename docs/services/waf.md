# WAF — AWS WAF v2

> AWS docs: https://docs.aws.amazon.com/waf/latest/APIReference/Welcome.html

AWS WAF v2 (Web Application Firewall) uses the `application/x-amz-json-1.1`
protocol. Operations are identified by the `X-Amz-Target` header with the
prefix `AWSWAF_20190729.`.

---

## Notes

- Target dispatch header: `X-Amz-Target: AWSWAF_20190729.<Operation>`.
- Unimplemented operations return a JSON `501 Not Implemented` error response.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| Web ACLs | 4            |

---

## Endpoints

### Web ACLs

| Operation      | Status       | Notes                              | AWS Docs                                                                          |
| -------------- | ------------ | ---------------------------------- | --------------------------------------------------------------------------------- |
| `CreateWebACL` | ✅ Supported | Returns Summary with Id/LockToken  | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_CreateWebACL.html) |
| `GetWebACL`    | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_GetWebACL.html)    |
| `ListWebACLs`  | ✅ Supported |                                    | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_ListWebACLs.html)  |
| `DeleteWebACL` | ✅ Supported | LockToken accepted but not checked | [docs](https://docs.aws.amazon.com/waf/latest/APIReference/API_DeleteWebACL.html) |

<!-- END overcast:capabilities -->
