// SPDX-License-Identifier: MIT

package golden

import (
	"bytes"
	"encoding/json"
	"sort"
)

// jsonUnmarshal is a thin wrapper so the public surface doesn't
// depend on `encoding/json` at the import boundary (keeps the
// golden.go file readable).
func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

// jsonMarshalIndentSorted re-encodes a JSON document with sorted
// object keys at every level and 2-space indentation. This is the
// canonical form used by AssertJSON to neutralise map-iteration
// nondeterminism.
func jsonMarshalIndentSorted(v any) ([]byte, error) {
	canon := canonicalize(v)
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canon); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// canonicalize walks v, replacing every map[string]any with a
// sorted-key alternative that the json encoder will preserve.
func canonicalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make([]kv, 0, len(keys))
		for _, k := range keys {
			out = append(out, kv{k, canonicalize(x[k])})
		}
		return sortedMap(out)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonicalize(e)
		}
		return out
	default:
		return v
	}
}

type kv struct {
	K string
	V any
}

// sortedMap is a []kv that marshals as a JSON object preserving the
// slice order.
type sortedMap []kv

func (s sortedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range s {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(e.K)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(e.V)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
