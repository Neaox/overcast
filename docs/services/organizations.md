# Organizations — AWS Organizations

> AWS docs: https://docs.aws.amazon.com/organizations/latest/APIReference/Welcome.html

This is a stub implementation suitable for unblocking CDK bootstrap calls and similar operations that require a minimal Organizations API response.

## Summary

Organizations provides only the `DescribeOrganization` endpoint, which returns a hardcoded organization stub with ID `o-overcast`.

## Behavior Notes

- Returns a hardcoded stub organization with ID `o-overcast` and master account ID `000000000000`.
- All other Organizations operations are unsupported and return 501 Not Implemented.
- This implementation is designed for compatibility with CDK bootstrap operations that probe for `DescribeOrganization` availability.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category   | 🧊 Inert |
| ---------- | -------- |
| Operations | 1        |

---

## Endpoints

### Operations

| Operation              | Status   | Notes | AWS Docs                                                                                            |
| ---------------------- | -------- | ----- | --------------------------------------------------------------------------------------------------- |
| `DescribeOrganization` | 🧊 Inert |       | [docs](https://docs.aws.amazon.com/organizations/latest/APIReference/API_DescribeOrganization.html) |

<!-- END overcast:capabilities -->
