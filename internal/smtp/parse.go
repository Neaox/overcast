package smtp

import (
	"bufio"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// parseDataPayload parses the raw DATA section of an SMTP message and returns
// (subject, textBody, htmlBody). It handles both plain messages and
// multipart/alternative MIME messages. For anything else, the entire body is
// returned as textBody.
func parseDataPayload(raw string) (subject, textBody, htmlBody string) {
	reader := bufio.NewReader(strings.NewReader(raw))
	tp := textproto.NewReader(reader)

	header, err := tp.ReadMIMEHeader()
	if err != nil && !strings.Contains(err.Error(), "EOF") {
		// Unparseable — return raw as text body.
		return "", raw, ""
	}

	subject = decodeHeader(header.Get("Subject"))

	ct := header.Get("Content-Type")
	if ct == "" {
		// No Content-Type — rest of buffer is plain text.
		var sb strings.Builder
		for {
			line, err2 := tp.ReadLine()
			sb.WriteString(line)
			sb.WriteByte('\n')
			if err2 != nil {
				break
			}
		}
		return subject, strings.TrimSpace(sb.String()), ""
	}

	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return subject, raw, ""
	}

	if !strings.HasPrefix(mediaType, "multipart/") {
		// Single-part: read remaining body.
		var sb strings.Builder
		for {
			line, err2 := tp.ReadLine()
			sb.WriteString(line)
			sb.WriteByte('\n')
			if err2 != nil {
				break
			}
		}
		body := strings.TrimSpace(sb.String())
		if strings.EqualFold(mediaType, "text/html") {
			return subject, "", body
		}
		return subject, body, ""
	}

	// Multipart: walk parts looking for text/plain and text/html.
	boundary := params["boundary"]
	if boundary == "" {
		return subject, raw, ""
	}

	// Re-read the remaining body as a string to feed to multipart reader.
	var bodyBuf strings.Builder
	for {
		line, err2 := tp.ReadLine()
		bodyBuf.WriteString(line)
		bodyBuf.WriteByte('\n')
		if err2 != nil {
			break
		}
	}

	mr := multipart.NewReader(strings.NewReader(bodyBuf.String()), boundary)
	for {
		part, err2 := mr.NextPart()
		if err2 != nil {
			break
		}
		partCT := part.Header.Get("Content-Type")
		partMedia, _, _ := mime.ParseMediaType(partCT)
		var partBuf strings.Builder
		scanner := bufio.NewScanner(part)
		for scanner.Scan() {
			partBuf.WriteString(scanner.Text())
			partBuf.WriteByte('\n')
		}
		content := strings.TrimSpace(partBuf.String())
		switch strings.ToLower(partMedia) {
		case "text/plain":
			textBody = content
		case "text/html":
			htmlBody = content
		}
	}

	return subject, textBody, htmlBody
}

// decodeHeader decodes an RFC 2047 encoded header value, falling back to the
// raw value if decoding fails.
func decodeHeader(v string) string {
	dec := new(mime.WordDecoder)
	decoded, err := dec.DecodeHeader(v)
	if err != nil {
		return v
	}
	return decoded
}

// parseOvercastHeader extracts the value of a single custom header from a raw
// RFC 2822 DATA payload. Only the header section (before the first blank line)
// is scanned. Returns "" if the header is absent.
func parseOvercastHeader(raw, name string) string {
	prefix := strings.ToLower(name) + ":"
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			// Blank line marks end of headers.
			break
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}
