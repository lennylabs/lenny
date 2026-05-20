// SPDX-License-Identifier: MIT

package outputpartfidelity

import "strings"

// isImageMIME returns true when mime starts with "image/", matching the
// §15.4.1 image_url-vs-text branch used by the OpenAI Completions and
// Open Responses adapters.
func isImageMIME(mime string) bool {
	return strings.HasPrefix(mime, "image/")
}

// parseDataURL parses a data: URL of the form
// `data:<mime>;base64,<payload>` and returns the MIME type and the
// base64-encoded payload. The function is forgiving: a non-data URL
// returns ("", url).
func parseDataURL(u string) (string, string) {
	const prefix = "data:"
	if !strings.HasPrefix(u, prefix) {
		return "", u
	}
	body := u[len(prefix):]
	comma := strings.IndexByte(body, ',')
	if comma == -1 {
		return "", u
	}
	header, payload := body[:comma], body[comma+1:]
	semicolon := strings.IndexByte(header, ';')
	if semicolon == -1 {
		return header, payload
	}
	return header[:semicolon], payload
}
