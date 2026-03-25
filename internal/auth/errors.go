package auth

import (
	"encoding/json"
	"net/http"

	"github.com/formation-res/open-rtls-hub/internal/httpapi/gen"
)

type authError struct {
	status    int
	typ       string
	message   string
	challenge string
}

func (e *authError) Error() string { return e.message }
func (e *authError) Status() int   { return e.status }
func (e *authError) Type() string  { return e.typ }
func (e *authError) Message() string {
	return e.message
}

func unauthorized(message string) error {
	return &authError{
		status:    http.StatusUnauthorized,
		typ:       "authentication_failed",
		message:   message,
		challenge: `Bearer realm="open-rtls-hub"`,
	}
}

func forbidden(message string) error {
	return &authError{
		status:  http.StatusForbidden,
		typ:     "authorization_failed",
		message: message,
	}
}

func writeAuthError(w http.ResponseWriter, err error) {
	authErr, ok := err.(*authError)
	if !ok {
		authErr = &authError{
			status:  http.StatusUnauthorized,
			typ:     "authentication_failed",
			message: "authentication failed",
		}
	}
	if authErr.challenge != "" {
		w.Header().Set("WWW-Authenticate", authErr.challenge)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(authErr.status)
	msg := authErr.message
	_ = json.NewEncoder(w).Encode(gen.ErrorResponse{
		Type:    authErr.typ,
		Code:    authErr.status,
		Message: &msg,
	})
}
