package lambda

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const hotReloadTagKey = "overcast:hot-reload-path"

func copyTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}
	return out
}

func hotReloadTagPath(fn *Function) string {
	if fn == nil || fn.Tags == nil {
		return ""
	}
	return strings.TrimSpace(fn.Tags[hotReloadTagKey])
}

func normalizeHotReloadPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}

	// Convert Windows drive-letter paths to Docker-compatible mount paths.
	// Example: C:\\Users\\dev\\app -> /c/Users/dev/app
	if len(p) >= 3 && unicode.IsLetter(rune(p[0])) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		drive := strings.ToLower(string(p[0]))
		rest := strings.ReplaceAll(p[2:], "\\", "/")
		p = "/" + drive + rest
	}

	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if !strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%q is not absolute", raw)
	}
	return p, nil
}

func validateFunctionHotReloadConfig(fn *Function) (string, error) {
	raw := hotReloadTagPath(fn)
	if raw == "" {
		return "", nil
	}
	normalized, err := normalizeHotReloadPath(raw)
	if err != nil {
		return "", fmt.Errorf("tag %s must be an absolute path: %w", hotReloadTagKey, err)
	}
	return normalized, nil
}

func hotReloadBindPath(fn *Function, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	return validateFunctionHotReloadConfig(fn)
}

func functionCodeIdentity(fn *Function) string {
	if fn == nil {
		return ""
	}
	if fn.PackageType == "Image" {
		return imageHash(fn.ImageUri)
	}
	if p, err := validateFunctionHotReloadConfig(fn); err == nil && p != "" {
		if st, statErr := os.Stat(p); statErr == nil {
			return imageHash("hotreload:" + p + ":" + fmt.Sprintf("%d", st.ModTime().UnixNano()))
		}
		return imageHash("hotreload:" + p + ":missing")
	}
	return codeHashOf(fn.CodeZip)
}

func decorateHotReloadMountError(err error, hotReloadPath string) error {
	if err == nil || hotReloadPath == "" {
		return err
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "mounts denied") ||
		strings.Contains(msg, "invalid mount config") ||
		strings.Contains(msg, "bind source path does not exist") {
		return fmt.Errorf("hot reload mount failed for %q: %w; if using Docker Desktop, allow this path in File Sharing settings: https://docs.docker.com/desktop/settings-and-maintenance/settings/#file-sharing", hotReloadPath, err)
	}
	return err
}

// typeScriptSourceDiagnostic returns a non-empty warning string when dir
// contains only .ts files and no .js files AND the runtime does not support
// native TypeScript execution. Node.js 24+ strips types natively, so no
// bundling step is required for those runtimes.
// This catches the common mistake of pointing overcast:hot-reload-path at a
// TypeScript source directory on an older runtime — the Node.js runtime cannot
// import .ts files and will produce a cryptic Runtime.ImportModuleError.
// The caller should log the returned string at Warn.
func typeScriptSourceDiagnostic(dir, runtime string) string {
	// Node.js 24+ supports native type-stripping; .ts files run directly.
	if runtimeSupportsTypeStripping(runtime) {
		return ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var hasTS, hasJS bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".ts":
			hasTS = true
		case ".js", ".mjs", ".cjs":
			hasJS = true
		}
	}
	if hasTS && !hasJS {
		return fmt.Sprintf(
			"hot-reload: %q contains .ts files but no .js files — "+
				"the Node.js runtime cannot import TypeScript directly (upgrade to nodejs24.x for native type-stripping, "+
				"or run esbuild --watch --outdir=<dir> and point overcast:hot-reload-path at the output directory)",
			dir,
		)
	}
	return ""
}

// runtimeSupportsTypeStripping reports whether the given Lambda runtime
// identifier supports native TypeScript execution without a build step.
// Node.js 24+ ships with --experimental-strip-types enabled by default.
func runtimeSupportsTypeStripping(runtime string) bool {
	// Extract the major version number from strings like "nodejs24.x".
	runtime = strings.ToLower(runtime)
	if !strings.HasPrefix(runtime, "nodejs") {
		return false
	}
	majorStr := strings.TrimPrefix(runtime, "nodejs")
	if dot := strings.IndexByte(majorStr, '.'); dot >= 0 {
		majorStr = majorStr[:dot]
	}
	var major int
	for _, c := range majorStr {
		if c < '0' || c > '9' {
			return false
		}
		major = major*10 + int(c-'0')
	}
	return major >= 24
}
