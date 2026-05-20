// SPDX-License-Identifier: MIT

package outputpartfidelity

import (
	"encoding/json"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// OpenResponseItem is one item in the Open Responses output array.
// Per §15.4.1 fidelity matrix, the output item carries:
//
//   - Type: a content type (output_text, output_image, output_file)
//     derived from OutputPart.type (lossy).
//   - ID: mapped from OutputPart.id ([extended] — recoverable on
//     ingest for top-level output items).
//   - Status: mapped from OutputPart.status ([lossy] — failed is
//     representable; per-part streaming distinctions are not).
//   - Text/ImageURL/File: the inline content, base64 for binary.
//
// Other OutputPart fields are dropped per the matrix.
type OpenResponseItem struct {
	Type     string                  `json:"type"`
	ID       string                  `json:"id,omitempty"`
	Status   string                  `json:"status,omitempty"`
	Text     string                  `json:"text,omitempty"`
	ImageURL *openResponsesImageURL  `json:"image_url,omitempty"`
	File     *openResponsesFileBlock `json:"file,omitempty"`
}

type openResponsesImageURL struct {
	URL string `json:"url"`
}

type openResponsesFileBlock struct {
	Data     string `json:"data"`
	MimeType string `json:"mime_type"`
}

// TranslateOpenResponses converts an OutputPart to a single Open
// Responses output item per the §15.4.1 fidelity matrix.
//
// Type collapse:
//
//   - text, code, reasoning_trace, citation, diff, error, custom →
//     output_text with the inline content; reasoning_trace gets a
//     "reasoning" role annotation per the Canonical Type Registry.
//   - image, screenshot → output_image with a data: URL.
//   - file (image MIME) → output_image; otherwise output_file with
//     the inline bytes.
//
// ID and Status are preserved per the matrix (extended and lossy
// respectively).
func TranslateOpenResponses(p runtime.OutputPart) ([]byte, error) {
	return json.Marshal(toResponsesItem(p))
}

func toResponsesItem(p runtime.OutputPart) OpenResponseItem {
	item := OpenResponseItem{ID: p.ID, Status: mapResponsesStatus(p.Status)}
	switch p.Type {
	case "image", "screenshot":
		mime := p.MimeType
		if !isImageMIME(mime) {
			mime = "image/png"
		}
		item.Type = "output_image"
		item.ImageURL = &openResponsesImageURL{
			URL: "data:" + mime + ";base64," + p.Inline,
		}
	case "file":
		if isImageMIME(p.MimeType) {
			item.Type = "output_image"
			item.ImageURL = &openResponsesImageURL{
				URL: "data:" + p.MimeType + ";base64," + p.Inline,
			}
			return item
		}
		item.Type = "output_file"
		item.File = &openResponsesFileBlock{
			Data:     p.Inline,
			MimeType: p.MimeType,
		}
	default:
		// text, code, reasoning_trace, citation, diff, error and
		// every custom type collapse to output_text. Annotations
		// are dropped per the matrix; the inline content survives.
		item.Type = "output_text"
		item.Text = p.Inline
	}
	return item
}

// mapResponsesStatus maps the OutputPart.status field to the lossy
// Open Responses output-item status. Per the matrix, only `failed`
// has a native representation; `streaming` and `complete` collapse to
// the unset (default-complete) state.
func mapResponsesStatus(s string) string {
	if s == "failed" {
		return "failed"
	}
	return ""
}

// ParseOpenResponses parses a single Open Responses output item back
// into an OutputPart, applying the matrix:
//
//   - ID is recovered ([extended]).
//   - Type and MimeType are reconstructed only from the wire form
//     (lossy: a reasoning_trace input collapsed to output_text comes
//     back as text).
//   - Inline is recovered.
//   - Status comes back only when the wire-form set it to "failed".
//   - schemaVersion, ref, annotations, parts, protocolHints come back
//     as the zero value (dropped).
func ParseOpenResponses(wire []byte) (runtime.OutputPart, error) {
	var item OpenResponseItem
	if err := json.Unmarshal(wire, &item); err != nil {
		return runtime.OutputPart{}, err
	}
	out := runtime.OutputPart{ID: item.ID, Status: item.Status}
	switch item.Type {
	case "output_text":
		out.Type = "text"
		out.Inline = item.Text
	case "output_image":
		out.Type = "image"
		if item.ImageURL != nil {
			mime, data := parseDataURL(item.ImageURL.URL)
			out.MimeType = mime
			out.Inline = data
		}
	case "output_file":
		out.Type = "file"
		if item.File != nil {
			out.MimeType = item.File.MimeType
			out.Inline = item.File.Data
		}
	default:
		out.Type = "text"
		out.Inline = item.Text
	}
	return out, nil
}
