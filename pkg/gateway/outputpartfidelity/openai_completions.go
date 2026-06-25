// SPDX-License-Identifier: MIT

package outputpartfidelity

import (
	"encoding/json"

	"github.com/lennylabs/lenny/sdks/runtime/go/runtime"
)

// OpenAIContentBlock is one entry in the OpenAI Chat Completions
// content array. Chat Completions accepts a `string` content or a
// content-block array; the array form is what the fidelity matrix
// covers because a single `MessagePart` maps to a single content block.
//
// Fields preserved per §15.4.1 fidelity matrix:
//
//   - Type: collapsed to "text" or "image_url" (lossy).
//   - Text: present when the part collapses to text.
//   - ImageURL: present when the part is an image_url block.
//
// Every other MessagePart field (schemaVersion, id, mimeType, ref,
// annotations, parts, status, protocolHints) is dropped by the matrix.
type OpenAIContentBlock struct {
	Type     string             `json:"type"`
	Text     string             `json:"text,omitempty"`
	ImageURL *openAIImageURLObj `json:"image_url,omitempty"`
}

type openAIImageURLObj struct {
	URL string `json:"url"`
}

// TranslateOpenAICompletions converts a MessagePart to a single OpenAI
// Chat Completions content block per the §15.4.1 fidelity matrix. The
// returned bytes are the JSON-marshalled block; callers wrap them into
// the message content array.
//
// Type collapse:
//
//   - image, screenshot → image_url block carrying the inline data as
//     a data: URL.
//   - file with image MIME → image_url block.
//   - everything else → text block whose text is the inline content.
//
// Dropped fields produce no representation on the wire.
func TranslateOpenAICompletions(p runtime.MessagePart) ([]byte, error) {
	return json.Marshal(toOpenAIBlock(p))
}

// toOpenAIBlock applies the lossy type collapse and emits a content block.
func toOpenAIBlock(p runtime.MessagePart) OpenAIContentBlock {
	switch p.Type {
	case "image", "screenshot":
		return openAIImageBlock(p)
	case "file":
		if isImageMIME(p.MimeType) {
			return openAIImageBlock(p)
		}
		// Non-image files collapse to text per the matrix
		// (mimeType `[lossy]`, inline preserved).
		return OpenAIContentBlock{Type: "text", Text: p.Inline}
	}
	// Every other canonical type and every custom type collapses to
	// text with the inline content preserved.
	return OpenAIContentBlock{Type: "text", Text: p.Inline}
}

// openAIImageBlock builds the image_url block, encoding inline base64
// as a data: URL so the bytes survive the round trip.
func openAIImageBlock(p runtime.MessagePart) OpenAIContentBlock {
	if p.Inline == "" {
		return OpenAIContentBlock{Type: "text", Text: ""}
	}
	mime := p.MimeType
	if !isImageMIME(mime) {
		mime = "image/png"
	}
	return OpenAIContentBlock{
		Type:     "image_url",
		ImageURL: &openAIImageURLObj{URL: "data:" + mime + ";base64," + p.Inline},
	}
}

// ParseOpenAICompletions parses a single OpenAI Chat Completions
// content block back into a MessagePart, applying the matrix's
// dropped-field rule: every field not carried on the wire comes back
// as the zero value.
func ParseOpenAICompletions(wire []byte) (runtime.MessagePart, error) {
	var b OpenAIContentBlock
	if err := json.Unmarshal(wire, &b); err != nil {
		return runtime.MessagePart{}, err
	}
	out := runtime.MessagePart{}
	switch b.Type {
	case "image_url":
		out.Type = "image"
		if b.ImageURL != nil {
			mime, data := parseDataURL(b.ImageURL.URL)
			out.MimeType = mime
			out.Inline = data
		}
	case "text":
		out.Type = "text"
		out.Inline = b.Text
	default:
		// Unknown wire types are foreign to the matrix; treat them
		// as text per the canonical fallback rule.
		out.Type = "text"
		out.Inline = b.Text
	}
	return out, nil
}
