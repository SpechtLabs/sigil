package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
)

// BenchmarkPolicies measures the compiled policies used by the HTTP service,
// including public result and trace construction, without HTTP or telemetry.
func BenchmarkPolicies(b *testing.B) {
	ctx := context.Background()
	a := store.NewAccess()
	d := store.NewDeploy(store.WithTeams("payments"))
	if err := a.Load(ctx); err != nil {
		b.Fatal(err)
	}
	if err := d.Load(ctx); err != nil {
		b.Fatal(err)
	}
	ap, _ := a.Policy(store.AccessRoot)
	dp, _ := d.Policy("payments")
	member := access.Input{Actor: access.Actor{Name: "ada", Groups: []string{"payments"}}, Team: "payments", Environment: "production"}
	conflict := member
	conflict.Actor.Groups = []string{"break-glass", "platform"}
	owner := deploy.Input{
		Release: deploy.Release{Soak: 6 * time.Hour},
		Service: deploy.Service{Name: "ledger", Tier: "standard", Owners: []string{"payments"}, Labels: map[string]string{
			"regions": "eu,us", "compliance": "pci", "platform.example.com/lifecycle": "ga",
			"app.kubernetes.io/managed-by": "argocd",
		}},
		Actor:       deploy.Actor{Name: "ada", Teams: []string{"payments"}, Roles: []string{"deployer"}, Regions: []string{"eu", "us"}},
		Environment: "production",
	}
	if res, err := dp.Eval(ctx, owner); err != nil || res.Reason != "service_owner" {
		b.Fatalf("owner fixture: result=%+v error=%v", res, err)
	}
	unnamed := member
	unnamed.Actor.Name = ""
	b.Run("access-member", func(b *testing.B) { benchmarkPolicy(b, ap, member, false, 2) })
	b.Run("access-conflict", func(b *testing.B) { benchmarkPolicy(b, ap, conflict, true, 0) })
	b.Run("deploy-owner", func(b *testing.B) { benchmarkPolicy(b, dp, owner, false, 1) })
	b.Run("access-assert", func(b *testing.B) { benchmarkPolicy(b, ap, unnamed, true, 0) })
}

func benchmarkPolicy[In any](b *testing.B, p *policy.Policy[In], in In, wantErr bool, outcomes int) {
	ctx := context.Background()
	check := func() {
		res, err := p.Eval(ctx, in)
		if (err != nil) != wantErr || res == nil || len(res.Outcome) != outcomes {
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
