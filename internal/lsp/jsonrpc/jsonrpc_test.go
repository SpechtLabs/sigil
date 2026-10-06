package jsonrpc

import (
	"math"
	"strings"
	"testing"
)

// TestDecode decodes bodies into messages, and reports the ones that
// aren't JSON-RPC messages with the error to answer them with and the id
// to answer.
func TestDecode(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		kind   string // request, notification or response, for a body that decodes
		code   Code   // the error, for one that doesn't
		id     string // the id the message carries
		reason string // a substring of the error's message
	}{
		{name: "a request", body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, kind: "request", id: "1"},
		{name: "a request with a string id", body: `{"jsonrpc":"2.0","id":"a","method":"shutdown"}`, kind: "request", id: `"a"`},
		{name: "a notification", body: `{"jsonrpc":"2.0","method":"initialized","params":{}}`, kind: "notification"},
		{name: "a notification with a null id", body: `{"jsonrpc":"2.0","id":null,"method":"exit"}`, kind: "notification", id: "null"},
		{name: "a response", body: `{"jsonrpc":"2.0","id":7,"result":null}`, kind: "response", id: "7"},
		{name: "an error response", body: `{"jsonrpc":"2.0","id":7,"error":{"code":-1,"message":"x"}}`, kind: "response", id: "7"},
		{name: "not JSON", body: `{"jsonrpc":`, code: ParseError, reason: "isn't valid JSON"},
		{name: "empty", body: ``, code: ParseError},
		{name: "a batch", body: `[{"jsonrpc":"2.0","method":"x"}]`, code: InvalidRequest, reason: "batches"},
		{name: "a number", body: `42`, code: InvalidRequest, reason: "not a JSON-RPC message"},
		{name: "a method that isn't a string", body: `{"jsonrpc":"2.0","id":3,"method":1}`, code: InvalidRequest, id: "3", reason: "not a JSON-RPC message"},
		{name: "another version", body: `{"jsonrpc":"1.0","id":2,"method":"x"}`, code: InvalidRequest, id: "2", reason: `"jsonrpc": "2.0"`},
		{name: "no version", body: `{"id":2,"method":"x"}`, code: InvalidRequest, id: "2"},
		{name: "an object id", body: `{"jsonrpc":"2.0","id":{},"method":"x"}`, code: InvalidRequest, reason: "a number or a string"},
		{name: "an object id among bad fields", body: `{"jsonrpc":"2.0","id":{},"method":1}`, code: InvalidRequest},
		{name: "neither a method nor a result", body: `{"jsonrpc":"2.0","id":4}`, code: InvalidRequest, id: "4", reason: "has a method"},
		{name: "a response without an id", body: `{"jsonrpc":"2.0","result":1}`, code: InvalidRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, bad := Decode([]byte(tt.body))
			if string(m.ID) != tt.id {
				t.Errorf("id = %s, want %s", m.ID, tt.id)
			}
			if tt.kind == "" {
				if bad == nil || bad.Code != tt.code || !strings.Contains(bad.Message, tt.reason) {
					t.Errorf("Decode() error = %+v, want code %d holding %q", bad, tt.code, tt.reason)
				}
				return
			}
			if bad != nil {
				t.Fatalf("Decode() error = %s", bad.Message)
			}
			kind := "response"
			switch {
			case m.IsRequest():
				kind = "request"
			case m.IsNotification():
				kind = "notification"
			}
			if kind != tt.kind {
				t.Errorf("decoded a %s, want a %s", kind, tt.kind)
			}
		})
	}
}

// TestResponseOfWhatCantBeEncoded checks that a result JSON can't hold
// becomes an InternalError, and params it can't hold are left out.
func TestResponseOfWhatCantBeEncoded(t *testing.T) {
	m := Response([]byte("1"), math.Inf(1))
	if m.Error == nil || m.Error.Code != InternalError || m.Result != nil {
		t.Errorf("Response() = %+v, want an InternalError", m)
	}
	if n := Notification("x", math.NaN()); n.Params != nil {
		t.Errorf("Notification() params = %s, want none", n.Params)
	}
}
