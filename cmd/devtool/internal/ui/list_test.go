package ui

import (
	"bytes"
	"testing"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
	"github.com/spechtlabs/sigil/cmd/internal/output"
)

func TestList(t *testing.T) {
	targets := []gotool.Target{
		{Package: "example.com/m/a", Dir: "./a", Name: "FuzzA"},
		{Package: "example.com/m/a", Dir: "./a", Name: "FuzzB"},
		{Package: "example.com/m/b", Dir: "./b", Name: "FuzzC"},
	}
	tests := []struct {
		name         string
		format       output.Format
		targets      []gotool.Target
		packagesOnly bool
		want         string
	}{
		{
			name:    "text",
			format:  output.Text,
			targets: targets,
			want:    "./a\n  FuzzA\n  FuzzB\n./b\n  FuzzC\n\n3 fuzz targets in 2 packages\n",
		},
		{
			name:    "text, one of each",
			format:  output.Text,
			targets: targets[2:],
			want:    "./b\n  FuzzC\n\n1 fuzz target in 1 package\n",
		},
		{
			name:         "text, packages only",
			format:       output.Text,
			targets:      targets,
			packagesOnly: true,
			want:         "./a\n./b\n\n3 fuzz targets in 2 packages\n",
		},
		{
			name:    "JSON",
			format:  output.JSON,
			targets: targets[:1],
			want:    `[{"package":"example.com/m/a","name":"FuzzA"}]` + "\n",
		},
		{
			name:         "JSON, packages only",
			format:       output.JSON,
			targets:      targets,
			packagesOnly: true,
			want:         `["example.com/m/a","example.com/m/b"]` + "\n",
		},
		{
			name:    "YAML",
			format:  output.YAML,
			targets: targets[:1],
			want:    "---\n- package: example.com/m/a\n  name: FuzzA\n",
		},
		{
			name:         "YAML, packages only",
			format:       output.YAML,
			targets:      targets,
			packagesOnly: true,
			want:         "---\n- example.com/m/a\n- example.com/m/b\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := List(printer(&out, false), tt.format, tt.targets, tt.packagesOnly, "fuzz target", "fuzz targets"); err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Errorf("output = %q, want %q", out.String(), tt.want)
			}
		})
	}
}
