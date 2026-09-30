package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
)

// BenchmarkPolicies measures the compiled team policies the HTTP service
// evaluates, including public result and trace construction, without HTTP or
// telemetry: one alert of each decision.
func BenchmarkPolicies(b *testing.B) {
	st := store.NewRouting(store.WithTeams("checkout"))
	if err := st.Load(context.Background()); err != nil {
		b.Fatal(err)
	}
	p, _ := st.Policy("checkout")
	team := routing.Team{Name: "checkout", Oncall: "checkout-primary", Channel: "#checkout-alerts"}
	alert := func(severity routing.Severity, env string, firingFor time.Duration) routing.Input {
		return routing.Input{Team: team, Alert: routing.Alert{
			Name: "CheckoutLatencyHigh", Severity: severity, Labels: map[string]string{"env": env}, FiringFor: firingFor,
		}}
	}

	b.Run("page-critical", func(b *testing.B) {
		benchmarkPolicy(b, p, alert(routing.Critical, "production", time.Minute), routing.CriticalAlert)
	})
	b.Run("page-sustained", func(b *testing.B) {
		benchmarkPolicy(b, p, alert(routing.Warning, "production", time.Hour), routing.Sustained)
	})
	b.Run("notify-routine", func(b *testing.B) {
		benchmarkPolicy(b, p, alert(routing.Warning, "production", time.Minute), routing.Routine)
	})
	b.Run("drop-staging", func(b *testing.B) {
		benchmarkPolicy(b, p, alert(routing.Warning, "staging", time.Hour), routing.NotProduction)
	})
}

func benchmarkPolicy(b *testing.B, p *policy.Policy[routing.Input], in routing.Input, want policy.Outcome) {
	ctx := context.Background()
	check := func() {
		res, err := p.Eval(ctx, in)
		if err != nil || !want.Is(res) {
			b.Errorf("unexpected evaluation: result=%+v error=%v", res, err)
		}
	}
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			check()
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				check()
			}
		})
	})
}
