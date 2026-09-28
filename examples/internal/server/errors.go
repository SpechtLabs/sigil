package server

import (
	"errors"
	"strings"

	humane "github.com/sierrasoftworks/humane-errors-go"
)

// ErrorResponse is a humane error as JSON: what went wrong, what to do about
// it, and what caused it, down the chain.
type ErrorResponse struct {
	Message string         `json:"message"`
	Advice  []string       `json:"advice,omitempty"`
	Cause   *ErrorResponse `json:"cause,omitempty"`
}

// ErrorEnvelope wraps an ErrorResponse in the `error` field, the body of
// every error status other than 422, whose body is a DecisionResponse.
type ErrorEnvelope struct {
	Error *ErrorResponse `json:"error"`
}

// NewErrorResponse renders err and its causes. A cause that isn't a humane
// error becomes a message without advice. A plain cause whose text the
// message already quotes, such as the compiler's diagnostics in a failed
// reload, is left out rather than printed twice.
func NewErrorResponse(err error) *ErrorResponse {
	if err == nil {
		return nil
	}

	var herr humane.Error
	if !errors.As(err, &herr) {
		return &ErrorResponse{Message: err.Error()}
	}

	resp := &ErrorResponse{Message: herr.Error(), Advice: herr.Advice()}
	cause := herr.Cause()
	if cause == nil {
		return resp
	}

	var causeHumane humane.Error
	if !errors.As(cause, &causeHumane) && strings.Contains(resp.Message, cause.Error()) {
		return resp
	}
	resp.Cause = NewErrorResponse(cause)
	return resp
}
