package cloudformation

import "fmt"

// tagDiffMaps computes the tags to add and remove when updating a
// CloudFormation resource. It compares old tag map (from oldProps)
// against new tag map (from newProps) and returns the sets that
// differ.
func tagDiffMaps(old any, new any) (add, remove map[string]string) {
	oldTags := toTagMap(old)
	newTags := toTagMap(new)

	add = make(map[string]string)
	remove = make(map[string]string)

	for k, v := range newTags {
		if oldVal, ok := oldTags[k]; !ok || oldVal != v {
			add[k] = v
		}
	}
	for k := range oldTags {
		if _, ok := newTags[k]; !ok {
			remove[k] = k
		}
	}
	return
}

// toTagMap converts a CFN Tags value into a flat map[string]string.
func toTagMap(v any) map[string]string {
	out := make(map[string]string)
	switch tags := v.(type) {
	case map[string]any:
		for k, val := range tags {
			out[k] = fmt.Sprintf("%v", val)
		}
	case []map[string]any:
		for _, t := range tags {
			k, _ := t["Key"].(string)
			val, _ := t["Value"].(string)
			if k != "" {
				out[k] = val
			}
		}
	case []any:
		for _, item := range tags {
			if t, ok := item.(map[string]any); ok {
				k, _ := t["Key"].(string)
				val, _ := t["Value"].(string)
				if k != "" {
					out[k] = val
				}
			}
		}
	}
	return out
}
