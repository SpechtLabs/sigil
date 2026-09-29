package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/config"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/server"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
)

// telemetryFlushTimeout bounds flushing the last spans on exit, separately
// from the server's shutdown budget, which may be spent by then.
const telemetryFlushTimeout = 5 * time.Second

// serve runs the service until SIGINT or SIGTERM: telemetry first, so every
// later step is logged and traced, then the startup, then the reload watcher
// and the HTTP server.
func serve(ctx context.Context, cfg config.Config) (err error) {
	if cfg.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	tel, herr := telemetry.Setup(telemetry.Config{Version: version, LogFormat: cfg.LogFormat, Debug: cfg.Debug})
	if herr != nil {
		return herr
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), telemetryFlushTimeout)
		defer cancel()
		if ferr := tel.Shutdown(flushCtx); ferr != nil && err == nil {
			err = ferr
		}
	}()

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// signal.Notify delivers every SIGHUP to each channel, so each store
	// gets its own and both reload.
	deployHUP := make(chan os.Signal, 1)
	accessHUP := make(chan os.Signal, 1)
	signal.Notify(deployHUP, syscall.SIGHUP)
	signal.Notify(accessHUP, syscall.SIGHUP)
	defer signal.Stop(deployHUP)
	defer signal.Stop(accessHUP)

	svc, herr := start(ctx, cfg)
	if herr != nil {
		return herr
	}

	var wg sync.WaitGroup
	wg.Go(func() { svc.deploy.Watch(ctx, cfg.ReloadInterval, deployHUP) })
	wg.Go(func() { svc.access.Watch(ctx, cfg.ReloadInterval, accessHUP) })
	defer wg.Wait()

	serr := svc.server.Serve(ctx)
	stop() // stops the watchers, so the deferred Wait returns
	return serr
}

// service is what start builds: both policy stores and the server that
// evaluates against them.
type service struct {
	deploy *store.Store[deploy.Input]
	access *store.Store[access.Input]
	server *server.Server
}

// start builds the stores and the server in a startup span, so the initial
// policy loads show up in the traces as part of it. An initial load that
// fails fails the process: serving without a policy would deny every
// deploy, and a pod that exits never becomes ready, so a rollout with a
// broken bundle stalls on the old pods instead.
func start(ctx context.Context, cfg config.Config) (*service, humane.Error) {
	ctx, span := otel.Tracer(telemetry.TracerName).Start(ctx, "deploygate.startup")
	defer span.End()

	teamSource, accessSource := sourceName(cfg.PoliciesDir), sourceName(cfg.AccessPoliciesDir)
	span.SetAttributes(
		attribute.String("deploygate.version", version),
		attribute.String("deploygate.addr", cfg.Addr),
		attribute.StringSlice("deploygate.teams", cfg.Teams),
		attribute.String("sigil.source", teamSource),
		attribute.String("sigil.access_source", accessSource),
	)

	metrics := telemetry.NewMetrics()
	deployOpts := []store.Option{store.WithTeams(cfg.Teams...), store.WithMetrics(metrics)}
	if cfg.PoliciesDir != "" {
		deployOpts = append(deployOpts, store.WithBundleDir(cfg.PoliciesDir))
	}
	accessOpts := []store.Option{store.WithMetrics(metrics)}
	if cfg.AccessPoliciesDir != "" {
		accessOpts = append(accessOpts, store.WithBundleDir(cfg.AccessPoliciesDir))
	}
	svc := &service{deploy: store.NewDeploy(deployOpts...), access: store.NewAccess(accessOpts...)}

	if lerr := svc.deploy.InitialLoad(ctx); lerr != nil {
		span.SetStatus(codes.Error, "initial policy load failed")
		return nil, humane.Wrap(lerr, "deploygate won't start without team policies that load",
			"fix the team policies, or point --policies at a directory that loads")
	}
	if lerr := svc.access.InitialLoad(ctx); lerr != nil {
		span.SetStatus(codes.Error, "initial policy load failed")
		return nil, humane.Wrap(lerr, "deploygate won't start without access policies that load",
			"fix the access policies, or point --access-policies at a directory that loads")
	}

	srv, herr := server.New(
		server.WithStore(svc.deploy),
		server.WithAccessStore(svc.access),
		server.WithMetrics(metrics),
		server.WithAddr(cfg.Addr),
		server.WithShutdownTimeout(cfg.ShutdownTimeout),
		server.WithEvaluationTimeout(cfg.EvaluationTimeout),
	)
	if herr != nil {
		span.SetStatus(codes.Error, "building the server failed")
		return nil, herr
	}
	svc.server = srv

	telemetry.FromContext(ctx).InfoContext(ctx, "deploygate started",
		zap.String("version", version),
		zap.String("addr", cfg.Addr),
		zap.Strings("teams", cfg.Teams),
		zap.String("policies", teamSource),
		zap.String("access_policies", accessSource),
		zap.Duration("reload_interval", cfg.ReloadInterval),
		zap.Duration("evaluation_timeout", cfg.EvaluationTimeout),
	)
	return svc, nil
}

// sourceName names a bundle's source the way the store does.
func sourceName(dir string) string {
	if dir == "" {
		return store.SourceEmbedded
	}
	return dir
}
