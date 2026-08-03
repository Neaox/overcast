package rds

import "strings"

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

	for known := range versions {
		// Longest advertised version wins: "14.11" is a better answer for
		// "14.11.2" than a bare "14" would be.
		if versionMatches(version, known) && len(known) > len(matched) {
			matched = known
		}
	}
	if matched != "" {
		return versions[matched], matched, true
	}
	if fallback := defaultEngineVersions[engine]; versions[fallback] != "" {
		return versions[fallback], fallback, true
	}
	return "", "", false
}

// versionMatches reports whether an advertised version is a usable stand-in for
// the requested one: a dot-boundary prefix of it, or the same major version.
func versionMatches(want, advertised string) bool {
	if strings.HasPrefix(want, advertised+".") {
		return true
	}
	return majorOf(want) != "" && majorOf(want) == majorOf(advertised)
}

func majorOf(version string) string {
	if i := strings.IndexByte(version, '.'); i > 0 {
		return version[:i]
	}
	return version
}
