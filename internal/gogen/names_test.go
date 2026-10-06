package gogen

import (
	"slices"
	"testing"
)

func TestGoName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "approve", want: "Approve"},
		{name: "release_manager", want: "ReleaseManager"},
		{name: "user_id", want: "UserID"},
		{name: "ttl", want: "TTL"},
		{name: "api_url", want: "APIURL"},
		{name: "firingFor", want: "FiringFor"},
		{name: "Tier", want: "Tier"},
		{name: "a__b", want: "AB"},
		{name: "_private", want: "Private"},
		{name: "trailing_", want: "Trailing"},
		{name: "x_2", want: "X2"},
		{name: "_1", want: "X1"},
		{name: "_", want: "X"},
		{name: "v2_api", want: "V2API"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GoName(tt.name); got != tt.want {
				t.Errorf("GoName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestPackageName(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{kind: "DeployApproval", want: "deployapproval"},
		{kind: "name_clash", want: "name_clash"},
		{kind: "Switch", want: "switch"},
	}
	for _, tt := range tests {
		if got := PackageName(tt.kind); got != tt.want {
			t.Errorf("PackageName(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestCheckPackage(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{name: "approval"},
		{name: "name_clash"},
		{name: "Approval"},
		{name: "func", wantErr: `package name "func" is a Go keyword`},
		{name: "my-pkg", wantErr: `package name "my-pkg" isn't a Go identifier`},
		{name: "1x", wantErr: `package name "1x" isn't a Go identifier`},
		{name: "", wantErr: `package name "" isn't a Go identifier`},
		{name: "_", wantErr: `package name "_" isn't a Go identifier`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPackage(tt.name)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("CheckPackage(%q) = %v, want nil", tt.name, err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Errorf("CheckPackage(%q) = %v, want %q", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestReserved(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "Service"},
		{name: "time"},
		{name: "service"},
		{name: "_", want: "_ is Go's blank identifier"},
		{name: "init", want: "init is reserved for Go's package initializers"},
		{name: "chan", want: "chan is a Go keyword"},
		{name: "error", want: "error is predeclared in Go, and the generated code uses Go's own"},
		{name: "int64", want: "int64 is predeclared in Go, and the generated code uses Go's own"},
	}
	for _, tt := range tests {
		if got := reserved(tt.name); got != tt.want {
			t.Errorf("reserved(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestClaim(t *testing.T) {
	tests := []struct {
		name   string
		taken  []string
		claims [][]string // the candidates of each claim, in order
		want   []string
	}{
		{name: "free", claims: [][]string{{"Input"}}, want: []string{"Input"}},
		{name: "first free candidate", taken: []string{"Input"}, claims: [][]string{{"Input", "GateInput"}}, want: []string{"GateInput"}},
		{name: "numbered after the last candidate", taken: []string{"Input", "GateInput"}, claims: [][]string{{"Input", "GateInput"}}, want: []string{"GateInput2"}},
		{name: "numbers skip taken ones", taken: []string{"X", "X2"}, claims: [][]string{{"X"}, {"X"}}, want: []string{"X3", "X4"}},
		{name: "earlier claims win", claims: [][]string{{"LevelLow"}, {"LevelLow"}}, want: []string{"LevelLow", "LevelLow2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := namespace{}
			for _, name := range tt.taken {
				ns[name] = true
			}
			got := make([]string, len(tt.claims))
			for i, c := range tt.claims {
				got[i] = ns.claim(c...)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("claims = %v, want %v", got, tt.want)
			}
		})
	}
}
