package server

import "net/http"

type apiOperationFailure struct {
	status int
	body   any
}

func apiOperationError(status int, message string) *apiOperationFailure {
	return &apiOperationFailure{
		status: status,
		body:   map[string]string{"error": message},
	}
}

func (failure *apiOperationFailure) write(w http.ResponseWriter) {
	writeJSON(w, failure.status, failure.body)
}
