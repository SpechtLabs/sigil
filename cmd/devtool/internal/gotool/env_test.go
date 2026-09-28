package gotool

import (
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestEnvName(t *testing.T) {
	tests := []struct {
		prefix, flag string
		want         string
	}{
		{prefix: "BENCH", flag: "time", want: "BENCH_TIME"},
		{prefix: "FUZZ", flag: "no-baseline", want: "FUZZ_NO_BASELINE"},
	}
	for _, tt := range tests {
		if got := EnvName(tt.prefix, tt.flag); got != tt.want {
			t.Errorf("EnvName(%q, %q) = %q, want %q", tt.prefix, tt.flag, got, tt.want)
		}
	}
}

func TestBindEnv(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		wantCount int
		wantLong  bool
		wantErr   string
	}{
		{name: "defaults", wantCount: 2},
		{name: "environment sets a flag", env: map[string]string{"TEST_COUNT": "5"}, wantCount: 5},
		{name: "dashes become underscores", env: map[string]string{"TEST_LONG_NAME": "true"}, wantCount: 2, wantLong: true},
		{name: "a flag wins", args: []string{"--count", "7"}, env: map[string]string{"TEST_COUNT": "5"}, wantCount: 7},
		{name: "invalid value", env: map[string]string{"TEST_COUNT": "five"}, wantErr: "invalid TEST_COUNT value five"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var count int
			var long bool
			cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
			cmd.Flags().IntVar(&count, "count", 2, "Samples")
			cmd.Flags().BoolVar(&long, "long-name", false, "A long name")
			BindEnv(cmd, "TEST", func(k string) string { return tt.env[k] })

			if got := cmd.Flags().Lookup("count").Usage; got != "Samples (env TEST_COUNT)" {
				t.Errorf("usage = %q", got)
			}
			if got := cmd.Flags().Lookup("long-name").Usage; got != "A long name (env TEST_LONG_NAME)" {
				t.Errorf("usage = %q", got)
			}

			cmd.SetArgs(tt.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			checkErr(t, cmd.Execute(), tt.wantErr)
			if tt.wantErr == "" && (count != tt.wantCount || long != tt.wantLong) {
				t.Errorf("count, long-name = %d, %v; want %d, %v", count, long, tt.wantCount, tt.wantLong)
			}
		})
	}
}

func TestEnvDefaults(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    int
		wantErr string
	}{
		{name: "unset keeps the default", want: 2},
		{name: "environment sets the flag", env: map[string]string{"COUNT": "5"}, want: 5},
		{name: "flag wins over the environment", args: []string{"--count", "7"}, env: map[string]string{"COUNT": "5"}, want: 7},
		{name: "invalid value", env: map[string]string{"COUNT": "five"}, wantErr: "invalid COUNT value five"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			count := flags.Int("count", 2, "")
			if err := flags.Parse(tt.args); err != nil {
				t.Fatal(err)
			}
			err := EnvDefaults(flags, func(k string) string { return tt.env[k] }, map[string]string{"count": "COUNT"})
			checkErr(t, err, tt.wantErr)
			if err == nil && *count != tt.want {
				t.Errorf("count = %d, want %d", *count, tt.want)
			}
		})
	}
}
