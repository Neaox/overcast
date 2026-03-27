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
	"strings"
	"time"

	"github.com/your-org/overcast/internal/protocol"
	"github.com/your-org/overcast/internal/state"
)

// Namespaces used in the state store. Keeping them as constants avoids
// typos and makes grep-ability easy.
const (
	nsBuckets = "s3:buckets"
	nsObjects = "s3:objects"
	nsMeta    = "s3:meta"
)

// Bucket represents a stored S3 bucket.
type Bucket struct {
	Name         string    `json:"name"`
	Region       string    `json:"region"`
	CreationDate time.Time `json:"creation_date"`
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

// getObject returns object metadata and loads the full body from disk.
// Use getObjectMeta + openBody for streaming large bodies to clients.
func (s *s3Store) getObject(ctx context.Context, bucket, key string) (*Object, *protocol.AWSError) {
	obj, aerr := s.getObjectMeta(ctx, bucket, key)
	if aerr != nil {
		return nil, aerr
	}
	body, err := os.ReadFile(s.bodyPath(bucket, key))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: read body %s/%s: %w", bucket, key, err))
	}
	obj.Body = body
	return obj, nil
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

func (s *s3Store) putObject(ctx context.Context, obj *Object) *protocol.AWSError {
	// Write body to disk first — if this fails we haven't touched the metadata store.
	if err := s.writeBody(obj.Bucket, obj.Key, obj.Body); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsObjects, objectStoreKey(obj.Bucket, obj.Key), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
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

func (s *s3Store) deleteObject(ctx context.Context, bucket, key string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsObjects, objectStoreKey(bucket, key)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Remove body file — ignore "not found" since DeleteObject is idempotent.
	p := s.bodyPath(bucket, key)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: remove body %s/%s: %w", bucket, key, err))
	}
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

// writeBody persists the object body to disk, creating directories as needed.
// Used by putObject for small/known bodies. For streaming uploads, use
// putObjectStream instead.
func (s *s3Store) writeBody(bucket, key string, data []byte) error {
	p := s.bodyPath(bucket, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("s3: create body dir for %s/%s: %w", bucket, key, err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return fmt.Errorf("s3: write body %s/%s: %w", bucket, key, err)
	}
	return nil
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
		obj, aerr := s.getObject(ctx, bucket, objKey)
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
