package middleware

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	MaxSubmitBytes = 1 << 20
	MaxSubmitItems = 1000
)

// DecodeSubmission consumes exactly one JSON value, including trailing bytes,
// so a small JSON prefix cannot bypass the total request size limit.
func DecodeSubmission(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxSubmitBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("request must contain one JSON value")
	}
	return nil
}
