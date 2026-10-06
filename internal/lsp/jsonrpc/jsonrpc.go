// Package jsonrpc reads and writes the JSON-RPC 2.0 messages of the
// Language Server Protocol's base protocol: each message is a JSON body
// after a header of `Content-Length: N` lines and a blank line, sent over
// a byte stream such as a language server's stdin and stdout.
//
// A [Reader] reads one body at a time and [Decode] turns it into a
// [Message], saying what's wrong with one that isn't a JSON-RPC message
// as the [Error] to answer with. A [Writer] writes one message at a time,
// safe for concurrent use. The package knows nothing of LSP's methods;
// package lsp dispatches them.
//
// The base protocol is specified at
// https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#baseProtocol.
package jsonrpc

import (
	"bytes"
	"encoding/json"
)

// Version is the only JSON-RPC version a message may name.
const Version = "2.0"

// null is JSON's null, the result of a response that has none and the id
// of one that answers a message without a readable id.
var null = json.RawMessage("null")

// Code is a JSON-RPC error code.
type Code int

// The error codes the server answers with: JSON-RPC's own, and the ones
// LSP adds.
const (
	ParseError     Code = -32700 // the body isn't JSON
	InvalidRequest Code = -32600 // JSON that isn't a JSON-RPC request, or a request the server can't take now
	MethodNotFound Code = -32601 // a request for a method the server doesn't implement
	InvalidParams  Code = -32602 // params the method can't read
	InternalError  Code = -32603 // the server failed answering

	// ServerNotInitialized answers a request sent before initialize.
	ServerNotInitialized Code = -32002
	// RequestFailed answers a request the server understood but couldn't
	// carry out, such as formatting a file that doesn't parse.
	RequestFailed Code = -32803
	// RequestCanceled answers a request the client canceled before the
	// server got to it.
	RequestCanceled Code = -32800
)

// Message is one JSON-RPC message: a request (Method and ID), a
// notification (Method alone) or a response (ID, and Result or Error).
// Params and Result stay raw, for the method's handler to decode.
type Message struct {
	Error   *Error          `json:"error,omitempty"` // a failed response's error
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`     // a number or a string; nil for a notification, `null` for a response to a message without one
	Params  json.RawMessage `json:"params,omitempty"` // a request's or notification's arguments
	Result  json.RawMessage `json:"result,omitempty"` // a successful response's value, `null` when it has none
}

// Error is the error a response carries.
type Error struct {
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
	Code    Code            `json:"code"`
}

// Response returns the response to the request with id that carries
// result. A result that can't be encoded makes it an InternalError
// response instead, which says why.
func Response(id json.RawMessage, result any) *Message {
	raw, err := json.Marshal(result)
	if err != nil {
		return Failure(id, InternalError, "the result couldn't be encoded: "+err.Error())
	}
	return &Message{JSONRPC: Version, ID: responseID(id), Result: raw}
}

// Failure returns the response to the request with id that fails with
// code and msg. An id that's nil, for a message whose id couldn't be
// read, becomes `null`, as JSON-RPC asks.
func Failure(id json.RawMessage, code Code, msg string) *Message {
	return &Message{JSONRPC: Version, ID: responseID(id), Error: &Error{Code: code, Message: msg}}
}

// Request returns the request of method with params, under id. Params
// that can't be encoded are left out.
func Request(id json.RawMessage, method string, params any) *Message {
	m := Notification(method, params)
	m.ID = id
	return m
}

// Notification returns the notification of method with params. Params
// that can't be encoded are left out.
func Notification(method string, params any) *Message {
	raw, err := json.Marshal(params)
	if err != nil {
		raw = nil
	}
	return &Message{JSONRPC: Version, Method: method, Params: raw}
}

// IsRequest reports whether m is a request: a method and an id.
func (m *Message) IsRequest() bool { return m.Method != "" && hasID(m.ID) }

// IsNotification reports whether m is a notification: a method and no id.
func (m *Message) IsNotification() bool { return m.Method != "" && !hasID(m.ID) }

// Decode parses body as one message. A body that isn't JSON is a
// ParseError. JSON that isn't one JSON-RPC 2.0 request, notification or
// response is an InvalidRequest: a batch, which LSP doesn't use, an object
// with another version, an id that's neither a number nor a string, an
// object with neither a method nor a result or error, or a result without
// an id; an error without one answers a message that had none. The message
// returned with an InvalidRequest holds the id when it could be read, for
// the error response to answer it.
func Decode(body []byte) (*Message, *Error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' && json.Valid(trimmed) {
		return &Message{}, &Error{Code: InvalidRequest, Message: "batches aren't supported; send one message at a time"}
	}
	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		if json.Valid(body) {
			// Valid JSON of the wrong shape, such as a number, or a field
			// of the wrong type.
			return &Message{ID: idOf(body)}, &Error{Code: InvalidRequest, Message: "not a JSON-RPC message: " + err.Error()}
		}
		return &Message{}, &Error{Code: ParseError, Message: "the message isn't valid JSON: " + err.Error()}
	}
	switch {
	case !validID(m.ID):
		return &Message{}, &Error{Code: InvalidRequest, Message: "a message id is a number or a string"}
	case m.JSONRPC != Version:
		return &Message{ID: m.ID}, &Error{Code: InvalidRequest, Message: `a message must have "jsonrpc": "2.0"`}
	case m.Method == "" && (m.Result == nil && m.Error == nil || m.Result != nil && !hasID(m.ID)):
		return &Message{ID: m.ID}, &Error{Code: InvalidRequest, Message: "a message has a method, or answers a request with a result or an error"}
	}
	return &m, nil
}

// hasID reports whether id names a request: it's there and isn't null.
func hasID(id json.RawMessage) bool {
	return len(id) > 0 && !bytes.Equal(id, null)
}

// validID reports whether id is absent, null, a number or a string.
func validID(id json.RawMessage) bool {
	if !hasID(id) {
		return true
	}
	var v any
	if err := json.Unmarshal(id, &v); err != nil {
		return false
	}
	switch v.(type) {
	case float64, string:
		return true
	}
	return false
}

// idOf reads the id of a JSON object whose other fields don't decode, so
// the error response can answer it, or returns nil.
func idOf(body []byte) json.RawMessage {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(body, &probe) != nil || !validID(probe.ID) {
		return nil
	}
	return probe.ID
}

// responseID is the id a response carries: the request's, or null.
func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return null
	}
	return id
}
