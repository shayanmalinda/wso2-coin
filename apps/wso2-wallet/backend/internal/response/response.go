// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Package response writes JSON HTTP responses and decodes request bodies.
package response

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxBodyBytes = 1 << 20

// Canonical error messages returned to clients.
const (
	ErrMsgUnauthorized = "You are not authorized to perform this action."
	ErrMsgForbidden    = "Access to the requested resource is forbidden."
	ErrMsgNotFound     = "The requested resource was not found."
	ErrMsgBadRequest   = "Invalid request payload."
	ErrMsgConflict     = "The resource already exists."
	ErrMsgTooLarge     = "Request body too large."
	ErrMsgInternal     = "An internal server error occurred. Please try again later."

	ErrMsgRecipientNotFound = "Recipient wallet not found."
	ErrMsgSelfTransfer      = "Cannot transfer to the same wallet."
	ErrMsgInvalidAmount     = "Invalid transfer amount."
	ErrMsgInsufficientFunds = "Insufficient funds."

	ErrMsgInvalidName      = "Wallet name must use printable characters and be at most 50 characters long."
	ErrMsgNameNotAllowed   = "Share the wallet with at least one person before naming it."
	ErrMsgInvalidGrant     = "Invalid email or access level."
	ErrMsgDomainNotAllowed = "Wallets can only be shared between people in the organization."
)

type errorBody struct {
	Message string `json:"message"`
}

// WriteError writes a JSON error envelope.
func WriteError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errorBody{Message: message})
}

// WriteJSON writes a value as a JSON success response.
func WriteJSON(w http.ResponseWriter, statusCode int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrMsgInternal)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(data)
}

// DecodeJSON reads and decodes a JSON request body, enforcing a size cap.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errTooLarge
		}
		return errBadRequest
	}
	// Reject anything after the first JSON value (e.g. two concatenated objects).
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errTooLarge
		}
		return errBadRequest
	}
	return nil
}

var (
	errBadRequest = errors.New("bad request")
	errTooLarge   = errors.New("request too large")
)

// WriteDecodeError translates a DecodeJSON error into the correct HTTP response.
func WriteDecodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, ErrMsgTooLarge)
	default:
		WriteError(w, http.StatusBadRequest, ErrMsgBadRequest)
	}
}
