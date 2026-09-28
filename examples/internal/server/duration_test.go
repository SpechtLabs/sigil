package server_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/internal/server"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "hours", in: "6h", want: 6 * time.Hour},
		{name: "chained", in: "1h30m", want: 90 * time.Minute},
		{name: "milliseconds", in: "500ms", want: 500 * time.Millisecond},
		{name: "sigil days", in: "2d", want: 48 * time.Hour},
		{name: "days and hours", in: "1d12h", want: 36 * time.Hour},
		{name: "go fractional", in: "1.5h", want: 90 * time.Minute},
		{name: "zero", in: "0s", want: 0},
		{name: "negative", in: "-15m", want: -15 * time.Minute},
		{name: "surrounding space", in: " 4h ", want: 4 * time.Hour},
		{name: "empty", in: "", wantErr: true},
		{name: "bare number", in: "30", wantErr: true},
		{name: "unknown unit", in: "6x", wantErr: true},
		{name: "fractional days", in: "1.5d", wantErr: true},
		{name: "negative after days", in: "1d-1h", wantErr: true},
		{name: "double sign", in: "--1h", wantErr: true},
		{name: "sign alone", in: "-", wantErr: true},
		{name: "plus after minus", in: "-+1h", wantErr: true},
		{name: "longest day count", in: "106751d", want: 106751 * 24 * time.Hour},
		{name: "longest duration", in: "106751d23h", want: 106751*24*time.Hour + 23*time.Hour},
		{name: "too many days", in: "106752d", wantErr: true},
		{name: "days overflow", in: "200000d", wantErr: true},
		{name: "days and clock overflow", in: "106751d24h", wantErr: true},
		{name: "day count beyond int64", in: "99999999999999999999d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := server.ParseDuration(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want an error", tt.in, got)
				}
				if len(err.Advice()) == 0 {
					t.Errorf("ParseDuration(%q) error has no advice", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDuration(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		want string
		in   time.Duration
	}{
		{name: "zero", in: 0, want: "0s"},
		{name: "minutes", in: 15 * time.Minute, want: "15m"},
		{name: "hour", in: time.Hour, want: "1h"},
		{name: "chained", in: 90 * time.Minute, want: "1h30m"},
		{name: "days", in: 50 * time.Hour, want: "2d2h"},
		{name: "milliseconds", in: 1500 * time.Millisecond, want: "1s500ms"},
		{name: "negative", in: -45 * time.Minute, want: "-45m"},
		{name: "sub-millisecond remainder", in: time.Second + 250, want: "1s250ns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := server.FormatDuration(tt.in)
			if got != tt.want {
				t.Fatalf("FormatDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
			back, err := server.ParseDuration(got)
			if err != nil {
				t.Fatalf("ParseDuration(%q) of a formatted duration: %v", got, err)
			}
			if back != tt.in {
				t.Errorf("round trip of %v gave %v", tt.in, back)
			}
		})
	}
}

func TestDurationJSON(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "string", in: `"6h"`, want: 6 * time.Hour},
		{name: "days", in: `"1d"`, want: 24 * time.Hour},
		{name: "number is refused", in: `21600`, wantErr: true},
		{name: "invalid string", in: `"soon"`, wantErr: true},
		{name: "boolean", in: `true`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d server.Duration
			err := json.Unmarshal([]byte(tt.in), &d)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Unmarshal(%s) = %v, want an error", tt.in, time.Duration(d))
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal(%s): %v", tt.in, err)
			}
			if time.Duration(d) != tt.want {
				t.Errorf("Unmarshal(%s) = %v, want %v", tt.in, time.Duration(d), tt.want)
			}
		})
	}

	out, err := json.Marshal(struct {
		Bake server.Duration `json:"bake"`
	}{Bake: server.Duration(15 * time.Minute)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, want := string(out), `{"bake":"15m"}`; got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}
