package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lib/pq"
)

func WriteJson(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(value)
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}

	return e.Message
}

func (e *HTTPError) HTTPStatusCode() int {
	return e.StatusCode
}

func MakeHttpHandleFunc(f ApiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			writeAPIError(w, err)
		}
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	message := err.Error()

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		statusCode = httpErr.StatusCode
		message = httpErr.Error()
	} else {
		var statusErr statusCodeError
		if errors.As(err, &statusErr) {
			statusCode = statusErr.HTTPStatusCode()
			message = statusErr.Error()
		}
	}

	if statusCode == http.StatusInternalServerError {
		if sqlStatusCode, sqlMessage, ok := sqlConstraintHTTPError(err); ok {
			statusCode = sqlStatusCode
			message = sqlMessage
		}
	}

	WriteJson(w, statusCode, ApiError{Message: message})
}

func sqlConstraintHTTPError(err error) (int, string, bool) {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) {
		return 0, "", false
	}

	switch pqErr.Code {
	case "23505":
		return http.StatusConflict, "duplicate value violates unique constraint", true
	case "23503":
		return http.StatusBadRequest, "referenced resource does not exist", true
	case "23502":
		return http.StatusBadRequest, "required field is missing", true
	case "23514":
		return http.StatusBadRequest, "request violates check constraint", true
	case "23P01":
		return http.StatusConflict, "request conflicts with an existing record", true
	default:
		if strings.HasPrefix(string(pqErr.Code), "23") {
			return http.StatusBadRequest, "request violates database constraint", true
		}
	}

	return 0, "", false
}
