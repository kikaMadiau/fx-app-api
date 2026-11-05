package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"fx-app-api/internal/storage"
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
			log.Printf("API handler error: %s %s: %v", r.Method, r.URL.Path, err)
			writeAPIError(w, err)
		}
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	statusCode := http.StatusInternalServerError
	message := "internal server error"
	code := ""

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		statusCode = httpErr.StatusCode
		message = httpErr.Message
		code = httpErr.Code
	} else {
		var statusErr statusCodeError
		if errors.As(err, &statusErr) {
			statusCode = statusErr.HTTPStatusCode()
			var publicErr interface{ PublicMessage() string }
			if errors.As(err, &publicErr) {
				message = publicErr.PublicMessage()
			} else {
				message = "request failed"
			}
			var publicCode interface{ PublicCode() string }
			if errors.As(err, &publicCode) {
				code = publicCode.PublicCode()
			}
		}
	}

	if statusCode == http.StatusInternalServerError {
		if sqlStatusCode, sqlMessage, ok := sqlConstraintHTTPError(err); ok {
			statusCode = sqlStatusCode
			message = sqlMessage
		}
	} else if message == "internal server error" {
		message = "request failed"
	}

	WriteJson(w, statusCode, ApiError{Message: message, Code: code})
}

func sqlConstraintHTTPError(err error) (int, string, bool) {
	code := storage.PostgresErrorCode(err)
	if code == "" {
		return 0, "", false
	}

	switch code {
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
		if strings.HasPrefix(code, "23") {
			return http.StatusBadRequest, "request violates database constraint", true
		}
	}

	return 0, "", false
}
