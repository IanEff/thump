package wire

import "encoding/json"

func Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func Unmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
