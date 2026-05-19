package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// S3 returns the S3 service group.
func S3() ServiceGroup {
	g := &s3Group{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			// s3-crud
			"CreateBucket":           g.CreateBucket,
			"PutObject":              g.PutObject,
			"HeadObject":             g.HeadObject,
			"GetObject":              g.GetObject,
			"ListObjectsV2":          g.ListObjectsV2,
			"PutObjectMultipleKeys":  g.PutObjectMultipleKeys,
			"ListObjectsV2Delimiter": g.ListObjectsV2Delimiter,
			"DeleteObject":           g.DeleteObject,
			"DeleteObjects":          g.DeleteObjects,
			"DeleteBucket":           g.DeleteBucket,
			// s3-copy
			"CreateSourceBucket": g.CreateSourceBucket,
			"PutSourceObject":    g.PutSourceObject,
			"CopyObject":         g.CopyObject,
			// s3-multipart
			"CreateMultipartUpload":   g.CreateMultipartUpload,
			"UploadPart":              g.UploadPart,
			"CompleteMultipartUpload": g.CompleteMultipartUpload,
			"AbortMultipartUpload":    g.AbortMultipartUpload,
			// s3-versioning
			"PutBucketVersioning": g.PutBucketVersioning,
			"GetBucketVersioning": g.GetBucketVersioning,
			// s3-tagging
			"PutObjectTagging": g.PutObjectTagging,
			"GetObjectTagging": g.GetObjectTagging,
			"PutBucketTagging": g.PutBucketTagging,
			"GetBucketTagging": g.GetBucketTagging,
			// s3-website
			"PutBucketWebsite": g.PutBucketWebsite,
			"GetBucketWebsite": g.GetBucketWebsite,
			// s3-cors
			"PutBucketCors": g.PutBucketCors,
			"GetBucketCors": g.GetBucketCors,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"s3-crud":       g.setupCrud,
			"s3-copy":       g.setupCopy,
			"s3-multipart":  g.setupMultipart,
			"s3-versioning": g.setupVersioning,
			"s3-tagging":    g.setupTagging,
			"s3-website":    g.setupWebsite,
			"s3-cors":       g.setupCors,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"s3-crud":       g.teardownBucket,
			"s3-copy":       g.teardownCopy,
			"s3-multipart":  g.teardownMultipart,
			"s3-versioning": g.teardownVersioning,
			"s3-tagging":    g.teardownBucket,
			"s3-website":    g.teardownBucket,
			"s3-cors":       g.teardownBucket,
		},
	}
}

// One Namer per S3 sub-group ensures parallel groups never share bucket names.
var (
	s3CrudNamer    = harness.NewNamer("s3-crud")
	s3CopyNamer    = harness.NewNamer("s3-copy")
	s3CopySrcNamer = harness.NewNamer("s3-copy-src")
	s3MpNamer      = harness.NewNamer("s3-mp")
	s3VerNamer     = harness.NewNamer("s3-ver")
	s3TagNamer     = harness.NewNamer("s3-tag")
	s3WebNamer     = harness.NewNamer("s3-web")
	s3CorsNamer    = harness.NewNamer("s3-cors")
)

type s3Group struct{}

// bucket returns the bucket name for the current test group, stored in context
// by each group's setup function to avoid name collisions between parallel groups.
func (g *s3Group) bucket(t *harness.TestContext) string {
	if b := t.GetString("bucket"); b != "" {
		return b
	}
	return s3CrudNamer.Name(t)
}

func (g *s3Group) srcBucket(t *harness.TestContext) string {
	return s3CopySrcNamer.Name(t)
}

// ─── s3-crud ─────────────────────────────────────────────────────────────────

func (g *s3Group) setupCrud(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3CrudNamer.Name(t))
	return nil
}

func (g *s3Group) setupCopy(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3CopyNamer.Name(t))
	return nil
}

func (g *s3Group) CreateBucket(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t))
}

func (g *s3Group) PutObject(_ context.Context, t *harness.TestContext) error {
	if err := awscli.RunWithStdin(t.Endpoint, t.Region, "hello",
		"s3api", "put-object",
		"--bucket", g.bucket(t),
		"--key", "hello.txt",
		"--content-type", "text/plain",
		"--body", "/dev/stdin",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "head-object",
		"--bucket", g.bucket(t),
		"--key", "hello.txt",
	)
	if err != nil {
		return fmt.Errorf("s3 PutObject: head-object failed: %w", err)
	}
	if out["ContentLength"] == nil {
		return fmt.Errorf("s3 PutObject: missing ContentLength")
	}
	return nil
}

func (g *s3Group) HeadObject(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "head-object",
		"--bucket", g.bucket(t),
		"--key", "hello.txt",
	)
	return err
}

func (g *s3Group) GetObject(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region,
		"s3api", "get-object",
		"--bucket", g.bucket(t),
		"--key", "hello.txt",
		"/dev/null",
	)
}

func (g *s3Group) ListObjectsV2(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-objects-v2",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return err
	}
	if out["Contents"] == nil {
		return fmt.Errorf("s3 ListObjectsV2: expected Contents, got none")
	}
	return nil
}

func (g *s3Group) PutObjectMultipleKeys(_ context.Context, t *harness.TestContext) error {
	for _, key := range []string{"prefix/a.txt", "prefix/b.txt"} {
		if err := awscli.RunWithStdin(t.Endpoint, t.Region, "data",
			"s3api", "put-object",
			"--bucket", g.bucket(t),
			"--key", key,
			"--body", "/dev/stdin",
		); err != nil {
			return err
		}
	}
	return nil
}

func (g *s3Group) ListObjectsV2Delimiter(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-objects-v2",
		"--bucket", g.bucket(t),
		"--delimiter", "/",
		"--prefix", "prefix/",
	)
	if err != nil {
		return err
	}
	prefixes, _ := out["CommonPrefixes"].([]any)
	contents, _ := out["Contents"].([]any)
	if len(prefixes) == 0 && len(contents) == 0 {
		return fmt.Errorf("s3 ListObjectsV2Delimiter: expected CommonPrefixes or Contents, got none")
	}
	return nil
}

func (g *s3Group) DeleteObject(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "delete-object",
		"--bucket", g.bucket(t),
		"--key", "hello.txt",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-objects-v2",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 DeleteObject: list-objects-v2 failed: %w", err)
	}
	contents, _ := out["Contents"].([]any)
	for _, raw := range contents {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "hello.txt" {
			return fmt.Errorf("s3 DeleteObject: key 'hello.txt' still present after delete")
		}
	}
	return nil
}

func (g *s3Group) DeleteObjects(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "delete-objects",
		"--bucket", g.bucket(t),
		"--delete", `{"Objects":[{"Key":"prefix/a.txt"},{"Key":"prefix/b.txt"}],"Quiet":true}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-objects-v2",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 DeleteObjects: list-objects-v2 failed: %w", err)
	}
	contents, _ := out["Contents"].([]any)
	for _, raw := range contents {
		if m, ok := raw.(map[string]any); ok {
			if m["Key"] == "prefix/a.txt" || m["Key"] == "prefix/b.txt" {
				return fmt.Errorf("s3 DeleteObjects: key %v still present after delete", m["Key"])
			}
		}
	}
	return nil
}

func (g *s3Group) DeleteBucket(_ context.Context, t *harness.TestContext) error {
	bucket := g.bucket(t)
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "delete-bucket",
		"--bucket", bucket,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "s3api", "list-buckets")
	if err != nil {
		return fmt.Errorf("s3 DeleteBucket: list-buckets failed: %w", err)
	}
	buckets, _ := out["Buckets"].([]any)
	for _, raw := range buckets {
		if m, ok := raw.(map[string]any); ok && m["Name"] == bucket {
			return fmt.Errorf("s3 DeleteBucket: bucket %q still present after delete", bucket)
		}
	}
	return nil
}

func (g *s3Group) teardownBucket(_ context.Context, t *harness.TestContext) error {
	// Best-effort: purge objects then delete bucket.
	out, _ := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-objects-v2", "--bucket", g.bucket(t),
	)
	if out != nil {
		if contents, ok := out["Contents"].([]any); ok {
			for _, item := range contents {
				obj := item.(map[string]any)
				if key, ok := obj["Key"].(string); ok {
					awscli.Run(t.Endpoint, t.Region, "s3api", "delete-object", //nolint:errcheck
						"--bucket", g.bucket(t), "--key", key)
				}
			}
		}
	}
	awscli.Run(t.Endpoint, t.Region, "s3api", "delete-bucket", "--bucket", g.bucket(t)) //nolint:errcheck
	return nil
}

// teardownMultipart aborts any incomplete multipart uploads before deleting the bucket.
func (g *s3Group) teardownMultipart(ctx context.Context, t *harness.TestContext) error {
	out, _ := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-multipart-uploads", "--bucket", g.bucket(t),
	)
	if out != nil {
		if uploads, ok := out["Uploads"].([]any); ok {
			for _, u := range uploads {
				up := u.(map[string]any)
				key, _ := up["Key"].(string)
				uploadID, _ := up["UploadId"].(string)
				if key != "" && uploadID != "" {
					awscli.Run(t.Endpoint, t.Region, "s3api", "abort-multipart-upload", //nolint:errcheck
						"--bucket", g.bucket(t), "--key", key, "--upload-id", uploadID)
				}
			}
		}
	}
	return g.teardownBucket(ctx, t)
}

// teardownVersioning deletes all object versions and delete markers before deleting the bucket.
func (g *s3Group) teardownVersioning(_ context.Context, t *harness.TestContext) error {
	out, _ := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-object-versions", "--bucket", g.bucket(t),
	)
	if out != nil {
		for _, listKey := range []string{"Versions", "DeleteMarkers"} {
			if items, ok := out[listKey].([]any); ok {
				for _, item := range items {
					obj := item.(map[string]any)
					key, _ := obj["Key"].(string)
					versionID, _ := obj["VersionId"].(string)
					if key != "" && versionID != "" {
						awscli.Run(t.Endpoint, t.Region, "s3api", "delete-object", //nolint:errcheck
							"--bucket", g.bucket(t), "--key", key, "--version-id", versionID)
					}
				}
			}
		}
	}
	awscli.Run(t.Endpoint, t.Region, "s3api", "delete-bucket", "--bucket", g.bucket(t)) //nolint:errcheck
	return nil
}

// ─── s3-copy ─────────────────────────────────────────────────────────────────

func (g *s3Group) CreateSourceBucket(_ context.Context, t *harness.TestContext) error {
	return awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.srcBucket(t))
}

func (g *s3Group) PutSourceObject(_ context.Context, t *harness.TestContext) error {
	if err := awscli.RunWithStdin(t.Endpoint, t.Region, "source content",
		"s3api", "put-object",
		"--bucket", g.srcBucket(t),
		"--key", "src.txt",
		"--body", "/dev/stdin",
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "head-object",
		"--bucket", g.srcBucket(t),
		"--key", "src.txt",
	)
	if err != nil {
		return fmt.Errorf("s3 PutSourceObject: head-object failed: %w", err)
	}
	if out["ContentLength"] == nil {
		return fmt.Errorf("s3 PutSourceObject: missing ContentLength")
	}
	return nil
}

func (g *s3Group) CopyObject(_ context.Context, t *harness.TestContext) error {
	// Destination bucket is created here (lazily) for the copy group.
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "copy-object",
		"--bucket", g.bucket(t),
		"--key", "dst.txt",
		"--copy-source", fmt.Sprintf("%s/src.txt", g.srcBucket(t)),
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "head-object",
		"--bucket", g.bucket(t),
		"--key", "dst.txt",
	)
	if err != nil {
		return fmt.Errorf("s3 CopyObject: head-object on dst.txt failed: %w", err)
	}
	if out["ContentLength"] == nil {
		return fmt.Errorf("s3 CopyObject: missing ContentLength on copied object")
	}
	return nil
}

func (g *s3Group) teardownCopy(_ context.Context, t *harness.TestContext) error {
	for _, b := range []string{g.bucket(t), g.srcBucket(t)} {
		out, _ := awscli.RunOutput(t.Endpoint, t.Region, "s3api", "list-objects-v2", "--bucket", b)
		if out != nil {
			if contents, ok := out["Contents"].([]any); ok {
				for _, item := range contents {
					obj := item.(map[string]any)
					if key, ok := obj["Key"].(string); ok {
						awscli.Run(t.Endpoint, t.Region, "s3api", "delete-object", "--bucket", b, "--key", key) //nolint:errcheck
					}
				}
			}
		}
		awscli.Run(t.Endpoint, t.Region, "s3api", "delete-bucket", "--bucket", b) //nolint:errcheck
	}
	return nil
}

// ─── s3-multipart ────────────────────────────────────────────────────────────

func (g *s3Group) setupMultipart(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3MpNamer.Name(t))
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *s3Group) CreateMultipartUpload(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "create-multipart-upload",
		"--bucket", g.bucket(t),
		"--key", "multipart.bin",
	)
	if err != nil {
		return err
	}
	uploadID, _ := out["UploadId"].(string)
	if uploadID == "" {
		return fmt.Errorf("s3 CreateMultipartUpload: missing UploadId")
	}
	t.Set("upload_id", uploadID)
	return nil
}

func (g *s3Group) UploadPart(_ context.Context, t *harness.TestContext) error {
	uploadID := t.GetString("upload_id")
	if uploadID == "" {
		return fmt.Errorf("s3 UploadPart: missing upload_id in state")
	}
	// Multipart parts must be at least 5 MiB on real AWS, but the emulator accepts any size.
	partData := strings.Repeat("x", 1024)
	out, err := awscli.RunOutputWithStdin(t.Endpoint, t.Region, partData,
		"s3api", "upload-part",
		"--bucket", g.bucket(t),
		"--key", "multipart.bin",
		"--part-number", "1",
		"--upload-id", uploadID,
		"--body", "/dev/stdin",
	)
	if err != nil {
		return err
	}
	etag, _ := out["ETag"].(string)
	if etag == "" {
		return fmt.Errorf("s3 UploadPart: missing ETag")
	}
	t.Set("part_etag", strings.Trim(etag, `"`))
	return nil
}

func (g *s3Group) CompleteMultipartUpload(_ context.Context, t *harness.TestContext) error {
	uploadID := t.GetString("upload_id")
	etag := t.GetString("part_etag")
	if uploadID == "" || etag == "" {
		return fmt.Errorf("s3 CompleteMultipartUpload: missing upload_id or part_etag")
	}
	parts := fmt.Sprintf(`{"Parts":[{"PartNumber":1,"ETag":"%s"}]}`, etag)
	_, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "complete-multipart-upload",
		"--bucket", g.bucket(t),
		"--key", "multipart.bin",
		"--upload-id", uploadID,
		"--multipart-upload", parts,
	)
	if err != nil {
		return err
	}
	// Verify the object exists
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "head-object",
		"--bucket", g.bucket(t),
		"--key", "multipart.bin",
	)
	if err != nil {
		return fmt.Errorf("s3 CompleteMultipartUpload: head-object failed: %w", err)
	}
	if out["ContentLength"] == nil {
		return fmt.Errorf("s3 CompleteMultipartUpload: missing ContentLength")
	}
	return nil
}

func (g *s3Group) AbortMultipartUpload(_ context.Context, t *harness.TestContext) error {
	// Create a fresh upload to abort.
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "create-multipart-upload",
		"--bucket", g.bucket(t),
		"--key", "abort.bin",
	)
	if err != nil {
		return err
	}
	uploadID, _ := out["UploadId"].(string)
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "abort-multipart-upload",
		"--bucket", g.bucket(t),
		"--key", "abort.bin",
		"--upload-id", uploadID,
	); err != nil {
		return err
	}
	uploadList, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "list-multipart-uploads",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 AbortMultipartUpload: list-multipart-uploads failed: %w", err)
	}
	uploads, _ := uploadList["Uploads"].([]any)
	for _, raw := range uploads {
		if m, ok := raw.(map[string]any); ok && m["UploadId"] == uploadID {
			return fmt.Errorf("s3 AbortMultipartUpload: upload %s still present after abort", uploadID)
		}
	}
	return nil
}

// ─── s3-versioning ───────────────────────────────────────────────────────────

func (g *s3Group) setupVersioning(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3VerNamer.Name(t))
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *s3Group) PutBucketVersioning(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "put-bucket-versioning",
		"--bucket", g.bucket(t),
		"--versioning-configuration", `{"Status":"Enabled"}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-versioning",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 PutBucketVersioning: get-bucket-versioning failed: %w", err)
	}
	if status, _ := out["Status"].(string); status != "Enabled" {
		return fmt.Errorf("s3 PutBucketVersioning: expected Enabled, got %q", status)
	}
	return nil
}

func (g *s3Group) GetBucketVersioning(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-versioning",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return err
	}
	if status, _ := out["Status"].(string); status != "Enabled" {
		return fmt.Errorf("s3 GetBucketVersioning: expected Status=Enabled, got %q", status)
	}
	return nil
}

// ─── s3-tagging ──────────────────────────────────────────────────────────────

func (g *s3Group) setupTagging(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3TagNamer.Name(t))
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	return awscli.RunWithStdin(t.Endpoint, t.Region, "tag-content",
		"s3api", "put-object",
		"--bucket", g.bucket(t),
		"--key", "obj.txt",
		"--body", "/dev/stdin",
	)
}

func (g *s3Group) PutObjectTagging(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "put-object-tagging",
		"--bucket", g.bucket(t),
		"--key", "obj.txt",
		"--tagging", `{"TagSet":[{"Key":"env","Value":"test"}]}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-object-tagging",
		"--bucket", g.bucket(t),
		"--key", "obj.txt",
	)
	if err != nil {
		return fmt.Errorf("s3 PutObjectTagging: get-object-tagging failed: %w", err)
	}
	tags, _ := out["TagSet"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "env" && m["Value"] == "test" {
			return nil
		}
	}
	return fmt.Errorf("s3 PutObjectTagging: env=test tag not found")
}

func (g *s3Group) GetObjectTagging(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-object-tagging",
		"--bucket", g.bucket(t),
		"--key", "obj.txt",
	)
	if err != nil {
		return err
	}
	tags, _ := out["TagSet"].([]any)
	if len(tags) == 0 {
		return fmt.Errorf("s3 GetObjectTagging: expected tags, got none")
	}
	return nil
}

func (g *s3Group) PutBucketTagging(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "put-bucket-tagging",
		"--bucket", g.bucket(t),
		"--tagging", `{"TagSet":[{"Key":"project","Value":"overcast"}]}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-tagging",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 PutBucketTagging: get-bucket-tagging failed: %w", err)
	}
	tags, _ := out["TagSet"].([]any)
	for _, raw := range tags {
		if m, ok := raw.(map[string]any); ok && m["Key"] == "project" && m["Value"] == "overcast" {
			return nil
		}
	}
	return fmt.Errorf("s3 PutBucketTagging: project=overcast tag not found")
}

func (g *s3Group) GetBucketTagging(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-tagging",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return err
	}
	tags, _ := out["TagSet"].([]any)
	if len(tags) == 0 {
		return fmt.Errorf("s3 GetBucketTagging: expected tags, got none")
	}
	return nil
}

// ─── s3-website ──────────────────────────────────────────────────────────────

func (g *s3Group) setupWebsite(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3WebNamer.Name(t))
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *s3Group) PutBucketWebsite(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "put-bucket-website",
		"--bucket", g.bucket(t),
		"--website-configuration",
		`{"IndexDocument":{"Suffix":"index.html"},"ErrorDocument":{"Key":"error.html"}}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-website",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 PutBucketWebsite: get-bucket-website failed: %w", err)
	}
	idx, _ := out["IndexDocument"].(map[string]any)
	if idx == nil || idx["Suffix"] != "index.html" {
		return fmt.Errorf("s3 PutBucketWebsite: IndexDocument.Suffix != index.html")
	}
	return nil
}

func (g *s3Group) GetBucketWebsite(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-website",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return err
	}
	idx, _ := out["IndexDocument"].(map[string]any)
	if idx == nil || idx["Suffix"] != "index.html" {
		return fmt.Errorf("s3 GetBucketWebsite: expected IndexDocument.Suffix=index.html")
	}
	return nil
}

// ─── s3-cors ─────────────────────────────────────────────────────────────────

func (g *s3Group) setupCors(_ context.Context, t *harness.TestContext) error {
	t.Set("bucket", s3CorsNamer.Name(t))
	if err := awscli.Run(t.Endpoint, t.Region, "s3api", "create-bucket", "--bucket", g.bucket(t)); err != nil && !isAlreadyExists(err) {
		return err
	}
	return nil
}

func (g *s3Group) PutBucketCors(_ context.Context, t *harness.TestContext) error {
	if err := awscli.Run(t.Endpoint, t.Region,
		"s3api", "put-bucket-cors",
		"--bucket", g.bucket(t),
		"--cors-configuration",
		`{"CORSRules":[{"AllowedMethods":["GET","PUT"],"AllowedOrigins":["*"],"AllowedHeaders":["*"]}]}`,
	); err != nil {
		return err
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-cors",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return fmt.Errorf("s3 PutBucketCors: get-bucket-cors failed: %w", err)
	}
	rules, _ := out["CORSRules"].([]any)
	if len(rules) == 0 {
		return fmt.Errorf("s3 PutBucketCors: expected CORS rules, got none")
	}
	return nil
}

func (g *s3Group) GetBucketCors(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region,
		"s3api", "get-bucket-cors",
		"--bucket", g.bucket(t),
	)
	if err != nil {
		return err
	}
	rules, _ := out["CORSRules"].([]any)
	if len(rules) == 0 {
		return fmt.Errorf("s3 GetBucketCors: expected CORS rules, got none")
	}
	return nil
}
