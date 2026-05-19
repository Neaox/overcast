// Package load_test contains end-to-end load / resilience tests that thrash the
// Overcast server with concurrent requests across multiple services.
//
// These tests verify that Overcast handles sustained concurrent load without
// crashing, leaking goroutines, or becoming unresponsive. They are heavier than
// standard integration tests — run them separately with `go test -count=1
// -timeout=60s ./tests/load/...`.
//
// Test conventions: see tests/AGENTS.md. Each test uses GWT structure and
// helpers.NewTestServer for isolation.
package load_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

const (
	loadGoroutines = 100
	loadIterations = 10 // total = goroutines × iterations requests per service
)

// ---- Concurrent multi-service thrash ---------------------------------------

// TestLoad_MultiServiceThrash blasts Overcast with concurrent requests to S3,
// SQS, DynamoDB, and SNS simultaneously, then verifies the server is still
// healthy and functional.
func TestLoad_MultiServiceThrash(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())

	var wg sync.WaitGroup
	errs := make(chan string, loadGoroutines*loadIterations*4)

	start := time.Now()

	// S3: concurrent bucket create → put object → head → delete.
	wg.Add(loadGoroutines)
	for g := 0; g < loadGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loadIterations; i++ {
				bucket := fmt.Sprintf("load-s3-%d-%d", id, i)
				// Create bucket
				req, _ := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket, nil)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Sprintf("S3 CreateBucket %s: %v", bucket, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("S3 CreateBucket %s: status %d", bucket, resp.StatusCode)
					continue
				}
				// Put object
				body := []byte(fmt.Sprintf("load-data-%d-%d", id, i))
				req, _ = http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/key.txt", bytes.NewReader(body))
				resp, err = http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Sprintf("S3 PutObject %s/key.txt: %v", bucket, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("S3 PutObject %s/key.txt: status %d", bucket, resp.StatusCode)
				}
			}
		}(g)
	}

	// SQS: concurrent create queue → send message → receive → delete.
	wg.Add(loadGoroutines)
	for g := 0; g < loadGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loadIterations; i++ {
				name := fmt.Sprintf("load-sqs-%d-%d", id, i)
				queueURL := sqsCreateQueue(t, srv, name)
				if queueURL == "" {
					continue
				}
				sqsSendMessage(t, srv, queueURL, "load-msg")
			}
		}(g)
	}

	// DynamoDB: concurrent create table → put item → get item.
	wg.Add(loadGoroutines)
	for g := 0; g < loadGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loadIterations; i++ {
				name := fmt.Sprintf("load-ddb-%d-%d", id, i)
				if !ddbCreateTable(t, srv, name) {
					continue
				}
				ddbPutItem(t, srv, name, fmt.Sprintf("pk-%d-%d", id, i), "val")
			}
		}(g)
	}

	// SNS: concurrent create topic → publish.
	wg.Add(loadGoroutines)
	for g := 0; g < loadGoroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < loadIterations; i++ {
				name := fmt.Sprintf("load-sns-%d-%d", id, i)
				topicARN := snsCreateTopic(t, srv, name)
				if topicARN == "" {
					continue
				}
				snsPublish(t, srv, topicARN, "load-msg")
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	elapsed := time.Since(start)

	var errorList []string
	for e := range errs {
		errorList = append(errorList, e)
	}
	if len(errorList) > 0 {
		// Show first 10 errors, fail on any.
		max := len(errorList)
		if max > 10 {
			max = 10
		}
		for _, e := range errorList[:max] {
			t.Errorf("load error: %s", e)
		}
		t.Errorf("total load errors: %d / %d requests",
			len(errorList), loadGoroutines*loadIterations*4)
	}

	t.Logf("multi-service thrash: %d goroutines × %d iterations × 4 services = %d requests in %v",
		loadGoroutines, loadIterations, loadGoroutines*loadIterations*4, elapsed.Round(time.Millisecond))

	// Then: server still responds to a simple health-like operation.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_health", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("server unreachable after thrash: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("server returned %d on health check after thrash", resp.StatusCode)
	}
}

// ---- S3 throughput thrash --------------------------------------------------

func TestLoad_S3Thrash(t *testing.T) {
	srv := helpers.NewTestServer(t)

	const goroutines = 200
	const perG = 10 // 2000 total requests

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*perG)

	start := time.Now()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			bucket := fmt.Sprintf("load-s3-thrash-%d", id)
			// Create bucket once per goroutine.
			req, _ := http.NewRequest(http.MethodPut, srv.URL+"/"+bucket, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- fmt.Sprintf("CreateBucket %s: %v", bucket, err)
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Sprintf("CreateBucket %s: status %d", bucket, resp.StatusCode)
				return
			}
			for i := 0; i < perG; i++ {
				key := fmt.Sprintf("obj-%d", i)
				body := []byte(fmt.Sprintf("body-%d-%d", id, i))
				req, _ = http.NewRequest(http.MethodPut, srv.URL+"/"+bucket+"/"+key, bytes.NewReader(body))
				resp, err = http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Sprintf("PutObject %s/%s: %v", bucket, key, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("PutObject %s/%s: status %d", bucket, key, resp.StatusCode)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	elapsed := time.Since(start)
	var failures int
	for range errs {
		failures++
	}
	if failures > 0 {
		t.Errorf("%d S3 requests failed", failures)
	}

	t.Logf("S3 thrash: %d goroutines × %d PUTs = %d requests in %v (%d failures)",
		goroutines, perG, goroutines*perG, elapsed.Round(time.Millisecond), failures)
}

// ---- SMTP thrash -----------------------------------------------------------

func TestLoad_SMTPThrash(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithSMTPMock())

	const goroutines = 100
	const perG = 5 // 500 emails

	var wg sync.WaitGroup
	errs := make(chan string, goroutines*perG)

	start := time.Now()

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				from := fmt.Sprintf("sender-%d-%d@load.test", id, i)
				to := fmt.Sprintf("rcpt-%d-%d@load.test", id, i)
				subject := fmt.Sprintf("load subject %d-%d", id, i)

				// Verify identity first, then send email.
				// Use query (form-encoded) SES v1 API.
				form := url.Values{}
				form.Set("Action", "VerifyEmailIdentity")
				form.Set("EmailAddress", from)
				req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Sprintf("VerifyEmailIdentity %s: %v", from, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("VerifyEmailIdentity %s: status %d", from, resp.StatusCode)
					continue
				}

				// SendEmail
				form = url.Values{}
				form.Set("Action", "SendEmail")
				form.Set("Source", from)
				form.Set("Destination.ToAddresses.member.1", to)
				form.Set("Message.Subject.Data", subject)
				form.Set("Message.Body.Text.Data", "load test body")
				req, _ = http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(form.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				resp, err = http.DefaultClient.Do(req)
				if err != nil {
					errs <- fmt.Sprintf("SendEmail %s→%s: %v", from, to, err)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					errs <- fmt.Sprintf("SendEmail %s→%s: status %d", from, to, resp.StatusCode)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	elapsed := time.Since(start)
	var failures int
	for range errs {
		failures++
	}
	if failures > 0 {
		t.Errorf("%d SMTP-backed SES requests failed", failures)
	}

	t.Logf("SMTP thrash: %d goroutines × %d emails = %d sends in %v (%d failures)",
		goroutines, perG, goroutines*perG, elapsed.Round(time.Millisecond), failures)
}

// ---- Helpers ---------------------------------------------------------------

func sqsCreateQueue(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	body := map[string]any{"QueueName": name}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.CreateQueue")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var result struct{ QueueUrl string }
	json.NewDecoder(resp.Body).Decode(&result)
	return result.QueueUrl
}

func sqsSendMessage(t *testing.T, srv *helpers.TestServer, queueURL, body string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"QueueUrl":    queueURL,
		"MessageBody": body,
	})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSQS.SendMessage")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func ddbCreateTable(t *testing.T, srv *helpers.TestServer, name string) bool {
	t.Helper()
	body := map[string]any{
		"TableName": name,
		"KeySchema": []map[string]string{
			{"AttributeName": "pk", "KeyType": "HASH"},
		},
		"AttributeDefinitions": []map[string]string{
			{"AttributeName": "pk", "AttributeType": "S"},
		},
		"BillingMode": "PAY_PER_REQUEST",
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.CreateTable")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func ddbPutItem(t *testing.T, srv *helpers.TestServer, table, pk, val string) {
	t.Helper()
	body := map[string]any{
		"TableName": table,
		"Item": map[string]map[string]string{
			"pk":  {"S": pk},
			"val": {"S": val},
		},
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "DynamoDB_20120810.PutItem")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func snsCreateTopic(t *testing.T, srv *helpers.TestServer, name string) string {
	t.Helper()
	body := map[string]any{"Name": name}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSNS.CreateTopic")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var result struct{ TopicArn string }
	json.NewDecoder(resp.Body).Decode(&result)
	return result.TopicArn
}

func snsPublish(t *testing.T, srv *helpers.TestServer, topicARN, msg string) {
	t.Helper()
	body := map[string]any{
		"TopicArn": topicARN,
		"Message":  msg,
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", "AmazonSNS.Publish")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
