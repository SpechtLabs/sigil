package routing_test

import (
	"context"
	"fmt"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
)

// Compile a one-rule policy against the kind, fill in the input for one
// alert and ask the policy what to do with it. Page.Match hands back the
// page's payload as a typed PageData.
func Example() {
	p, err := routing.Kind.Compile(`
policy checkout.alerts: AlertRouting@1

when alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}
`, "checkout.alerts")
	if err != nil {
		fmt.Println(err)
		return
	}

	res, err := p.Eval(context.Background(), routing.Input{
		Alert: routing.Alert{
			Name:     "CheckoutErrorRate",
			Severity: routing.Critical,
			Labels:   map[string]string{"env": "production"},
		},
		Team: routing.Team{Name: "checkout", Oncall: "checkout-primary", Channel: "#checkout-alerts"},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	if page, ok := routing.Page.Match(res); ok {
		fmt.Println("page", page.Target, "for", res.Reason)
	}
	// Output: page checkout-primary for critical_alert
}
