package serviceutil

import (
	"strings"
)

// RegionKey builds a region-scoped store key by prepending region + "/" to key.
// If region is empty the key is returned unchanged, enabling "scan all regions"
// queries when used as a List/Scan prefix.
func RegionKey(region, key string) string {
	if region == "" {
		return key
	}
	return region + "/" + key
}

// SplitRegionKey extracts the region prefix and the remaining key from a
// region-scoped store key. Returns ("", key) if the key has no "/" separator.
func SplitRegionKey(key string) (region, rest string) {
	i := strings.IndexByte(key, '/')
	if i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// ARNRegion extracts the region component from an AWS ARN
// (the 4th colon-separated field, e.g. "us-east-1").
// Returns an empty string for non-ARN inputs or ARNs without a region field.
func ARNRegion(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) >= 5 {
		return parts[3]
	}
	return ""
}
