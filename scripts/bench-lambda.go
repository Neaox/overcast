//go:build ignore

// Script: bench-lambda
// Measures Lambda cold-start and warm-invoke latency against a RUNNING
// Overcast instance (it does not spawn one), via the direct Invoke API and,
// optionally, an API Gateway v1 proxy route in front of the same function.
//
// Method (docs/plans/lambda-cold-start.md Phase 0):
//   - One throwaway function per runtime (inline hello-world zip), deleted on
//     exit along with the API Gateway API.
//   - A cold sample = bump an env var (identity change retires the warm set),
//     wait for Active, settle, then time one invoke. Includes container
//     create/copy/start and INIT.
//   - Warm samples = sequential invokes against the now-warm container.
//   - Paced deliberately: sequential requests only, with a cooldown between
//     cold rounds, so the Docker daemon is never slammed.
//
// Usage:
//
//	go run ./scripts/bench-lambda.go                         # defaults
//	go run ./scripts/bench-lambda.go -endpoint http://host.docker.internal:4566
//	go run ./scripts/bench-lambda.go -cold 5 -warm 20 -runtime nodejs22.x
//	go run ./scripts/bench-lambda.go -apigw=false            # skip the API GW round
//
// Output: per-runtime table with cold p50/p95/max, warm p50/p95, and API GW
// warm p50/p95 (gateway overhead = apigw-warm minus warm). Conditions to
// record with any published numbers: host OS, Docker backend, image cache
// state, store backend, and the overcast version.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 180 * time.Second}

func main() {
	endpoint := flag.String("endpoint", "http://localhost:4566", "Overcast endpoint")
	coldN := flag.Int("cold", 5, "cold-start samples per runtime")
	warmN := flag.Int("warm", 20, "warm-invoke samples per runtime")
	runtimeCSV := flag.String("runtime", "nodejs22.x,python3.13", "comma-separated runtimes")
	apigw := flag.Bool("apigw", true, "also measure through an API Gateway v1 proxy route")
	cooldown := flag.Duration("cooldown", 2*time.Second, "pause between cold rounds (pacing)")
	padMB := flag.Int("pad-mb", 0, "embed N MiB of incompressible padding in the deployment zip — exercises the package-size-dependent paths (store split, tar cache) that hello-world zips cannot show")
	flag.Parse()

	if err := ping(*endpoint); err != nil {
		fatalf("overcast not reachable at %s: %v", *endpoint, err)
	}

	type row struct {
		runtime            string
		cold, warm, apigwW []float64 // ms
	}
	var rows []row
	for _, rt := range strings.Split(*runtimeCSV, ",") {
		rt = strings.TrimSpace(rt)
		fmt.Printf("\n── %s: %d cold rounds × (1 cold + %d warm) ──\n", rt, *coldN, *warmN)
		r := row{runtime: rt}
		if err := benchRuntime(*endpoint, rt, *coldN, *warmN, *apigw, *cooldown, *padMB, &r.cold, &r.warm, &r.apigwW); err != nil {
			fmt.Fprintf(os.Stderr, "  %s FAILED: %v\n", rt, err)
			continue
		}
		rows = append(rows, r)
	}

	fmt.Printf("\n%-12s %10s %10s %10s │ %10s %10s │ %12s %12s\n",
		"Runtime", "cold-p50", "cold-p95", "cold-max", "warm-p50", "warm-p95", "apigw-p50", "apigw-p95")
	fmt.Println(strings.Repeat("─", 108))
	for _, r := range rows {
		fmt.Printf("%-12s %9.0fms %9.0fms %9.0fms │ %9.1fms %9.1fms │ %11.1fms %11.1fms\n",
			r.runtime, pctl(r.cold, 50), pctl(r.cold, 95), pctl(r.cold, 100),
			pctl(r.warm, 50), pctl(r.warm, 95), pctl(r.apigwW, 50), pctl(r.apigwW, 95))
	}
}

func benchRuntime(endpoint, rt string, coldN, warmN int, apigw bool, cooldown time.Duration, padMB int, cold, warm, apigwW *[]float64) error {
	name := fmt.Sprintf("bench-lambda-%s-%d", strings.ReplaceAll(rt, ".", "-"), time.Now().Unix())
	if err := createFunction(endpoint, name, rt, padMB); err != nil {
		return fmt.Errorf("create function: %w", err)
	}
	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, endpoint+"/2015-03-31/functions/"+name, nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
	if err := waitActive(endpoint, name, 3*time.Minute); err != nil {
		return fmt.Errorf("function never became Active (image pull?): %w", err)
	}

	var apiID string
	if apigw {
		id, err := createProxyAPI(endpoint, name)
		if err != nil {
			return fmt.Errorf("create API GW route: %w", err)
		}
		apiID = id
		defer func() {
			req, _ := http.NewRequest(http.MethodDelete, endpoint+"/restapis/"+apiID, nil)
			resp, err := httpClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
	}

	for round := 0; round < coldN; round++ {
		// Retire the warm set via an identity-changing configuration update.
		if err := bumpEnv(endpoint, name, round); err != nil {
			return fmt.Errorf("bump env: %w", err)
		}
		if err := waitActive(endpoint, name, time.Minute); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond) // let retirement settle

		ms, err := timeInvoke(endpoint, name)
		if err != nil {
			return fmt.Errorf("cold invoke: %w", err)
		}
		*cold = append(*cold, ms)
		fmt.Printf("  round %d: cold %.0fms", round+1, ms)

		warmRounds := warmN / coldN
		if warmRounds < 1 {
			warmRounds = 1
		}
		var roundWarm []float64
		for i := 0; i < warmRounds; i++ {
			ms, err := timeInvoke(endpoint, name)
			if err != nil {
				return fmt.Errorf("warm invoke: %w", err)
			}
			roundWarm = append(roundWarm, ms)
			*warm = append(*warm, ms)
		}
		fmt.Printf(", warm p50 %.1fms", pctl(roundWarm, 50))

		if apiID != "" {
			var roundGW []float64
			for i := 0; i < warmRounds; i++ {
				ms, err := timeHTTP(http.MethodGet, endpoint+"/restapis/"+apiID+"/bench/_user_request_/bench", nil)
				if err != nil {
					return fmt.Errorf("apigw invoke: %w", err)
				}
				roundGW = append(roundGW, ms)
				*apigwW = append(*apigwW, ms)
			}
			fmt.Printf(", apigw p50 %.1fms", pctl(roundGW, 50))
		}
		fmt.Println()
		time.Sleep(cooldown)
	}
	return nil
}

// ---- Overcast API helpers -------------------------------------------------

func ping(endpoint string) error {
	resp, err := httpClient.Get(endpoint + "/_overcast/metrics")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func helloZip(rt string, padMB int) ([]byte, string, error) {
	var filename, source string
	switch {
	case strings.HasPrefix(rt, "nodejs"):
		filename = "index.js"
		source = "exports.handler = async () => ({ statusCode: 200, body: \"ok\" });\n"
	case strings.HasPrefix(rt, "python"):
		filename = "index.py"
		source = "def handler(event, context):\n    return {\"statusCode\": 200, \"body\": \"ok\"}\n"
	default:
		return nil, "", fmt.Errorf("no hello-world template for runtime %q", rt)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(filename)
	if err != nil {
		return nil, "", err
	}
	if _, err := f.Write([]byte(source)); err != nil {
		return nil, "", err
	}
	if padMB > 0 {
		// Store the padding uncompressed: random bytes don't deflate, and
		// Store keeps the harness's own CPU out of the measurement.
		hdr := &zip.FileHeader{Name: "padding.bin", Method: zip.Store}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, "", err
		}
		pad := make([]byte, padMB<<20)
		if _, err := rand.Read(pad); err != nil {
			return nil, "", err
		}
		if _, err := w.Write(pad); err != nil {
			return nil, "", err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "index.handler", nil
}

func createFunction(endpoint, name, rt string, padMB int) error {
	zipBytes, handler, err := helloZip(rt, padMB)
	if err != nil {
		return err
	}
	body := map[string]any{
		"FunctionName": name,
		"Runtime":      rt,
		"Handler":      handler,
		"Role":         "arn:aws:iam::000000000000:role/bench",
		"Timeout":      30,
		"MemorySize":   128,
		"Code":         map[string]any{"ZipFile": base64.StdEncoding.EncodeToString(zipBytes)},
	}
	return doJSON(http.MethodPost, endpoint+"/2015-03-31/functions", body, http.StatusCreated, nil)
}

func waitActive(endpoint, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var cfg struct {
			State string `json:"State"`
		}
		if err := doJSON(http.MethodGet, endpoint+"/2015-03-31/functions/"+name+"/configuration", nil, http.StatusOK, &cfg); err == nil {
			switch cfg.State {
			case "Active", "":
				return nil
			case "Failed":
				return fmt.Errorf("function state Failed")
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Active")
}

func bumpEnv(endpoint, name string, round int) error {
	body := map[string]any{
		"Environment": map[string]any{"Variables": map[string]string{"BENCH_ROUND": fmt.Sprint(round)}},
	}
	return doJSON(http.MethodPut, endpoint+"/2015-03-31/functions/"+name+"/configuration", body, http.StatusOK, nil)
}

func timeInvoke(endpoint, name string) (float64, error) {
	return timeHTTP(http.MethodPost, endpoint+"/2015-03-31/functions/"+name+"/invocations", strings.NewReader("{}"))
}

func createProxyAPI(endpoint, functionName string) (string, error) {
	var api struct {
		ID             string `json:"id"`
		RootResourceID string `json:"rootResourceId"`
	}
	if err := doJSON(http.MethodPost, endpoint+"/restapis", map[string]any{"name": "bench-lambda"}, http.StatusCreated, &api); err != nil {
		return "", fmt.Errorf("create rest api: %w", err)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := doJSON(http.MethodPost, endpoint+"/restapis/"+api.ID+"/resources/"+api.RootResourceID,
		map[string]any{"pathPart": "bench"}, http.StatusCreated, &res); err != nil {
		return "", fmt.Errorf("create resource: %w", err)
	}
	if err := doJSON(http.MethodPut, endpoint+"/restapis/"+api.ID+"/resources/"+res.ID+"/methods/GET",
		map[string]any{"authorizationType": "NONE"}, http.StatusCreated, nil); err != nil {
		return "", fmt.Errorf("put method: %w", err)
	}
	uri := "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:" + functionName + "/invocations"
	if err := doJSON(http.MethodPut, endpoint+"/restapis/"+api.ID+"/resources/"+res.ID+"/methods/GET/integration",
		map[string]any{"type": "AWS_PROXY", "httpMethod": "POST", "uri": uri}, http.StatusCreated, nil); err != nil {
		return "", fmt.Errorf("put integration: %w", err)
	}
	return api.ID, nil
}

// ---- plumbing -------------------------------------------------------------

func timeHTTP(method, url string, body io.Reader) (float64, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, payload)
	}
	if fe := resp.Header.Get("X-Amz-Function-Error"); fe != "" {
		return 0, fmt.Errorf("function error %s: %s", fe, payload)
	}
	return elapsed, nil
}

func doJSON(method, url string, body any, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != wantStatus && !(wantStatus == http.StatusCreated && resp.StatusCode == http.StatusOK) {
		return fmt.Errorf("%s %s: status %d (want %d): %s", method, url, resp.StatusCode, wantStatus, payload)
	}
	if out != nil {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func pctl(samples []float64, p float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := int(p / 100 * float64(len(sorted)-1))
	return sorted[idx]
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "bench-lambda: "+format+"\n", args...)
	os.Exit(1)
}
