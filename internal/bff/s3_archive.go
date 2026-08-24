package bff

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// maxArchiveKeys bounds one archive request. The archive itself streams, so
// the count costs no memory here — the limit is a guard against a runaway
// selection turning into an unbounded run of GETs against the emulator, and it
// is reported before the first byte so the console can say what happened.
//
// It sits well below the 10,000 parameters net/url will parse in one form
// body, so a selection over the limit is answered by the message below rather
// than by the runtime refusing to parse the request at all.
const maxArchiveKeys = 5_000

// archiveErrorsEntry names the note added to an archive when some of the
// selection could not be read.
const archiveErrorsEntry = "_download-errors.txt"

// handleS3Archive streams the selected objects of one bucket as a single zip.
//
// The request is a form POST rather than a GET with the keys in the query,
// and the response is a download rather than JSON, because both halves have
// to stream:
//
//   - a selection is unbounded, and a URL is not. Keys go in the body, where
//     no header-size limit applies to them.
//   - the response is written entry by entry as each object is read, so the
//     memory cost here is one buffer rather than the whole archive. The
//     browser only gets that benefit if the request is a navigation — a fetch
//     read into a Blob would buffer the archive in the tab instead — so the
//     console submits a real form and lets the browser stream to disk.
//
// Because the response is a download, its status code is spent on the first
// byte. Everything that can be judged up front is judged before then and
// answers with JSON; an object that fails once the archive is under way is
// recorded inside it, in archiveErrorsEntry.
func handleS3Archive(w http.ResponseWriter, r *http.Request) {
	// A form POST cannot set x-overcast-endpoint, so the console puts the
	// endpoint in the action URL's query — the same route the download links
	// and the event stream take.
	ep := resolveEndpointQP(r)
	bucket := chi.URLParam(r, "bucket")

	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("malformed form body (a selection carries at most %d objects)", maxArchiveKeys))
		return
	}
	keys := r.PostForm["key"]
	if len(keys) == 0 {
		writeJSONError(w, http.StatusBadRequest, "at least one key is required")
		return
	}
	if len(keys) > maxArchiveKeys {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("too many objects: %d selected, %d is the most one archive holds",
				len(keys), maxArchiveKeys))
		return
	}
	prefix := r.PostForm.Get("prefix")

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", archiveFilename(bucket, prefix)))
	// No Content-Length: the size of an archive that has not been built is not
	// known, and the only way to know it would be to build it in memory first.
	w.WriteHeader(http.StatusOK)

	flusher, canFlush := w.(http.Flusher)
	zw := zip.NewWriter(w)
	var failures []string

	for _, key := range keys {
		if err := archiveOneObject(r.Context(), zw, ep, bucket, key, prefix); err != nil {
			// Plain ASCII: this note is read in whatever text editor the
			// user unpacks the archive with, not in the console.
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
		}
		// Push each object out as it lands rather than letting the archive
		// pool up behind the zip writer's own buffer: the download shows
		// progress, and a long selection of small objects still moves.
		if err := zw.Flush(); err != nil {
			// The only realistic cause is the client having gone away, and
			// there is no way to tell it anything now. The archive is left
			// unclosed on purpose: a truncated zip reads as truncated,
			// where a closed one would claim to be everything asked for.
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}

	if len(failures) > 0 {
		writeArchiveErrors(zw, failures)
	}
	zw.Close()
}

// archiveOneObject copies one object from the emulator straight into the
// archive. The body is never held whole: it is copied through as it arrives.
func archiveOneObject(ctx context.Context, zw *zip.Writer, ep, bucket, key, prefix string) error {
	resp, err := doGet(ctx, fmt.Sprintf("%s/%s/%s", ep, bucket, escapeKeySegments(key)))
	if err != nil {
		return fmt.Errorf("emulator unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	header := &zip.FileHeader{Name: archiveEntryName(key, prefix), Method: zip.Deflate}
	// The object's own timestamp, so an unpacked file dates from when it was
	// written rather than from when it was downloaded.
	if t, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		header.Modified = t
	}
	entry, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, resp.Body)
	return err
}

// writeArchiveErrors adds the note describing what could not be read. It is
// written on a best-effort basis: the archive is already on the wire, so a
// failure to add the note leaves the objects that did arrive intact.
func writeArchiveErrors(zw *zip.Writer, failures []string) {
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: archiveErrorsEntry, Method: zip.Deflate})
	if err != nil {
		return
	}
	fmt.Fprintf(entry, "%d of the selected objects could not be downloaded:\n\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(entry, "  %s\n", f)
	}
}

// archiveEntryName is the path one key takes inside the archive: the key with
// the folder it was selected in removed, so a selection made in logs/2026/
// unpacks as a.txt rather than rebuilding the whole bucket path around it.
//
// The result is a path an extractor will create, and an S3 key may contain
// anything — including ".." and a leading "/". Those are defused here rather
// than trusted: a key of "logs/../../etc/passwd" must not write outside the
// directory the user unpacked into.
func archiveEntryName(key, prefix string) string {
	segments := strings.Split(strings.TrimPrefix(key, prefix), "/")
	kept := make([]string, 0, len(segments))
	for _, s := range segments {
		if s == "" || s == "." || s == ".." {
			continue
		}
		kept = append(kept, s)
	}
	if len(kept) == 0 {
		// Everything was stripped — a folder-marker key ("logs/") selected as
		// an object, or a key that was nothing but separators. Name it after
		// the key so the entry is still identifiable.
		return strings.Trim(strings.ReplaceAll(key, "/", "-"), "-")
	}
	return strings.Join(kept, "/")
}

// archiveFilename is what the browser saves the download as: the bucket, plus
// the folder the selection was made in so two archives from one bucket are
// told apart.
//
// Both halves go through the same reduction. A prefix is a key fragment and
// can hold anything a key can; a bucket name is far more constrained, but it
// reaches a response header from the URL, and one rule for the whole filename
// beats two and an argument about which inputs deserve which.
func archiveFilename(bucket, prefix string) string {
	parts := make([]string, 0, 4)
	if name := filenameSafe(bucket); name != "" {
		parts = append(parts, name)
	}
	for _, segment := range strings.Split(prefix, "/") {
		if cleaned := filenameSafe(segment); cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	if len(parts) == 0 {
		return "objects.zip"
	}
	return strings.Join(parts, "-") + ".zip"
}

// filenameSafe reduces one path segment to characters that are safe in a
// filename on every platform the console runs on, collapsing runs of anything
// else to a single "-".
func filenameSafe(segment string) string {
	var b strings.Builder
	lastDash := true // leading dashes are dropped
	for _, r := range segment {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
