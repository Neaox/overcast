---
title: "CloudWatch — Amazon CloudWatch"
description: "Amazon CloudWatch (monitoring and alarms) uses the Query protocol — form-encoded POST requests with Action and Version=2010-08-01 parameters."
section: "Service Reference"
tags:
  - amazon
  - cloudwatch
  - docs
  - services
---

# CloudWatch — Amazon CloudWatch

> AWS docs: https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/

Amazon CloudWatch (monitoring and alarms) uses the Query protocol — form-encoded
POST requests with `Action` and `Version=2010-08-01` parameters.

---

## Notes

- Query protocol: `POST / HTTP/1.1` with `Action=<Operation>&Version=2010-08-01` in the form body.
- Unrecognized operations return an XML `501 Not Implemented` error response.
- PutMetricData appears in both Alarms and Metrics categories as it supports both use cases.
- Alarm state transitions are tracked but no actual metric evaluation is performed.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category | ✅ Supported |
| -------- | ------------ |
| General  | 12           |

---

## Endpoints

### General

| Operation                 | Status       | Notes                                              | AWS Docs                                                                                                  |
| ------------------------- | ------------ | -------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `DeleteAlarms`            | ✅ Supported | Deletes one or more alarms by name                 | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DeleteAlarms.html)            |
| `DescribeAlarms`          | ✅ Supported | Lists alarms, supports filtering                   | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DescribeAlarms.html)          |
| `DescribeAlarmsForMetric` | ✅ Supported | Lists alarms for a specific metric                 | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_DescribeAlarmsForMetric.html) |
| `GetMetricData`           | ✅ Supported | Returns query-based metric values over time ranges | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricData.html)           |
| `GetMetricStatistics`     | ✅ Supported | Returns aggregated datapoints by period            | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_GetMetricStatistics.html)     |
| `ListMetrics`             | ✅ Supported | Lists available metrics                            | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_ListMetrics.html)             |
| `ListTagsForResource`     | ✅ Supported | Lists tags for an alarm                            | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_ListTagsForResource.html)     |
| `PutMetricAlarm`          | ✅ Supported | Creates or updates an alarm                        | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutMetricAlarm.html)          |
| `PutMetricData`           | ✅ Supported | Publishes metric data points                       | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutMetricData.html)           |
| `SetAlarmState`           | ✅ Supported | Manually sets the state of an alarm                | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_SetAlarmState.html)           |
| `TagResource`             | ✅ Supported | Adds or updates tags on an alarm                   | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_TagResource.html)             |
| `UntagResource`           | ✅ Supported | Removes tags from an alarm                         | [docs](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_UntagResource.html)           |

<!-- END overcast:capabilities -->
