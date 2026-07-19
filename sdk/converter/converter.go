// Package converter serializes workflow and activity payloads. Chronos uses
// JSON so payloads are human-readable in the inspector and language-neutral on
// the wire.
package converter

import "encoding/json"

// Encode serializes a value to bytes. A nil value encodes to nil bytes.
func Encode(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// Decode deserializes bytes into out. Empty input is a no-op so callers can
// decode absent payloads safely.
func Decode(data []byte, out any) error {
	if len(data) == 0 || out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
