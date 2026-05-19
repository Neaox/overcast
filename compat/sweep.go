package compat

// sweepOrphans lists and deletes "oc-" and "compat-" prefixed resources from
// the emulator. It runs both before and after a compat run to ensure a clean
// state regardless of suite teardown bugs or interrupted runs.

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// sweepOrphans deletes orphaned resources matching "oc-" or "compat-" prefixes.
func sweepOrphans(ctx context.Context, endpoint string, w io.Writer) {
	client := &http.Client{Timeout: 10 * time.Second}

	type sweepFn func(ctx context.Context, client *http.Client, endpoint string) (int, error)
	services := []struct {
		name string
		fn   sweepFn
	}{
		{"s3", sweepS3Buckets},
		{"sqs", sweepSQSQueues},
		{"dynamodb", sweepDynamoTables},
		{"sns", sweepSNSTopics},
		{"kinesis", sweepKinesisStreams},
		{"lambda", sweepLambdaFunctions},
		{"logs", sweepLogGroups},
		{"ecs", sweepECSClusters},
		{"rds", sweepRDSInstances},
		{"elasticache", sweepElastiCacheClusters},
	}

	total := 0
	for _, svc := range services {
		n, err := svc.fn(ctx, client, endpoint)
		if err != nil {
			// 501 or unreachable — skip silently.
			continue
		}
		total += n
	}
	if total > 0 {
		fmt.Fprintf(w, "compat: sweep: deleted %d orphaned resource(s)\n", total)
	}
}

func isOrphan(name string) bool {
	return strings.HasPrefix(name, "oc-") || strings.HasPrefix(name, "compat-")
}

func sendJSON(client *http.Client, ctx context.Context, endpoint, target string, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	return client.Do(req)
}

func sendQuery(client *http.Client, ctx context.Context, endpoint string, params map[string]string) (*http.Response, error) {
	vals := make([]string, 0, len(params)*2)
	for k, v := range params {
		vals = append(vals, k+"="+v)
	}
	body := strings.Join(vals, "&")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return client.Do(req)
}

// --- S3 ---

type s3ListBucketsResult struct {
	XMLName xml.Name   `xml:"ListAllMyBucketsResult"`
	Buckets []s3Bucket `xml:"Buckets>Bucket"`
}

type s3Bucket struct {
	Name string `xml:"Name"`
}

type listObjectsResult struct {
	XMLName  xml.Name   `xml:"ListBucketResult"`
	Contents []s3Object `xml:"Contents"`
	KeyCount int        `xml:"KeyCount"`
}

type s3Object struct {
	Key string `xml:"Key"`
}

func sweepS3Buckets(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result s3ListBucketsResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, b := range result.Buckets {
		if !isOrphan(b.Name) {
			continue
		}
		// Empty the bucket first: list and delete objects.
		bucketURL := endpoint + "/" + b.Name
		emptyReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, bucketURL, nil)
		emptyResp, err := client.Do(emptyReq)
		if err == nil {
			var objects listObjectsResult
			if xml.NewDecoder(emptyResp.Body).Decode(&objects) == nil {
				for _, obj := range objects.Contents {
					delReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, bucketURL+"/"+obj.Key, nil)
					delResp, err := client.Do(delReq)
					if err == nil {
						delResp.Body.Close()
					}
				}
			}
			emptyResp.Body.Close()
		}
		// Delete the bucket
		delReq, _ := http.NewRequestWithContext(ctx, http.MethodDelete, bucketURL, nil)
		delResp, err := client.Do(delReq)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- SQS ---

type sqsListQueuesResponse struct {
	QueueUrls []string `json:"QueueUrls"`
}

func sweepSQSQueues(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	body := `{"QueueNamePrefix":"oc-"}`
	resp, err := sendJSON(client, ctx, endpoint, "AmazonSQS.ListQueues", body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result sqsListQueuesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, qURL := range result.QueueUrls {
		parts := strings.Split(strings.TrimRight(qURL, "/"), "/")
		name := parts[len(parts)-1]
		if !isOrphan(name) {
			continue
		}
		delBody := fmt.Sprintf(`{"QueueUrl":"%s"}`, qURL)
		delResp, err := sendJSON(client, ctx, endpoint, "AmazonSQS.DeleteQueue", delBody)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- DynamoDB ---

type ddbListTablesResponse struct {
	TableNames []string `json:"TableNames"`
}

func sweepDynamoTables(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendJSON(client, ctx, endpoint, "DynamoDB_20120810.ListTables", "{}")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result ddbListTablesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, name := range result.TableNames {
		if !isOrphan(name) {
			continue
		}
		delBody := fmt.Sprintf(`{"TableName":"%s"}`, name)
		delResp, err := sendJSON(client, ctx, endpoint, "DynamoDB_20120810.DeleteTable", delBody)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- SNS ---

type snsListTopicsResult struct {
	XMLName xml.Name         `xml:"ListTopicsResponse"`
	Topics  []snsTopicMember `xml:"ListTopicsResult>Topics>member"`
}

type snsTopicMember struct {
	TopicArn string `xml:"TopicArn"`
}

func sweepSNSTopics(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendQuery(client, ctx, endpoint, map[string]string{
		"Action":  "ListTopics",
		"Version": "2010-03-31",
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result snsListTopicsResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, t := range result.Topics {
		parts := strings.Split(t.TopicArn, ":")
		name := parts[len(parts)-1]
		if !isOrphan(name) {
			continue
		}
		delResp, _ := sendQuery(client, ctx, endpoint, map[string]string{
			"Action":   "DeleteTopic",
			"TopicArn": t.TopicArn,
			"Version":  "2010-03-31",
		})
		if delResp != nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- Kinesis ---

type kinesisListStreamsResponse struct {
	StreamNames []string `json:"StreamNames"`
}

func sweepKinesisStreams(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendJSON(client, ctx, endpoint, "Kinesis_20131202.ListStreams", "{}")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result kinesisListStreamsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, name := range result.StreamNames {
		if !isOrphan(name) {
			continue
		}
		delBody := fmt.Sprintf(`{"StreamName":"%s"}`, name)
		delResp, err := sendJSON(client, ctx, endpoint, "Kinesis_20131202.DeleteStream", delBody)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- Lambda ---

type lambdaListFunctionsResponse struct {
	Functions []lambdaFunction `json:"Functions"`
}

type lambdaFunction struct {
	FunctionName string `json:"FunctionName"`
}

func sweepLambdaFunctions(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendJSON(client, ctx, endpoint, "AWSLambda_20150331.ListFunctions", "{}")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result lambdaListFunctionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, fn := range result.Functions {
		if !isOrphan(fn.FunctionName) {
			continue
		}
		delBody := fmt.Sprintf(`{"FunctionName":"%s"}`, fn.FunctionName)
		delResp, err := sendJSON(client, ctx, endpoint, "AWSLambda_20150331.DeleteFunction", delBody)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- CloudWatch Logs ---

type logsDescribeLogGroupsResponse struct {
	LogGroups []logsLogGroup `json:"logGroups"`
}

type logsLogGroup struct {
	LogGroupName string `json:"logGroupName"`
}

func sweepLogGroups(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	deleted := 0
	prefixes := []string{"/aws/lambda/oc-", "/aws/lambda/compat-"}
	for _, prefix := range prefixes {
		body := fmt.Sprintf(`{"logGroupNamePrefix":"%s"}`, prefix)
		resp, err := sendJSON(client, ctx, endpoint, "Logs_20140328.DescribeLogGroups", body)
		if err != nil {
			continue
		}
		var result logsDescribeLogGroupsResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		for _, group := range result.LogGroups {
			delBody := fmt.Sprintf(`{"logGroupName":"%s"}`, group.LogGroupName)
			delResp, err := sendJSON(client, ctx, endpoint, "Logs_20140328.DeleteLogGroup", delBody)
			if err == nil {
				delResp.Body.Close()
				deleted++
			}
		}
	}
	return deleted, nil
}

// --- ECS ---

type ecsListClustersResponse struct {
	ClusterArns []string `json:"clusterArns"`
}

type ecsListServicesResponse struct {
	ServiceArns []string `json:"serviceArns"`
}

func sweepECSClusters(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendJSON(client, ctx, endpoint, "AmazonEC2ContainerServiceV20141113.ListClusters", "{}")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result ecsListClustersResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, arn := range result.ClusterArns {
		parts := strings.Split(arn, "/")
		name := parts[len(parts)-1]
		if !isOrphan(name) {
			continue
		}
		// Delete services in cluster first
		svcBody := fmt.Sprintf(`{"cluster":"%s"}`, name)
		svcResp, err := sendJSON(client, ctx, endpoint, "AmazonEC2ContainerServiceV20141113.ListServices", svcBody)
		if err == nil {
			var svcResult ecsListServicesResponse
			if json.NewDecoder(svcResp.Body).Decode(&svcResult) == nil {
				for _, svcArn := range svcResult.ServiceArns {
					svcParts := strings.Split(svcArn, "/")
					svcName := svcParts[len(svcParts)-1]
					// Set desiredCount to 0
					updBody := fmt.Sprintf(`{"cluster":"%s","service":"%s","desiredCount":0}`, name, svcName)
					updResp, _ := sendJSON(client, ctx, endpoint, "AmazonEC2ContainerServiceV20141113.UpdateService", updBody)
					if updResp != nil {
						updResp.Body.Close()
					}
					// Delete service
					delSvcBody := fmt.Sprintf(`{"cluster":"%s","service":"%s"}`, name, svcName)
					delSvcResp, _ := sendJSON(client, ctx, endpoint, "AmazonEC2ContainerServiceV20141113.DeleteService", delSvcBody)
					if delSvcResp != nil {
						delSvcResp.Body.Close()
					}
				}
			}
			svcResp.Body.Close()
		}
		// Delete cluster
		delBody := fmt.Sprintf(`{"cluster":"%s"}`, name)
		delResp, err := sendJSON(client, ctx, endpoint, "AmazonEC2ContainerServiceV20141113.DeleteCluster", delBody)
		if err == nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- RDS ---

type rdsDescribeDBInstancesResult struct {
	XMLName     xml.Name        `xml:"DescribeDBInstancesResponse"`
	DBInstances []rdsDBInstance `xml:"DescribeDBInstancesResult>DBInstances>DBInstance"`
}

type rdsDBInstance struct {
	DBInstanceIdentifier string `xml:"DBInstanceIdentifier"`
}

func sweepRDSInstances(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendQuery(client, ctx, endpoint, map[string]string{
		"Action":  "DescribeDBInstances",
		"Version": "2014-10-31",
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result rdsDescribeDBInstancesResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, db := range result.DBInstances {
		if !isOrphan(db.DBInstanceIdentifier) {
			continue
		}
		delResp, _ := sendQuery(client, ctx, endpoint, map[string]string{
			"Action":               "DeleteDBInstance",
			"DBInstanceIdentifier": db.DBInstanceIdentifier,
			"SkipFinalSnapshot":    "true",
			"Version":              "2014-10-31",
		})
		if delResp != nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}

// --- ElastiCache ---

type ecDescribeCacheClustersResult struct {
	XMLName       xml.Name         `xml:"DescribeCacheClustersResponse"`
	CacheClusters []ecCacheCluster `xml:"DescribeCacheClustersResult>CacheClusters>CacheCluster"`
}

type ecCacheCluster struct {
	CacheClusterId string `xml:"CacheClusterId"`
}

func sweepElastiCacheClusters(ctx context.Context, client *http.Client, endpoint string) (int, error) {
	resp, err := sendQuery(client, ctx, endpoint, map[string]string{
		"Action":  "DescribeCacheClusters",
		"Version": "2015-02-02",
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil
	}
	var result ecDescribeCacheClustersResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	deleted := 0
	for _, c := range result.CacheClusters {
		if !isOrphan(c.CacheClusterId) {
			continue
		}
		delResp, _ := sendQuery(client, ctx, endpoint, map[string]string{
			"Action":         "DeleteCacheCluster",
			"CacheClusterId": c.CacheClusterId,
			"Version":        "2015-02-02",
		})
		if delResp != nil {
			delResp.Body.Close()
			deleted++
		}
	}
	return deleted, nil
}
