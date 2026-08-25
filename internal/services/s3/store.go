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
//
// It mirrors com.amazonaws.s3#WebsiteConfiguration: either
// RedirectAllRequestsTo on its own, or IndexDocument with an optional
// ErrorDocument and optional RoutingRules. handler_website.go enforces the
// exclusion, so a stored configuration never carries both forms.
type WebsiteConfiguration struct {
	IndexDocument         string               `json:"index_document,omitempty"`
	ErrorDocument         string               `json:"error_document,omitempty"`
	RedirectAllRequestsTo *WebsiteRedirectAll  `json:"redirect_all_requests_to,omitempty"`
	RoutingRules          []WebsiteRoutingRule `json:"routing_rules,omitempty"`
}

// WebsiteRedirectAll redirects every request to the bucket's website endpoint.
// HostName is required; Protocol is com.amazonaws.s3#Protocol (http | https)
// and is empty when the caller omitted it.
type WebsiteRedirectAll struct {
	HostName string `json:"host_name"`
	Protocol string `json:"protocol,omitempty"`
}

// WebsiteRoutingRule is one conditional redirect. Condition is optional; a
// rule without one applies to every request.
type WebsiteRoutingRule struct {
	Condition *WebsiteRoutingCondition `json:"condition,omitempty"`
	Redirect  WebsiteRedirect          `json:"redirect"`
}

// WebsiteRoutingCondition selects the requests a routing rule redirects. At
// least one predicate is set; both together mean both must hold.
type WebsiteRoutingCondition struct {
	HTTPErrorCodeReturnedEquals string `json:"http_error_code_returned_equals,omitempty"`
	KeyPrefixEquals             string `json:"key_prefix_equals,omitempty"`
}

// WebsiteRedirect is where a routing rule sends a matching request. At least
// one field is set, and ReplaceKeyWith and ReplaceKeyPrefixWith are mutually
// exclusive.
type WebsiteRedirect struct {
	HostName             string `json:"host_name,omitempty"`
	HTTPRedirectCode     string `json:"http_redirect_code,omitempty"`
	Protocol             string `json:"protocol,omitempty"`
	ReplaceKeyPrefixWith string `json:"replace_key_prefix_with,omitempty"`
	ReplaceKeyWith       string `json:"replace_key_with,omitempty"`
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

	// Namespace is the S3 namespace this bucket was created in:
	// "account-regional", or "" for the global namespace (also the decode of
	// every bucket record persisted before this field existed — no migration
	// needed). Store-only state: no CreateBucket/ListBuckets/GetBucketLocation
	// response carries a namespace member, so this never round-trips onto the
	// wire (wire purity — see issue #1471).
	Namespace string `json:"namespace,omitempty"`

	// VersionHistoryReady records that every object already in this bucket has
	// been given a place in s3:versions. See ensureVersionHistory in
	// version.go — it is the completion flag for that backfill, not a feature
	// switch, and it is meaningless on a bucket that is not versioned.
	VersionHistoryReady bool `json:"version_history_ready,omitempty"`
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
	// StorageClass is empty for an object that has never been given one,
	// which reads as STANDARD. A lifecycle Transition sets it as a synthetic
	// marker — no bytes move (docs/plans/full-emulation-priority.md §7).
	StorageClass string `json:"storage_class,omitempty"`

	// VersionID is the version id S3 reports for this version, or "" for the
	// null version — the one AWS gives an object stored while its bucket was
	// unversioned or version-suspended. See version.go.
	VersionID string `json:"version_id,omitempty"`

	// Seq orders a key's versions newest-first and forms the s3:versions
	// storage key. Empty on a record written before version history existed,
	// and on every object in a bucket that has never been versioned.
	Seq string `json:"seq,omitempty"`

	// DeleteMarker marks this version as a delete marker: a version with no
	// body that hides the versions beneath it. See version.go.
	DeleteMarker bool `json:"delete_marker,omitempty"`

	// IsLatest is derived at list time, never stored: which version is current
	// is a property of the key's history, not of one record, and persisting it
	// would be a second source of truth to keep in step.
	IsLatest bool `json:"-"`
}

// effectiveStorageClass returns the class S3 reports for the object, defaulting
// to STANDARD as AWS does for an object stored without one.
func (o *Object) effectiveStorageClass() string {
	if o.StorageClass == "" {
		return storageClassStandard
	}
	return o.StorageClass
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
	// Drop the lifecycle configuration with the bucket. Leaving it behind
	// would keep the sweeper visiting a bucket that no longer exists, and
	// would silently re-apply the old rules to a bucket recreated under the
	// same name — which is not what AWS does.
	if err := s.store.Delete(ctx, nsLifecycle, name); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Notification configuration is stored separately from the bucket record.
	// Remove it as part of the same logical deletion so recreating a bucket name
	// cannot resurrect destinations owned by the previous bucket.
	if err := s.store.Delete(ctx, nsNotifications, name); err != nil {
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

// listBuckets returns all stored buckets in an unspecified order. Uses Scan
// instead of List+per-key Get so listing N buckets costs one store round trip
// instead of N+1 (storage-plan.md item 3.1).
func (s *s3Store) listBuckets(ctx context.Context) ([]*Bucket, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsBuckets, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	buckets := make([]*Bucket, 0, len(pairs))
	for _, kv := range pairs {
		var b Bucket
		if err := json.Unmarshal([]byte(kv.Value), &b); err != nil {
			// One malformed persisted record must not fail the whole list
			// (CLAUDE.md malformed-persisted-state rule).
			continue
		}
		buckets = append(buckets, &b)
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

// lookupObjectMeta is getObjectMeta without the not-found error, for callers
// that treat an absent key as an ordinary case rather than a client error.
func (s *s3Store) lookupObjectMeta(ctx context.Context, bucket, key string) (*Object, *protocol.AWSError) {
	obj, aerr := s.getObjectMeta(ctx, bucket, key)
	if aerr != nil {
		if aerr.Code == "NoSuchKey" {
			return nil, nil
		}
		return nil, aerr
	}
	return obj, nil
}

// openBody returns an open file handle for the given version's body.
// The caller is responsible for closing it.
func (s *s3Store) openBody(obj *Object) (*os.File, *protocol.AWSError) {
	f, err := os.Open(s.bodyPath(obj.Bucket, obj.Key, obj.VersionID))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: open body %s/%s: %w", obj.Bucket, obj.Key, err))
	}
	return f, nil
}

// putObjectStream streams the request body to disk while computing size and
// MD5 in a single pass, then persists metadata to the store. This avoids
// buffering the entire body in memory.
func (s *s3Store) putObjectStream(ctx context.Context, obj *Object, body io.Reader) (etag string, n int64, aerr *protocol.AWSError) {
	p := s.bodyPath(obj.Bucket, obj.Key, obj.VersionID)
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

// deleteObject removes a key's current record and its body. This is the
// unversioned path: a key in a versioned bucket is never removed this way,
// because a delete there either adds a delete marker or removes one specific
// version — see handler_object.go's DeleteObject.
func (s *s3Store) deleteObject(ctx context.Context, bucket, key string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsObjects, objectStoreKey(bucket, key)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	// Remove body file — ignore "not found" since DeleteObject is idempotent.
	p := s.bodyPath(bucket, key, "")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return protocol.Wrap(protocol.ErrInternalError, fmt.Errorf("s3: remove body %s/%s: %w", bucket, key, err))
	}
	// Remove the bucket directory if it is now empty; ignore errors since
	// other objects may still be present or the directory may not exist.
	_ = os.Remove(filepath.Dir(p))
	return nil
}

// deleteObjectRecord removes only the key's current-version pointer, leaving
// every s3:versions record and body file alone. Used when a version-targeted
// delete removes the last version a key had.
func (s *s3Store) deleteObjectRecord(ctx context.Context, bucket, key string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsObjects, objectStoreKey(bucket, key)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

// ---- Body file helpers -----------------------------------------------------

// bodyPath returns the on-disk path for one version's body.
//
// Uses SHA-256 of the key to avoid filesystem issues with special characters,
// deeply nested paths, or path traversal. The version id is appended for every
// version but the null one, whose path is left exactly where an unversioned
// object's body has always been — a key has at most one null version, so there
// is nothing to disambiguate, and objects written before version history
// existed stay readable without moving a byte.
func (s *s3Store) bodyPath(bucket, key, versionID string) string {
	h := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(h[:])
	if versionID != "" {
		name += "." + versionID
	}
	return filepath.Join(s.bodyDir, bucket, name)
}

// copyBody copies one version's body file to another while computing the MD5
// ETag in a single streaming pass. Returns the ETag and byte count.
func (s *s3Store) copyBody(src, dst *Object) (etag string, n int64, err error) {
	srcBucket, srcKey, dstBucket, dstKey := src.Bucket, src.Key, dst.Bucket, dst.Key
	srcFile, err := os.Open(s.bodyPath(srcBucket, srcKey, src.VersionID))
	if err != nil {
		return "", 0, fmt.Errorf("s3: open source body %s/%s: %w", srcBucket, srcKey, err)
	}
	defer srcFile.Close()

	dstPath := s.bodyPath(dstBucket, dstKey, dst.VersionID)
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return "", 0, fmt.Errorf("s3: create body dir for %s/%s: %w", dstBucket, dstKey, err)
	}

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return "", 0, fmt.Errorf("s3: create dest body %s/%s: %w", dstBucket, dstKey, err)
	}

	h := md5.New()
	w := io.MultiWriter(dstFile, h)
	n, err = io.Copy(w, srcFile)
	if cerr := dstFile.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dstPath)
		return "", 0, fmt.Errorf("s3: copy body %s/%s -> %s/%s: %w", srcBucket, srcKey, dstBucket, dstKey, err)
	}

	return fmt.Sprintf(`"%x"`, h.Sum(nil)), n, nil
}

// listObjects returns all objects in bucket whose keys start with prefix.
// Uses Scan (a single store round trip) instead of List+per-key Get
// (storage-plan.md item 3.1) — object bodies still stay on disk and unread,
// only the metadata values already returned by Scan are decoded.
func (s *s3Store) listObjects(ctx context.Context, bucket, prefix string) ([]*Object, *protocol.AWSError) {
	scanPrefix := objectStoreKey(bucket, prefix)
	pairs, err := s.store.Scan(ctx, nsObjects, scanPrefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	objects := make([]*Object, 0, len(pairs))
	for _, kv := range pairs {
		var obj Object
		if err := json.Unmarshal([]byte(kv.Value), &obj); err != nil {
			// One malformed persisted record must not fail the whole list
			// (CLAUDE.md malformed-persisted-state rule).
			continue
		}
		objects = append(objects, &obj)
	}
	return objects, nil
}

// listObjectsPage returns up to limit objects in bucket whose keys start
// with prefix, in key order, starting strictly after startAfter (a plain
// object key, not a storage key).
//
// This is ListObjects{V1,V2}'s internal-pagination primitive
// (storage-access-plan.md item A6): the handler loops this instead of
// calling listObjects (a full-bucket Scan), so memory is bounded by the
// internal page size instead of the bucket size. ScanPage already returns
// results in key order, so — unlike listObjects' callers — nothing needs to
// re-sort the result.
//
// nextKey is the object key to pass as startAfter for the next internal
// page, or "" when the scan reached the end of the prefix range (no more
// objects in this bucket/prefix).
func (s *s3Store) listObjectsPage(ctx context.Context, bucket, prefix, startAfter string, limit int) ([]*Object, string, *protocol.AWSError) {
	scanPrefix := objectStoreKey(bucket, prefix)
	storageStartAfter := ""
	if startAfter != "" {
		storageStartAfter = objectStoreKey(bucket, startAfter)
	}

	pairs, nextStorageKey, err := s.store.ScanPage(ctx, nsObjects, scanPrefix, storageStartAfter, limit)
	if err != nil {
		return nil, "", protocol.Wrap(protocol.ErrInternalError, err)
	}

	objects := make([]*Object, 0, len(pairs))
	for _, kv := range pairs {
		var obj Object
		if err := json.Unmarshal([]byte(kv.Value), &obj); err != nil {
			// One malformed persisted record must not fail the whole list
			// (CLAUDE.md malformed-persisted-state rule). The storage
			// cursor below tracks raw keys, not decoded objects, so
			// skipping this record here does not skip it at the storage
			// layer — the next internal page still starts after it.
			continue
		}
		objects = append(objects, &obj)
	}

	nextKey := ""
	if nextStorageKey != "" {
		// nextStorageKey is "<bucket>/<key>" (objectStoreKey's format) —
		// strip the "<bucket>/" prefix back to a plain object key so
		// callers can feed it back into startAfter (and into StartAfter /
		// Marker / ContinuationToken wire fields) without knowing the
		// storage key's internal shape.
		nextKey = nextStorageKey[len(bucket)+1:]
	}
	return objects, nextKey, nil
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

	// EventBridgeConfiguration mirrors com.amazonaws.s3#EventBridgeConfiguration,
	// which is an empty structure: its presence is the whole signal, and while
	// it is set S3 sends every object event to the default event bus with no
	// event-type or key filtering. A pointer models that presence; the struct
	// stays empty so a future member lands here rather than changing the shape.
	EventBridgeConfiguration *EventBridgeNotificationConfig `json:"event_bridge_configuration,omitempty"`
}

// EventBridgeNotificationConfig enables delivery of the bucket's events to
// Amazon EventBridge. AWS models it as a structure with no members.
type EventBridgeNotificationConfig struct{}

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
// Uses Scan instead of List+per-key Get (storage-plan.md item 3.1).
func (s *s3Store) listMultipartUploads(ctx context.Context, bucket string) ([]*MultipartUpload, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsMultipart, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	result := make([]*MultipartUpload, 0)
	for _, kv := range pairs {
		var u MultipartUpload
		if err := json.Unmarshal([]byte(kv.Value), &u); err != nil {
			// One malformed persisted record must not fail the whole list
			// (CLAUDE.md malformed-persisted-state rule).
			continue
		}
		if u.Bucket == bucket {
			result = append(result, &u)
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

// listParts returns all parts for an upload, sorted by part number. Uses
// Scan instead of List+per-key Get (storage-plan.md item 3.1).
func (s *s3Store) listParts(ctx context.Context, uploadID string) ([]*Part, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsParts, uploadID+"/")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	parts := make([]*Part, 0, len(pairs))
	for _, kv := range pairs {
		var p Part
		if jsonErr := json.Unmarshal([]byte(kv.Value), &p); jsonErr != nil {
			// One malformed persisted record must not fail the whole list
			// (CLAUDE.md malformed-persisted-state rule).
			continue
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
