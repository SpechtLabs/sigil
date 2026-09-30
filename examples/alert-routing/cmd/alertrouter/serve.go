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

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/config"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/dispatch"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/server"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/teams"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
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

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	svc, herr := start(ctx, cfg)
	if herr != nil {
		return herr
	}

	var wg sync.WaitGroup
	wg.Go(func() { svc.store.Watch(ctx, cfg.ReloadInterval, hup) })
	defer wg.Wait()

	serr := svc.server.Serve(ctx)
	stop() // stops the watcher, so the deferred Wait returns
	return serr
}

// service is what start builds: the policy store and the server that routes
// against it.
type service struct {
	store  *store.Store[routing.Input]
	server *server.Server
}

// start loads the team directory and builds the store and the server in a
// startup span, so the initial policy load shows up in the traces as part of
// it. An initial load that fails fails the process: serving without a policy
// would route every alert to the default channel, and a pod that exits never
// becomes ready, so a rollout with a broken bundle stalls on the old pods
// instead.
func start(ctx context.Context, cfg config.Config) (*service, humane.Error) {
	ctx, span := otel.Tracer(telemetry.TracerName).Start(ctx, "alertrouter.startup")
	defer span.End()

	source := sourceName(cfg.PoliciesDir)
	span.SetAttributes(
		attribute.String("alertrouter.version", version),
		attribute.String("alertrouter.addr", cfg.Addr),
		attribute.String("sigil.source", source),
	)

	dir, herr := loadDirectory(cfg.TeamsFile)
	if herr != nil {
		span.SetStatus(codes.Error, "loading the team directory failed")
		return nil, humane.Wrap(herr, "alertrouter won't start without a team directory that loads",
			"fix the team directory, or leave --teams-file empty for the one embedded in the binary")
	}
	span.SetAttributes(
		attribute.StringSlice("alertrouter.teams", dir.Names()),
		attribute.String("alertrouter.teams_source", dir.Source()),
	)

	metrics := telemetry.NewMetrics()
	opts := []store.Option{store.WithTeams(dir.Names()...), store.WithMetrics(metrics)}
	if cfg.PoliciesDir != "" {
		opts = append(opts, store.WithBundleDir(cfg.PoliciesDir))
	}
	svc := &service{store: store.NewRouting(opts...)}

	if lerr := svc.store.InitialLoad(ctx); lerr != nil {
		span.SetStatus(codes.Error, "initial policy load failed")
		return nil, humane.Wrap(lerr, "alertrouter won't start without team policies that load",
			"fix the team policies, or point --policies at a directory that loads",
			"every team in the team directory needs a policy <team>.alerts that invokes platform.paging")
	}

	srv, herr := server.New(
		server.WithStore(svc.store),
		server.WithDirectory(dir),
		server.WithNotifier(dispatch.NewLogNotifier()),
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

	telemetry.FromContext(ctx).InfoContext(ctx, "alertrouter started",
		zap.String("version", version),
		zap.String("addr", cfg.Addr),
		zap.Strings("teams", dir.Names()),
		zap.String("teams_source", dir.Source()),
		zap.String("policies", source),
		zap.Duration("reload_interval", cfg.ReloadInterval),
		zap.Duration("evaluation_timeout", cfg.EvaluationTimeout),
	)
	return svc, nil
}

// loadDirectory loads the team directory from path, or the one embedded in
// the binary when path is empty.
func loadDirectory(path string) (*teams.Directory, humane.Error) {
	if path == "" {
		return teams.Default(), nil
	}
	return teams.LoadFile(path)
}

// sourceName names the bundle's source the way the store does.
func sourceName(dir string) string {
	if dir == "" {
		return store.SourceEmbedded
	}
	return dir
}
