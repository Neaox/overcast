package s3

import "encoding/xml"

// MarshalLifecycleConfiguration serializes the S3 domain model using the
// same private wire types used by Put/GetBucketLifecycleConfiguration. It is
// exported for internal service adapters such as CloudFormation; callers
// still dispatch the result through S3 so that S3 owns validation and state.
func MarshalLifecycleConfiguration(cfg *LifecycleConfiguration) ([]byte, error) {
	return xml.Marshal(lifecycleConfigurationXML{
		Xmlns: s3XMLNamespace,
		Rules: lifecycleRulesToXML(cfg.Rules),
	})
}

// MarshalVersioningConfiguration serializes a PutBucketVersioning request.
func MarshalVersioningConfiguration(status string) ([]byte, error) {
	return xml.Marshal(versioningConfigurationXML{Xmlns: s3XMLNamespace, Status: status})
}

// MarshalNotificationConfiguration serializes a
// PutBucketNotificationConfiguration request.
func MarshalNotificationConfiguration(cfg *NotificationConfig) ([]byte, error) {
	return xml.Marshal(notificationConfigToXML(cfg))
}

// MarshalBucketEncryption serializes a PutBucketEncryption request.
func MarshalBucketEncryption(rules []BucketEncryptionRule) ([]byte, error) {
	return xml.Marshal(bucketEncryptionXML{
		Xmlns: s3XMLNamespace,
		Rules: bucketEncryptionRulesToXML(rules),
	})
}

// MarshalBucketTagging serializes a PutBucketTagging request.
func MarshalBucketTagging(tags map[string]string) ([]byte, error) {
	return xml.Marshal(xmlTagging{Xmlns: s3XMLNamespace, TagSet: xmlTagSet{Tags: tagsToXML(tags)}})
}

// MarshalCORSConfiguration serializes a PutBucketCors request.
func MarshalCORSConfiguration(rules []CORSRule) ([]byte, error) {
	return xml.Marshal(corsConfigurationXML{Xmlns: s3XMLNamespace, CORSRules: corsRulesToXML(rules)})
}

// MarshalWebsiteConfiguration serializes a PutBucketWebsite request.
func MarshalWebsiteConfiguration(cfg *WebsiteConfiguration) ([]byte, error) {
	request := websiteConfigurationXML{Xmlns: s3XMLNamespace}
	if cfg.IndexDocument != "" {
		request.IndexDocument = &indexDocumentXML{Suffix: cfg.IndexDocument}
	}
	if cfg.ErrorDocument != "" {
		request.ErrorDocument = &errorDocumentXML{Key: cfg.ErrorDocument}
	}
	return xml.Marshal(request)
}
