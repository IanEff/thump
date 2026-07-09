// Package wire is the one codec every boundary object marshals through to
// cross a broker or WAL — a single chokepoint so the wire format (JSON
// today) is a one-line change, not a search-and-replace across every
// Publisher and Subscriber.
package wire

import "encoding/json"

// Marshal encodes v in the wire format.
func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal decodes b into v, which must be a pointer, as Marshal encoded it.
func Unmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
