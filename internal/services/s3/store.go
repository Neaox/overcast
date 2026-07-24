package s3

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// Namespaces used in the state store. Keeping them as constants avoids
// typos and makes grep-ability easy.
const (
	nsBuckets = "s3:buckets"
	nsObjects = "s3:objects"
)

// WebsiteConfiguration stores S3 website configuration for a bucket.
type WebsiteConfiguration struct {
	IndexDocument string `json:"index_document,omitempty"`
	ErrorDocument string `json:"error_document,omitempty"`
}

// CORSRule stores a single CORS rule for an S3 bucket.
type CORSRule struct {
	AllowedHeaders []string `json:"allowed_headers,omitempty"`
	AllowedMethods []string `json:"allowed_methods"`
	AllowedOrigins []string `json:"allowed_origins"`
	ExposeHeaders  []string `json:"expose_headers,omitempty"`
	MaxAgeSeconds  int      `json:"max_age_seconds,omitempty"`
}

type BucketEncryptionRule struct {
	SSEAlgorithm     string `json:"sse_algorithm"`
	KMSMasterKeyID   string `json:"kms_master_key_id,omitempty"`
	BucketKeyEnabled *bool  `json:"bucket_key_enabled,omitempty"`
}

// Bucket represents a stored S3 bucket.
type Bucket struct {
	Name             string                 `json:"name"`
	Region           string                 `json:"region"`
	CreationDate     time.Time              `json:"creation_date"`
	VersioningStatus string                 `json:"versioning_status,omitempty"` // "Enabled", "Suspended", or ""
	Tags             map[string]string      `json:"tags,omitempty"`
	WebsiteConfig    *WebsiteConfiguration  `json:"website_config,omitempty"`
	CORSRules        []CORSRule             `json:"cors_rules,omitempty"`
	Policy           string                 `json:"policy,omitempty"`
	EncryptionRules  []BucketEncryptionRule `json:"encryption_rules,omitempty"`
}

// Object represents a stored S3 object.
// Body is stored on disk, not in the state store — the json:"-" tag
// excludes it from serialisation. Use s3Store.putObject/getObject to
// handle body persistence transparently.
type Object struct {
	Bucket             string            `json:"bucket"`
	Key                string            `json:"key"`
	Body               []byte            `json:"-"`
	ContentType        string            `json:"content_type"`
	ContentLength      int64             `json:"content_length"`
	ETag               string            `json:"etag"`
	LastModified       time.Time         `json:"last_modified"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
	ContentDisposition string            `json:"content_disposition,omitempty"`
	ContentEncoding    string            `json:"content_encoding,omitempty"`
	ContentLanguage    string            `json:"content_language,omitempty"`
	CacheControl       string            `json:"cache_control,omitempty"`
	Expires            string            `json:"expires,omitempty"`
}

// s3Store wraps state.Store with S3-specific get/put/delete helpers.
// Services should go through this rather than calling state.Store directly,
// so that serialisation logic lives in one place.
//
// Object bodies are stored as files on disk under bodyDir rather than in the
// state store. This avoids unbounded memory growth when storing large objects
// and allows streaming reads without loading the full body into the heap.
type s3Store struct {
	store   state.Store
	bodyDir string // <dataDir>/s3-bodies
}

func newS3Store(store state.Store, dataDir string) *s3Store {
	return &s3Store{
		store:   store,
		bodyDir: filepath.Join(dataDir, "s3-bodies"),
	}
}

// ---- Bucket operations -----------------------------------------------------

func (s *s3Store) getBucket(ctx context.Context, name string) (*Bucket, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsBuckets, name)
	if err != nil {
		// Wrap preserves the storage error for logging while returning a clean AWS error to the client.
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchBucket(name)
	}
	var b Bucket
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &b, nil
}

func (s *s3Store) putBucket(ctx context.Context, b *Bucket) *protocol.AWSError {
	raw, err := json.Marshal(b)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsBuckets, b.Name, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *s3Store) deleteBucket(ctx context.Context, name string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsBuckets, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Remove the on-disk body directory for the bucket (and any remaining
	// body files). Ignore errors — the directory may not exist.
	_ = os.RemoveAll(filepath.Join(s.bodyDir, name))
	return nil
}

func (s *s3Store) bucketExists(ctx context.Context, name string) (bool, *protocol.AWSError) {
	_, found, err := s.store.Get(ctx, nsBuckets, name)
	if err != nil {
		return false, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return found, nil
}

// listBuckets returns all stored buckets in an unspecified order.
func (s *s3Store) listBuckets(ctx context.Context) ([]*Bucket, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsBuckets, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	buckets := make([]*Bucket, 0, len(keys))
	for _, k := range keys {
		b, aerr := s.getBucket(ctx, k)
		if aerr != nil {
			// Bucket was deleted between List and getBucket (TOCTOU race).
			if aerr.HTTPStatus == http.StatusNotFound {
				continue
			}
			return nil, aerr
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// ---- Object operations -----------------------------------------------------

// objectStoreKey builds the state store key for an object.
// Format: "<bucket>/<key>" — slashes in key names are preserved.
func objectStoreKey(bucket, key string) string {
	return bucket + "/" + key
}

// getObjectMeta returns object metadata without reading the body from disk.
// Use this for HeadObject and anywhere only headers/metadata are needed.
func (s *s3Store) getObjectMeta(ctx context.Context, bucket, key string) (*Object, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsObjects, objectStoreKey(bucket, key))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchKey(key)
	}
	var obj Object
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &obj, nil
}

// openBody returns an open file handle for the object's body.
// The caller is responsible for closing it.
func (s *s3Store) openBody(bucket, key string) (*os.File, *protocol.AWSError) {
	f, err := os.Open(s.bodyPath(bucket, key))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: open body %s/%s: %w", bucket, key, err))
	}
	return f, nil
}

// putObjectStream streams the request body to disk while computing size and
// MD5 in a single pass, then persists metadata to the store. This avoids
// buffering the entire body in memory.
func (s *s3Store) putObjectStream(ctx context.Context, obj *Object, body io.Reader) (etag string, n int64, aerr *protocol.AWSError) {
	p := s.bodyPath(obj.Bucket, obj.Key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: create body dir for %s/%s: %w", obj.Bucket, obj.Key, err))
	}

	f, err := os.Create(p)
	if err != nil {
		return "", 0, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: create body file %s/%s: %w", obj.Bucket, obj.Key, err))
	}

	var h hash.Hash = md5.New()
	w := io.MultiWriter(f, h)

	n, err = io.Copy(w, body)
	// Close file before checking io.Copy error so we don't leak the fd.
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p) // best-effort cleanup
		return "", 0, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: stream body %s/%s: %w", obj.Bucket, obj.Key, err))
	}

	etag = fmt.Sprintf(`"%x"`, h.Sum(nil))
	obj.ETag = etag
	obj.ContentLength = n

	raw, err := json.Marshal(obj)
	if err != nil {
		return "", 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsObjects, objectStoreKey(obj.Bucket, obj.Key), string(raw)); err != nil {
		return "", 0, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return etag, n, nil
}

// putObjectMeta persists object metadata changes (e.g. tags) without touching
// the body file on disk. The caller is responsible for reading the Object via
// getObjectMeta first so that existing fields are preserved.
func (s *s3Store) putObjectMeta(ctx context.Context, obj *Object) *protocol.AWSError {
	raw, err := json.Marshal(obj)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsObjects, objectStoreKey(obj.Bucket, obj.Key), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *s3Store) deleteObject(ctx context.Context, bucket, key string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsObjects, objectStoreKey(bucket, key)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Remove body file — ignore "not found" since DeleteObject is idempotent.
	p := s.bodyPath(bucket, key)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: remove body %s/%s: %w", bucket, key, err))
	}
	// Remove the bucket directory if it is now empty; ignore errors since
	// other objects may still be present or the directory may not exist.
	_ = os.Remove(filepath.Dir(p))
	return nil
}

// ---- Body file helpers -----------------------------------------------------

// bodyPath returns the on-disk path for an object's body.
// Uses SHA-256 of the key to avoid filesystem issues with special characters,
// deeply nested paths, or path traversal.
func (s *s3Store) bodyPath(bucket, key string) string {
	h := sha256.Sum256([]byte(key))
	return filepath.Join(s.bodyDir, bucket, hex.EncodeToString(h[:]))
}

// copyBody copies one object's body file to another while computing the MD5
// ETag in a single streaming pass. Returns the ETag and byte count.
func (s *s3Store) copyBody(srcBucket, srcKey, dstBucket, dstKey string) (etag string, n int64, err error) {
	src, err := os.Open(s.bodyPath(srcBucket, srcKey))
	if err != nil {
		return "", 0, fmt.Errorf("s3: open source body %s/%s: %w", srcBucket, srcKey, err)
	}
	defer src.Close()

	dstPath := s.bodyPath(dstBucket, dstKey)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return "", 0, fmt.Errorf("s3: create body dir for %s/%s: %w", dstBucket, dstKey, err)
	}

	dst, err := os.Create(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("s3: create dest body %s/%s: %w", dstBucket, dstKey, err)
	}

	h := md5.New()
	w := io.MultiWriter(dst, h)
	n, err = io.Copy(w, src)
	if cerr := dst.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dstPath)
		return "", 0, fmt.Errorf("s3: copy body %s/%s -> %s/%s: %w", srcBucket, srcKey, dstBucket, dstKey, err)
	}

	return fmt.Sprintf(`"%x"`, h.Sum(nil)), n, nil
}

// listObjects returns all objects in bucket whose keys start with prefix.
// Uses getObjectMeta to avoid loading object bodies from disk.
func (s *s3Store) listObjects(ctx context.Context, bucket, prefix string) ([]*Object, *protocol.AWSError) {
	scanPrefix := objectStoreKey(bucket, prefix)
	keys, err := s.store.List(ctx, nsObjects, scanPrefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	objects := make([]*Object, 0, len(keys))
	for _, k := range keys {
		// List returns keys that include the bucket/ prefix — strip it to get
		// just the object key for the individual get.
		objKey := strings.TrimPrefix(k, bucket+"/")
		obj, aerr := s.getObjectMeta(ctx, bucket, objKey)
		if aerr != nil {
			return nil, aerr
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

// ---- S3-specific errors ----------------------------------------------------

func errNoSuchBucket(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchBucket",
		Message:    fmt.Sprintf("The specified bucket does not exist: %s", name),
		HTTPStatus: http.StatusNotFound,
	}
}

func errNoSuchKey(key string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchKey",
		Message:    fmt.Sprintf("The specified key does not exist: %s", key),
		HTTPStatus: http.StatusNotFound,
	}
}

func errBucketAlreadyExists(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BucketAlreadyOwnedByYou",
		Message:    fmt.Sprintf("Your previous request to create the named bucket succeeded and you already own it: %s", name),
		HTTPStatus: http.StatusConflict,
	}
}

func errBucketNotEmpty(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "BucketNotEmpty",
		Message:    fmt.Sprintf("The bucket you tried to delete is not empty: %s", name),
		HTTPStatus: http.StatusConflict,
	}
}

// ---- Notification configuration --------------------------------------------

const nsNotifications = "s3:notifications"

// NotificationConfig is the top-level structure stored per bucket.
// It mirrors the AWS S3 NotificationConfiguration XML schema.
type NotificationConfig struct {
	QueueConfigurations  []QueueNotificationConfig  `json:"queue_configurations,omitempty"`
	LambdaConfigurations []LambdaNotificationConfig `json:"lambda_configurations,omitempty"`
	TopicConfigurations  []TopicNotificationConfig  `json:"topic_configurations,omitempty"`
}

// QueueNotificationConfig maps one set of S3 events to an SQS queue ARN.
type QueueNotificationConfig struct {
	ID     string              `json:"id"`
	ARN    string              `json:"arn"` // SQS queue ARN
	Events []string            `json:"events"`
	Filter *NotificationFilter `json:"filter,omitempty"`
}

// LambdaNotificationConfig maps one set of S3 events to a Lambda function ARN.
type LambdaNotificationConfig struct {
	ID     string              `json:"id"`
	ARN    string              `json:"arn"` // Lambda function ARN
	Events []string            `json:"events"`
	Filter *NotificationFilter `json:"filter,omitempty"`
}

// TopicNotificationConfig maps one set of S3 events to an SNS topic ARN.
type TopicNotificationConfig struct {
	ID     string              `json:"id"`
	ARN    string              `json:"arn"` // SNS topic ARN
	Events []string            `json:"events"`
	Filter *NotificationFilter `json:"filter,omitempty"`
}

// NotificationFilter holds key-based filter rules.
type NotificationFilter struct {
	Key NotificationFilterKey `json:"key"`
}

// NotificationFilterKey is the S3Key element that contains filter rules.
type NotificationFilterKey struct {
	Rules []NotificationFilterRule `json:"rules,omitempty"`
}

// NotificationFilterRule is a single prefix/suffix filter.
type NotificationFilterRule struct {
	Name  string `json:"name"` // "prefix" or "suffix"
	Value string `json:"value"`
}

func (s *s3Store) getNotificationConfig(ctx context.Context, bucket string) (*NotificationConfig, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsNotifications, bucket)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return &NotificationConfig{}, nil
	}
	var cfg NotificationConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &cfg, nil
}

func (s *s3Store) putNotificationConfig(ctx context.Context, bucket string, cfg *NotificationConfig) *protocol.AWSError {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsNotifications, bucket, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Multipart upload state ------------------------------------------------

const (
	nsMultipart = "s3:multipart" // stores MultipartUpload by uploadID
	nsParts     = "s3:parts"     // stores Part by "<uploadID>/<partNumber>"
)

// MultipartUpload tracks an initiated multipart upload before completion.
type MultipartUpload struct {
	UploadID    string            `json:"upload_id"`
	Bucket      string            `json:"bucket"`
	Key         string            `json:"key"`
	ContentType string            `json:"content_type"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Initiated   time.Time         `json:"initiated"`
}

// Part holds the metadata for one uploaded part.
// The body is stored on disk at partBodyPath(uploadID, partNumber).
type Part struct {
	PartNumber   int       `json:"part_number"`
	ETag         string    `json:"etag"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// partStoreKey returns the state-store key for a specific part.
func partStoreKey(uploadID string, partNumber int) string {
	return fmt.Sprintf("%s/%d", uploadID, partNumber)
}

// partBodyPath returns the on-disk path for a part body.
func (s *s3Store) partBodyPath(uploadID string, partNumber int) string {
	return filepath.Join(s.bodyDir, "multipart", uploadID, fmt.Sprintf("%d", partNumber))
}

func (s *s3Store) createMultipartUpload(ctx context.Context, upload *MultipartUpload) *protocol.AWSError {
	raw, err := json.Marshal(upload)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsMultipart, upload.UploadID, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *s3Store) getMultipartUpload(ctx context.Context, uploadID string) (*MultipartUpload, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsMultipart, uploadID)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNoSuchUpload(uploadID)
	}
	var u MultipartUpload
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &u, nil
}

func (s *s3Store) deleteMultipartUpload(ctx context.Context, uploadID string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsMultipart, uploadID); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// listMultipartUploads returns all in-progress uploads for the given bucket.
func (s *s3Store) listMultipartUploads(ctx context.Context, bucket string) ([]*MultipartUpload, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsMultipart, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	result := make([]*MultipartUpload, 0)
	for _, k := range keys {
		u, aerr := s.getMultipartUpload(ctx, k)
		if aerr != nil {
			return nil, aerr
		}
		if u.Bucket == bucket {
			result = append(result, u)
		}
	}
	return result, nil
}

// putPartStream streams the part body to disk and returns the ETag and size.
func (s *s3Store) putPartStream(uploadID string, partNumber int, body io.Reader) (etag string, n int64, err error) {
	p := s.partBodyPath(uploadID, partNumber)
	if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
		return "", 0, fmt.Errorf("s3: create part dir %s/%d: %w", uploadID, partNumber, mkErr)
	}

	f, fErr := os.Create(p)
	if fErr != nil {
		return "", 0, fmt.Errorf("s3: create part file %s/%d: %w", uploadID, partNumber, fErr)
	}

	h := md5.New()
	w := io.MultiWriter(f, h)
	n, err = io.Copy(w, body)
	if cerr := f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
		return "", 0, fmt.Errorf("s3: stream part %s/%d: %w", uploadID, partNumber, err)
	}
	return fmt.Sprintf(`"%x"`, h.Sum(nil)), n, nil
}

// savePart persists part metadata to the state store.
func (s *s3Store) savePart(ctx context.Context, uploadID string, part *Part) *protocol.AWSError {
	raw, err := json.Marshal(part)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsParts, partStoreKey(uploadID, part.PartNumber), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// listParts returns all parts for an upload, sorted by part number.
func (s *s3Store) listParts(ctx context.Context, uploadID string) ([]*Part, *protocol.AWSError) {
	keys, err := s.store.List(ctx, nsParts, uploadID+"/")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	parts := make([]*Part, 0, len(keys))
	for _, k := range keys {
		raw, found, getErr := s.store.Get(ctx, nsParts, k)
		if getErr != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, getErr)
		}
		if !found {
			continue
		}
		var p Part
		if jsonErr := json.Unmarshal([]byte(raw), &p); jsonErr != nil {
			return nil, protocol.Wrap(protocol.ErrInternalError, jsonErr)
		}
		parts = append(parts, &p)
	}
	// Sort by part number ascending.
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})
	return parts, nil
}

// deleteAllParts removes all part metadata and body files for an upload.
func (s *s3Store) deleteAllParts(ctx context.Context, uploadID string) *protocol.AWSError {
	keys, err := s.store.List(ctx, nsParts, uploadID+"/")
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	for _, k := range keys {
		if delErr := s.store.Delete(ctx, nsParts, k); delErr != nil {
			return protocol.Wrap(protocol.ErrInternalError, delErr)
		}
	}
	// Remove all part body files — ignore OS-level errors since they are non-critical.
	dir := filepath.Join(s.bodyDir, "multipart", uploadID)
	os.RemoveAll(dir)
	return nil
}

// openPartBody opens a part body file for reading. Caller must close.
func (s *s3Store) openPartBody(uploadID string, partNumber int) (*os.File, error) {
	return os.Open(s.partBodyPath(uploadID, partNumber))
}

// ---- Multipart-specific errors ---------------------------------------------

func errNoSuchUpload(uploadID string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "NoSuchUpload",
		Message:    fmt.Sprintf("The specified upload does not exist: %s", uploadID),
		HTTPStatus: http.StatusNotFound,
	}
}
