package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"

	"dschat/app/core/apierr"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ReadJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := dec.Decode(v); err != nil {
		return apierr.New(400, "BAD_REQUEST", "invalid json: "+err.Error())
	}
	return nil
}

// WriteSSE writes a single unnamed SSE message (data only).
func WriteSSE(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func WriteComment(w http.ResponseWriter, s string) {
	fmt.Fprintf(w, ": %s\n\n", s)
}
