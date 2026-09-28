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

	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	st, srv, herr := start(ctx, cfg)
	if herr != nil {
		return herr
	}

	var wg sync.WaitGroup
	wg.Go(func() { st.Watch(ctx, cfg.ReloadInterval, sighup) })
	defer wg.Wait()

	serr := srv.Serve(ctx)
	stop() // stops the watcher, so the deferred Wait returns
	return serr
}

// start builds the store and the server in a startup span, so the initial
// policy load shows up in the traces as part of it. The initial load fails
// the process: serving without a policy would deny every deploy, and a pod
// that exits never becomes ready, so a rollout with a broken bundle stalls
// on the old pods instead.
func start(ctx context.Context, cfg config.Config) (*store.Store, *server.Server, humane.Error) {
	ctx, span := otel.Tracer(telemetry.TracerName).Start(ctx, "deploygate.startup")
	defer span.End()

	source := store.SourceEmbedded
	if cfg.PoliciesDir != "" {
		source = cfg.PoliciesDir
	}
	span.SetAttributes(
		attribute.String("deploygate.version", version),
		attribute.String("deploygate.addr", cfg.Addr),
		attribute.StringSlice("deploygate.teams", cfg.Teams),
		attribute.String("sigil.source", source),
	)

	metrics := telemetry.NewMetrics()
	storeOpts := []store.Option{store.WithTeams(cfg.Teams...), store.WithMetrics(metrics)}
	if cfg.PoliciesDir != "" {
		storeOpts = append(storeOpts, store.WithTeamsDir(cfg.PoliciesDir))
	}
	st := store.New(deploy.Kind, storeOpts...)

	if lerr := st.InitialLoad(ctx); lerr != nil {
		span.SetStatus(codes.Error, "initial policy load failed")
		return nil, nil, humane.Wrap(lerr, "deploygate won't start without a policy bundle that loads",
			"fix the team policies, or point --policies at a directory that loads")
	}

	srv, herr := server.New(
		server.WithStore(st),
		server.WithMetrics(metrics),
		server.WithAddr(cfg.Addr),
		server.WithShutdownTimeout(cfg.ShutdownTimeout),
	)
	if herr != nil {
		span.SetStatus(codes.Error, "building the server failed")
		return nil, nil, herr
	}

	telemetry.FromContext(ctx).InfoContext(ctx, "deploygate started",
		zap.String("version", version),
		zap.String("addr", cfg.Addr),
		zap.Strings("teams", cfg.Teams),
		zap.String("policies", source),
		zap.Duration("reload_interval", cfg.ReloadInterval),
	)
	return st, srv, nil
}
