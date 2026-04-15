package handlers

import "encoding/json"

// jsonUnmarshal is a thin alias kept so ota.go does not import encoding/json
// itself (keeps imports tidy in the OTA-specific file).
func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
