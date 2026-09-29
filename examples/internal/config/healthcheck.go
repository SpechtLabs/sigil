package config

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// HealthcheckTimeout bounds one readiness probe, well inside the three
// seconds the compose healthcheck gives the whole command.
const HealthcheckTimeout = 2 * time.Second

// ReadyzURL is the readiness endpoint of a deploygate listening on addr,
// reached over the loopback interface: an address without a host, or with a
// wildcard one, listens there too, and the probe runs next to the server.
// An address with a specific host keeps it. It returns an error when addr
// has no port.
func ReadyzURL(addr string) (string, humane.Error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", humane.Wrap(err, "the listen address "+addr+" has no port",
			"set --addr or DEPLOYGATE_ADDR to host:port or :port, for example :8080")
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz", nil
}

// Probe asks url for readiness and returns nil on 200 OK, within timeout.
// Any other status, a server that doesn't answer and a timeout are errors
// with advice.
func Probe(ctx context.Context, url string, timeout time.Duration) humane.Error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return humane.Wrap(err, "building the readiness request for "+url+" failed",
			"check the listen address in --addr or DEPLOYGATE_ADDR")
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return humane.Wrap(err, "deploygate at "+url+" didn't answer",
			"check that deploygate serve is running and listens on the same --addr")
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode != http.StatusOK {
		return humane.New(fmt.Sprintf("deploygate at %s isn't ready: HTTP %d", url, resp.StatusCode),
			"wait for the first policy load, or check the deploygate logs for the policy that fails to compile")
	}
	return nil
}

// newHealthcheckCommand builds `deploygate healthcheck`, the container's
// healthcheck. The runtime image has no shell or HTTP client, so the binary
// probes itself. It prints nothing when the service is ready.
func newHealthcheckCommand() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 when the deploygate on this host is ready",
		Long: `Ask the deploygate serving on this host whether it is ready, through GET
/readyz on the loopback interface, and exit 0 on 200 OK and 1 otherwise. It
reads --addr, or DEPLOYGATE_ADDR, like serve does, so it finds the server
without extra configuration.`,
		Example: `  # In a compose file, for an image without a shell
  healthcheck:
    test: ["CMD", "/deploygate", "healthcheck"]`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if herr := bind(v, cmd); herr != nil {
				return herr
			}
			url, herr := ReadyzURL(v.GetString(keyAddr))
			if herr != nil {
				return herr
			}
			return Probe(cmd.Context(), url, HealthcheckTimeout)
		},
	}

	cmd.Flags().String(keyAddr, DefaultAddr, "listen address of the deploygate to probe, as given to serve")
	return cmd
}
