package rds

import (
	"strconv"
	"strings"
)

// resolveEngineImage picks the Docker image for an engine and version, and
// reports which advertised version answered.
//
// AWS engine versions are precise — CDK's `MysqlEngineVersion.VER_8_0_39` sends
// "8.0.39", `PostgresEngineVersion.VER_16_3` sends "16.3" — while engineImages
// is keyed by the handful of versions Overcast advertises. An exact-match lookup
// therefore failed for almost every version a real stack asks for, and it failed
// in the way that hurts most: no container started, so the endpoint hostname the
// API had already handed out resolved nowhere, while the instance still went
// "available".
//
// Matching narrows: exact, then the longest advertised version that is a
// dot-boundary prefix of the request ("8.0.39" → "8.0"), then the longest one
// sharing its major ("16.3" → "16.1", the 16.x image), then the engine's
// default. The images track version *families* — `mysql:8.0` serves every
// 8.0.x — so a prefix match is exact as far as the container is concerned.
//
// matched is the advertised version chosen; compare it against version to tell
// a substitution from a hit.
func resolveEngineImage(engine, version string) (image, matched string, ok bool) {
	versions, found := engineImages[engine]
	if !found {
		return "", "", false
	}
	if image, found := versions[version]; found {
		return image, version, true
	}

	want := version
	if engine == "aurora-mysql" {
		want = auroraMySQLTrackVersion(version)
	}
	for known := range versions {
		// Longest advertised version wins: "14.11" is a better answer for
		// "14.11.2" than a bare "14" would be.
		if strings.HasPrefix(want, known+".") && len(known) > len(matched) {
			matched = known
		}
	}
	if matched != "" {
		return versions[matched], matched, true
	}
	matched = nearestVersionInMajor(want, versions)
	if matched != "" {
		return versions[matched], matched, true
	}
	if fallback := defaultEngineVersions[engine]; versions[fallback] != "" {
		return versions[fallback], fallback, true
	}
	return "", "", false
}

func nearestVersionInMajor(want string, versions map[string]string) string {
	var lower, upper string
	for known := range versions {
		if majorOf(want) != majorOf(known) {
			continue
		}
		if versionAtLeast(want, known) {
			if lower == "" || versionAtLeast(known, lower) {
				lower = known
			}
		} else if upper == "" || versionAtLeast(upper, known) {
			upper = known
		}
	}
	if lower != "" {
		return lower
	}
	return upper
}

// auroraMySQLTrackVersion extracts Aurora's release track from both the short
// versions Overcast advertises and the full versions CloudFormation/CDK send.
// For example, 8.0.mysql_aurora.3.04.0 belongs to Aurora MySQL 3.04.0.
func auroraMySQLTrackVersion(version string) string {
	const marker = ".mysql_aurora."
	if i := strings.Index(version, marker); i >= 0 {
		return version[i+len(marker):]
	}
	return version
}

func majorOf(version string) string {
	if i := strings.IndexByte(version, '.'); i > 0 {
		return version[:i]
	}
	return version
}

func versionAtLeast(version, minimum string) bool {
	got := numericVersion(version)
	want := numericVersion(minimum)
	limit := len(got)
	if len(want) > limit {
		limit = len(want)
	}
	for i := 0; i < limit; i++ {
		var gotPart, wantPart int
		if i < len(got) {
			gotPart = got[i]
		}
		if i < len(want) {
			wantPart = want[i]
		}
		if gotPart != wantPart {
			return gotPart > wantPart
		}
	}
	return true
}

func numericVersion(version string) []int {
	parts := strings.Split(version, ".")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		result = append(result, n)
	}
	return result
}
