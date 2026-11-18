package apierr

import (
	"encoding/json"
	"errors"
	"net/http"
)

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Extra carries additional JSON fields (e.g. turnId for OPEN_TURN).
	Extra map[string]any `json:"-"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func (e *Error) With(key string, value any) *Error {
	if e.Extra == nil {
		e.Extra = map[string]any{}
	}
	e.Extra[key] = value
	return e
}

// Write renders an error as {"error": {...}} plus any extra fields.
func Write(w http.ResponseWriter, err error) {
	var ae *Error
	if !errors.As(err, &ae) {
		ae = New(http.StatusInternalServerError, "INTERNAL", err.Error())
	}
	body := map[string]any{"error": ae}
	for k, v := range ae.Extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(ae.Status)
	_ = json.NewEncoder(w).Encode(body)
}
