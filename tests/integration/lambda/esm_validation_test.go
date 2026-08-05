package lambda_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestCreateEventSourceMapping_batchSizeDefaultsBySource(t *testing.T) {
	tests := []struct {
		name           string
		eventSourceARN string
		wantBatchSize  float64
	}{
		{name: "SQS", eventSourceARN: sqsARN("default-batch"), wantBatchSize: 10},
		{name: "DynamoDB stream", eventSourceARN: dynamoDBStreamARN("DefaultBatch"), wantBatchSize: 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a Lambda function and an event source with a service-specific default.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "esm-default-batch")

			// When: the mapping is created without BatchSize.
			mapping := createESM(t, srv, map[string]any{
				"FunctionName":   "esm-default-batch",
				"EventSourceArn": tc.eventSourceARN,
			})

			// Then: Lambda applies the event source's AWS default.
			if got := mapping["BatchSize"]; got != tc.wantBatchSize {
				t.Fatalf("BatchSize = %v, want %v", got, tc.wantBatchSize)
			}
		})
	}
}

func TestCreateEventSourceMapping_batchSizeModelBounds(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
		want      string
	}{
		{
			name:      "zero",
			batchSize: 0,
			want:      "1 validation error detected: Value '0' at 'batchSize' failed to satisfy constraint: Member must have value greater than or equal to 1",
		},
		{
			name:      "negative",
			batchSize: -1,
			want:      "1 validation error detected: Value '-1' at 'batchSize' failed to satisfy constraint: Member must have value greater than or equal to 1",
		},
		{
			name:      "above modeled maximum",
			batchSize: 10001,
			want:      "1 validation error detected: Value '10001' at 'batchSize' failed to satisfy constraint: Member must have value less than or equal to 10000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a Lambda function and a valid SQS event source.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "esm-create-bounds")

			// When: CreateEventSourceMapping receives an out-of-range BatchSize.
			resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
				"FunctionName":   "esm-create-bounds",
				"EventSourceArn": sqsARN("create-bounds"),
				"BatchSize":      tc.batchSize,
			})

			// Then: the AWS-shaped modeled validation error is returned.
			assertLambdaValidationMessage(t, resp, tc.want)
		})
	}
}

func TestEventSourceMapping_sqsBatchSizeRequiresBatchWindow(t *testing.T) {
	const want = "Maximum batch window in seconds must be greater than 0 if maximum batch size is greater than 10"

	t.Run("create", func(t *testing.T) {
		// Given: a Lambda function and a standard SQS queue.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-create-window")

		// When: the mapping requests a batch above 10 with the default zero-second window.
		resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
			"FunctionName":   "esm-create-window",
			"EventSourceArn": sqsARN("create-window"),
			"BatchSize":      11,
		})

		// Then: Lambda rejects the source-specific invalid combination.
		assertLambdaValidationMessage(t, resp, want)
	})

	t.Run("update", func(t *testing.T) {
		// Given: an SQS mapping with the default zero-second batching window.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-update-window")
		mapping := createESM(t, srv, map[string]any{
			"FunctionName":   "esm-update-window",
			"EventSourceArn": sqsARN("update-window"),
		})

		// When: the mapping is updated to a batch above 10 without increasing the window.
		resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+mapping["UUID"].(string), map[string]any{"BatchSize": 11})

		// Then: the same Lambda service validation is returned.
		assertLambdaValidationMessage(t, resp, want)
	})
}

func TestEventSourceMapping_dynamoDBBatchSizeRequiresBatchWindow(t *testing.T) {
	const want = "Maximum batch window in seconds must be greater than 0 if maximum batch size is greater than 10"

	t.Run("create", func(t *testing.T) {
		// Given: a Lambda function and a DynamoDB stream.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-ddb-create-window")
		request := map[string]any{
			"FunctionName":     "esm-ddb-create-window",
			"EventSourceArn":   dynamoDBStreamARN("CreateWindow"),
			"StartingPosition": "LATEST",
			"BatchSize":        11,
		}

		// When: an explicit batch above 10 keeps the default zero-second window.
		resp := doJSON(t, http.MethodPost, esmURL(srv), request)

		// Then: Lambda rejects the invalid stream configuration.
		assertLambdaValidationMessage(t, resp, want)

		// When: the same explicit batch is paired with a one-second window.
		request["MaximumBatchingWindowInSeconds"] = 1
		mapping := createESM(t, srv, request)

		// Then: the documented stream configuration is accepted.
		if got := mapping["BatchSize"]; got != float64(11) {
			t.Fatalf("BatchSize = %v, want 11", got)
		}
	})

	t.Run("update", func(t *testing.T) {
		// Given: a DynamoDB mapping created with its valid 100-record default.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-ddb-update-window")
		mapping := createESM(t, srv, map[string]any{
			"FunctionName":     "esm-ddb-update-window",
			"EventSourceArn":   dynamoDBStreamARN("UpdateWindow"),
			"StartingPosition": "LATEST",
		})
		id := mapping["UUID"].(string)

		// When: an update explicitly sets a batch above 10 with a zero-second window.
		resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{"BatchSize": 11})

		// Then: Lambda rejects the invalid stream configuration.
		assertLambdaValidationMessage(t, resp, want)

		// When: the update includes the required window.
		resp = doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
			"BatchSize": 11, "MaximumBatchingWindowInSeconds": 1,
		})

		// Then: Lambda accepts and returns both values.
		helpers.AssertStatus(t, resp, http.StatusAccepted)
		var updated map[string]any
		decodeJSON(t, resp, &updated)
		if updated["BatchSize"] != float64(11) || updated["MaximumBatchingWindowInSeconds"] != float64(1) {
			t.Fatalf("updated mapping = %#v, want BatchSize 11 and window 1", updated)
		}
	})
}

func TestEventSourceMapping_sqsFIFOBatchSizeMaximum(t *testing.T) {
	const want = "Batch size cannot be greater than 10 for an SQS FIFO queue."

	t.Run("create", func(t *testing.T) {
		// Given: a Lambda function and an SQS FIFO queue.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-create-fifo")

		// When: the mapping exceeds the documented FIFO batch maximum.
		resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
			"FunctionName":                   "esm-create-fifo",
			"EventSourceArn":                 sqsARN("create.fifo"),
			"BatchSize":                      11,
			"MaximumBatchingWindowInSeconds": 1,
		})

		// Then: Lambda rejects the FIFO-specific bound.
		assertLambdaValidationMessage(t, resp, want)
	})

	t.Run("update", func(t *testing.T) {
		// Given: an SQS FIFO mapping.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-update-fifo")
		mapping := createESM(t, srv, map[string]any{
			"FunctionName":   "esm-update-fifo",
			"EventSourceArn": sqsARN("update.fifo"),
		})

		// When: an update exceeds the FIFO batch maximum.
		resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+mapping["UUID"].(string), map[string]any{
			"BatchSize":                      11,
			"MaximumBatchingWindowInSeconds": 1,
		})

		// Then: Lambda rejects the FIFO-specific bound.
		assertLambdaValidationMessage(t, resp, want)
	})
}

func TestUpdateEventSourceMapping_batchSizeModelBounds(t *testing.T) {
	tests := []struct {
		name      string
		batchSize int
		want      string
	}{
		{
			name:      "zero",
			batchSize: 0,
			want:      "1 validation error detected: Value '0' at 'batchSize' failed to satisfy constraint: Member must have value greater than or equal to 1",
		},
		{
			name:      "negative",
			batchSize: -1,
			want:      "1 validation error detected: Value '-1' at 'batchSize' failed to satisfy constraint: Member must have value greater than or equal to 1",
		},
		{
			name:      "above modeled maximum",
			batchSize: 10001,
			want:      "1 validation error detected: Value '10001' at 'batchSize' failed to satisfy constraint: Member must have value less than or equal to 10000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a valid DynamoDB Streams event source mapping.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "esm-update-bounds")
			mapping := createESM(t, srv, map[string]any{
				"FunctionName":     "esm-update-bounds",
				"EventSourceArn":   dynamoDBStreamARN("UpdateBounds"),
				"StartingPosition": "LATEST",
			})

			// When: UpdateEventSourceMapping receives an out-of-range BatchSize.
			resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+mapping["UUID"].(string), map[string]any{"BatchSize": tc.batchSize})

			// Then: the AWS-shaped modeled validation error is returned.
			assertLambdaValidationMessage(t, resp, tc.want)

			// And: the rejected update did not mutate the mapping.
			getResp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+mapping["UUID"].(string), nil)
			var persisted map[string]any
			decodeJSON(t, getResp, &persisted)
			if persisted["BatchSize"] != float64(100) {
				t.Fatalf("persisted BatchSize = %v, want 100", persisted["BatchSize"])
			}
		})
	}
}

func TestEventSourceMapping_batchingWindowModelBounds(t *testing.T) {
	tests := []struct {
		name   string
		window int
		want   string
	}{
		{
			name:   "negative",
			window: -1,
			want:   "1 validation error detected: Value '-1' at 'maximumBatchingWindowInSeconds' failed to satisfy constraint: Member must have value greater than or equal to 0",
		},
		{
			name:   "above modeled maximum",
			window: 301,
			want:   "1 validation error detected: Value '301' at 'maximumBatchingWindowInSeconds' failed to satisfy constraint: Member must have value less than or equal to 300",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("create", func(t *testing.T) {
				// Given: a function and valid SQS event source.
				srv := helpers.NewTestServer(t)
				createFunction(t, srv, "esm-create-window-bounds")

				// When: create receives an out-of-range batching window.
				resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
					"FunctionName": "esm-create-window-bounds", "EventSourceArn": sqsARN("create-window-bounds"),
					"MaximumBatchingWindowInSeconds": tc.window,
				})

				// Then: the modeled Lambda validation error is returned.
				assertLambdaValidationMessage(t, resp, tc.want)
			})

			t.Run("update", func(t *testing.T) {
				// Given: an existing SQS event source mapping.
				srv := helpers.NewTestServer(t)
				createFunction(t, srv, "esm-update-window-bounds")
				mapping := createESM(t, srv, map[string]any{
					"FunctionName": "esm-update-window-bounds", "EventSourceArn": sqsARN("update-window-bounds"),
				})
				id := mapping["UUID"].(string)

				// When: update receives an out-of-range batching window.
				resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
					"MaximumBatchingWindowInSeconds": tc.window,
				})

				// Then: the modeled error is returned without mutating the mapping.
				assertLambdaValidationMessage(t, resp, tc.want)
				persisted := getESM(t, srv, id)
				if persisted["MaximumBatchingWindowInSeconds"] != float64(0) {
					t.Fatalf("persisted window = %v, want 0", persisted["MaximumBatchingWindowInSeconds"])
				}
			})
		})
	}
}

func TestEventSourceMapping_batchingWindowModelBoundaries(t *testing.T) {
	// Given: a function and valid SQS event source.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-window-boundaries")

	// When: create uses the minimum batching window.
	mapping := createESM(t, srv, map[string]any{
		"FunctionName": "esm-window-boundaries", "EventSourceArn": sqsARN("window-boundaries"),
		"MaximumBatchingWindowInSeconds": 0,
	})

	// Then: update accepts the modeled maximum too.
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+mapping["UUID"].(string), map[string]any{
		"MaximumBatchingWindowInSeconds": 300,
	})
	helpers.AssertStatus(t, resp, http.StatusAccepted)
	var updated map[string]any
	decodeJSON(t, resp, &updated)
	if updated["MaximumBatchingWindowInSeconds"] != float64(300) {
		t.Fatalf("MaximumBatchingWindowInSeconds = %v, want 300", updated["MaximumBatchingWindowInSeconds"])
	}
}

func TestUpdateEventSourceMapping_explicitBatchRetainsWindowRequirement(t *testing.T) {
	tests := []struct {
		name           string
		eventSourceARN string
	}{
		{name: "SQS", eventSourceARN: sqsARN("explicit-batch")},
		{name: "DynamoDB stream", eventSourceARN: dynamoDBStreamARN("ExplicitBatch")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a mapping with an explicit batch above 10 and its required window.
			srv := helpers.NewTestServer(t)
			createFunction(t, srv, "esm-explicit-batch")
			request := map[string]any{
				"FunctionName": "esm-explicit-batch", "EventSourceArn": tc.eventSourceARN,
				"BatchSize": 11, "MaximumBatchingWindowInSeconds": 1,
			}
			if tc.name == "DynamoDB stream" {
				request["StartingPosition"] = "LATEST"
			}
			mapping := createESM(t, srv, request)
			id := mapping["UUID"].(string)
			if _, leaked := mapping["_overcastBatchSizeExplicit"]; leaked {
				t.Fatal("internal explicit-batch marker leaked into create response")
			}
			raw, found, err := srv.Store.Get(context.Background(), "lambda:esm", "us-east-1/"+id)
			if err != nil || !found || !strings.Contains(raw, `"_overcastBatchSizeExplicit":true`) {
				t.Fatalf("persisted explicit-batch marker = found %v, err %v, raw %q", found, err, raw)
			}

			// When: a later update clears only the batching window.
			resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
				"MaximumBatchingWindowInSeconds": 0,
			})

			// Then: Lambda retains the explicit-batch invariant and leaves state unchanged.
			assertLambdaValidationMessage(t, resp,
				"Maximum batch window in seconds must be greater than 0 if maximum batch size is greater than 10")
			persisted := getESM(t, srv, id)
			if persisted["MaximumBatchingWindowInSeconds"] != float64(1) {
				t.Fatalf("persisted window = %v, want 1", persisted["MaximumBatchingWindowInSeconds"])
			}
		})
	}
}

func TestUpdateEventSourceMapping_legacyExplicitBatchRetainsWindowRequirement(t *testing.T) {
	// Given: a pre-marker persisted SQS mapping whose nondefault batch was explicit.
	srv := helpers.NewTestServer(t)
	const id = "f540b512-a8c3-4b69-8f4a-cbf756f97078"
	legacy := `{"UUID":"` + id + `","FunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:legacy","EventSourceArn":"arn:aws:sqs:us-east-1:000000000000:legacy","State":"Enabled","BatchSize":20,"MaximumBatchingWindowInSeconds":1}`
	if err := srv.Store.Set(context.Background(), "lambda:esm", "us-east-1/"+id, legacy); err != nil {
		t.Fatalf("seed legacy event source mapping: %v", err)
	}

	// When: a later update clears only the batching window.
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
		"MaximumBatchingWindowInSeconds": 0,
	})

	// Then: migration inference preserves the explicit nondefault-batch invariant.
	assertLambdaValidationMessage(t, resp,
		"Maximum batch window in seconds must be greater than 0 if maximum batch size is greater than 10")
	persisted := getESM(t, srv, id)
	if persisted["MaximumBatchingWindowInSeconds"] != float64(1) {
		t.Fatalf("persisted window = %v, want 1", persisted["MaximumBatchingWindowInSeconds"])
	}
}

func TestUpdateEventSourceMapping_omittedStreamBatchAllowsDefaultWindow(t *testing.T) {
	// Given: DynamoDB mapping with the omitted BatchSize default of 100.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "esm-default-stream-window")
	mapping := createESM(t, srv, map[string]any{
		"FunctionName": "esm-default-stream-window", "EventSourceArn": dynamoDBStreamARN("DefaultWindow"),
		"StartingPosition": "LATEST",
	})

	// When: an update explicitly retains the default zero-second window.
	resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+mapping["UUID"].(string), map[string]any{
		"MaximumBatchingWindowInSeconds": 0,
	})

	// Then: the AWS default combination remains valid.
	helpers.AssertStatus(t, resp, http.StatusAccepted)
	var updated map[string]any
	decodeJSON(t, resp, &updated)
	if updated["BatchSize"] != float64(100) || updated["MaximumBatchingWindowInSeconds"] != float64(0) {
		t.Fatalf("updated mapping = %#v, want default BatchSize 100 and window 0", updated)
	}
}

func TestEventSourceMapping_sqsFIFORejectsBatchingWindow(t *testing.T) {
	const want = "Maximum batching window is not supported for SQS FIFO queues."

	t.Run("create", func(t *testing.T) {
		// Given: a function and SQS FIFO event source.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-fifo-create-window")

		// When: create requests a nonzero batching window.
		resp := doJSON(t, http.MethodPost, esmURL(srv), map[string]any{
			"FunctionName": "esm-fifo-create-window", "EventSourceArn": sqsARN("fifo-create-window.fifo"),
			"MaximumBatchingWindowInSeconds": 1,
		})

		// Then: Lambda rejects the FIFO-unsupported setting.
		assertLambdaValidationMessage(t, resp, want)
	})

	t.Run("update", func(t *testing.T) {
		// Given: an existing SQS FIFO mapping with its supported zero window.
		srv := helpers.NewTestServer(t)
		createFunction(t, srv, "esm-fifo-update-window")
		mapping := createESM(t, srv, map[string]any{
			"FunctionName": "esm-fifo-update-window", "EventSourceArn": sqsARN("fifo-update-window.fifo"),
		})
		id := mapping["UUID"].(string)

		// When: update requests a nonzero batching window.
		resp := doJSON(t, http.MethodPut, esmURL(srv)+"/"+id, map[string]any{
			"MaximumBatchingWindowInSeconds": 1,
		})

		// Then: Lambda rejects it without mutation.
		assertLambdaValidationMessage(t, resp, want)
		persisted := getESM(t, srv, id)
		if persisted["MaximumBatchingWindowInSeconds"] != float64(0) {
			t.Fatalf("persisted window = %v, want 0", persisted["MaximumBatchingWindowInSeconds"])
		}
	})
}

func getESM(t *testing.T, srv *helpers.TestServer, id string) map[string]any {
	t.Helper()
	resp := doJSON(t, http.MethodGet, esmURL(srv)+"/"+id, nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var mapping map[string]any
	decodeJSON(t, resp, &mapping)
	return mapping
}
