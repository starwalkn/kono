package kono

import (
	"encoding/json"
	"net/http"
)

// ClientResponse is an output structure that wraps the final response from the gateway to the client.
type ClientResponse struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []ClientError   `json:"errors,omitempty"`
}

type ClientError string

func (err ClientError) String() string {
	return string(err)
}

const (
	ClientErrRateLimitExceeded    ClientError = "RATE_LIMIT_EXCEEDED"
	ClientErrPayloadTooLarge      ClientError = "PAYLOAD_TOO_LARGE"
	ClientErrUpstreamBodyTooLarge ClientError = "UPSTREAM_BODY_TOO_LARGE"
	ClientErrUpstreamUnavailable  ClientError = "UPSTREAM_UNAVAILABLE"
	ClientErrUpstreamError        ClientError = "UPSTREAM_ERROR"
	ClientErrUpstreamMalformed    ClientError = "UPSTREAM_MALFORMED"
	ClientErrInternal             ClientError = "INTERNAL"
	ClientErrAborted              ClientError = "ABORTED"
	ClientErrValueConflict        ClientError = "VALUE_CONFLICT"
)

func WriteError(w http.ResponseWriter, code ClientError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := ClientResponse{
		Data:   nil,
		Errors: []ClientError{code},
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Fallback on error
		http.Error(w, http.StatusText(status), status)
	}
}
