package freeze

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
)

// Defaults of the OFREP source's options.
const (
	// DefaultFlag is the key of the flag that lists the frozen environments.
	DefaultFlag = "change-freeze"
	// DefaultTargetingKey is the evaluation context's targetingKey, which
	// OFREP requires and a flag service may hash or target on.
	DefaultTargetingKey = "deploygate"
	// DefaultRefreshInterval is how often the flag is evaluated.
	DefaultRefreshInterval = 15 * time.Second
	// DefaultMaxStaleness is how old the last answer may get before the
	// freeze is unknown: four missed refreshes at the default interval.
	DefaultMaxStaleness = time.Minute
)

// evaluatePath is OFREP's single-flag evaluation endpoint; the flag key
// follows it.
const evaluatePath = "/ofrep/v1/evaluate/flags/"

// maxResponseBytes caps an evaluation's body. One flag's answer is a few
// hundred bytes; the cap keeps a misbehaving server from making deploygate
// buffer megabytes every refresh.
const maxResponseBytes = 64 << 10

// reasonError is the OFREP reason of an evaluation that failed on the
// server, which still answers 200 with the flag's default value. That value
// says nothing about a freeze, so it counts as a failed refresh.
const reasonError = "ERROR"

// OFREP is a [Source] that evaluates a flag on an OpenFeature Remote
// Evaluation Protocol service, such as flagd. The
// flag's value lists the frozen environments: a JSON array of strings, or a
// string of comma-separated names, which is what a flag service with only
// boolean and string flags can hold. An empty list or an empty string means
// nothing is frozen.
//
// [OFREP.Refresh] evaluates the flag once and [OFREP.Run] keeps doing so in
// the background; [OFREP.Freeze] answers from the last successful
// evaluation, so a request never waits on the flag service. A failed
// refresh keeps the last answer, until it is older than the maximum
// staleness: then the freeze is unknown, and so it is before the first
// successful refresh. It is safe for concurrent use.
type OFREP struct {
	client *http.Client
	clock  Clock
	tracer trace.Tracer
	last   atomic.Pointer[answer]

	base         string
	endpoint     string
	flag         string
	body         []byte
	interval     time.Duration
	maxStaleness time.Duration
}

// Option configures an [OFREP] source. Options are applied in order by
// [NewOFREP], so a later option overrides an earlier one.
type Option func(*ofrepConfig)

// ofrepConfig is what the options set, checked by NewOFREP.
type ofrepConfig struct {
	client       *http.Client
	clock        Clock
	tracer       trace.Tracer
	context      map[string]string
	flag         string
	interval     time.Duration
	maxStaleness time.Duration
}

// answer is the last successful evaluation: when it arrived and the
// environments it named.
type answer struct {
	at           time.Time
	environments []string
}

// evaluation is OFREP's answer for one flag. A successful evaluation sets
// Value and Reason; a failed one, ErrorCode and ErrorDetails, with a 4xx or
// 5xx status or, for an error in the flag's own evaluation, with 200 and
// the reason ERROR.
type evaluation struct {
	Key          string          `json:"key"`
	Reason       string          `json:"reason"`
	Variant      string          `json:"variant"`
	ErrorCode    string          `json:"errorCode"`
	ErrorDetails string          `json:"errorDetails"`
	Value        json.RawMessage `json:"value"`
}

// WithFlag sets the key of the flag to evaluate. The default is
// [DefaultFlag].
func WithFlag(key string) Option {
	return func(c *ofrepConfig) {
		c.flag = key
	}
}

// WithEvaluationContext adds attributes to the evaluation context the flag
// is evaluated with, next to the targetingKey, which it may override. A flag
// service that targets on attributes, such as a region, needs
// them to evaluate the flag the way its policy expects.
func WithEvaluationContext(attrs map[string]string) Option {
	return func(c *ofrepConfig) {
		maps.Copy(c.context, attrs)
	}
}

// WithRefreshInterval sets how often [OFREP.Run] evaluates the flag, which
// also bounds each evaluation's request. The default is
// [DefaultRefreshInterval].
func WithRefreshInterval(d time.Duration) Option {
	return func(c *ofrepConfig) {
		c.interval = d
	}
}

// WithMaxStaleness sets how old the last answer may get before the freeze is
// unknown. It must be longer than the refresh interval, or the freeze would
// turn unknown between two refreshes that both succeed. The default is
// [DefaultMaxStaleness].
func WithMaxStaleness(d time.Duration) Option {
	return func(c *ofrepConfig) {
		c.maxStaleness = d
	}
}

// WithHTTPClient sends the evaluations through client instead of a plain
// http.Client whose timeout is the refresh interval. Each evaluation is
// bounded by the refresh interval either way.
func WithHTTPClient(client *http.Client) Option {
	return func(c *ofrepConfig) {
		c.client = client
	}
}

// WithClock reads time from clock instead of the wall clock, which is how a
// test ages an answer or fires a refresh.
func WithClock(clock Clock) Option {
	return func(c *ofrepConfig) {
		c.clock = clock
	}
}

// WithTracer starts the refresh spans on tracer instead of the global
// provider's.
func WithTracer(tracer trace.Tracer) Option {
	return func(c *ofrepConfig) {
		c.tracer = tracer
	}
}

// NewOFREP returns a source that evaluates a flag on the OFREP service at
// baseURL, such as http://flagd:8016. It sends no request: call
// [OFREP.Refresh] for the first answer and [OFREP.Run] for the rest. Until
// the first refresh succeeds, the freeze is unknown. It returns an error
// for a base URL that isn't an absolute http or https URL, an empty flag
// key, a refresh interval that isn't positive, and a maximum staleness no
// longer than the refresh interval.
func NewOFREP(baseURL string, opts ...Option) (*OFREP, humane.Error) {
	cfg := ofrepConfig{
		context:      map[string]string{"targetingKey": DefaultTargetingKey},
		flag:         DefaultFlag,
		interval:     DefaultRefreshInterval,
		maxStaleness: DefaultMaxStaleness,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	base, herr := parseBase(baseURL)
	if herr != nil {
		return nil, herr
	}
	if cfg.flag == "" {
		return nil, humane.New("the freeze flag key is empty", "set the key of the flag that lists the frozen environments, such as "+DefaultFlag)
	}
	if cfg.interval <= 0 {
		return nil, humane.New("the freeze refresh interval "+cfg.interval.String()+" isn't positive",
			"set it to how often the flag service is asked, such as "+DefaultRefreshInterval.String())
	}
	if cfg.maxStaleness <= cfg.interval {
		return nil, humane.New(fmt.Sprintf("the freeze's maximum staleness %s isn't longer than its refresh interval %s", cfg.maxStaleness, cfg.interval),
			"set it to a few refresh intervals, so one slow or failed refresh doesn't freeze every environment")
	}

	// A map of strings always encodes, so the error is always nil.
	body, _ := json.Marshal(map[string]map[string]string{"context": cfg.context})

	o := &OFREP{
		client:       cfg.client,
		clock:        cfg.clock,
		tracer:       cfg.tracer,
		base:         base,
		endpoint:     base + evaluatePath + url.PathEscape(cfg.flag),
		flag:         cfg.flag,
		body:         body,
		interval:     cfg.interval,
		maxStaleness: cfg.maxStaleness,
	}
	if o.client == nil {
		o.client = &http.Client{Timeout: o.interval}
	}
	if o.clock == nil {
		o.clock = WallClock{}
	}
	if o.tracer == nil {
		o.tracer = otel.Tracer(telemetry.TracerName)
	}
	return o, nil
}

// Freeze returns the environments of the last successful evaluation. Before
// the first one, and once the last is older than the maximum staleness, the
// freeze is unknown; it then still lists the environments last heard of, if
// any, for the log and the trace.
func (o *OFREP) Freeze() deploy.Freeze {
	last := o.last.Load()
	if last == nil {
		return deploy.Freeze{Environments: []string{}, Unknown: true}
	}
	return deploy.Freeze{
		Environments: slices.Clone(last.environments),
		Unknown:      o.clock.Now().Sub(last.at) > o.maxStaleness,
	}
}

// Refresh evaluates the flag once, in a span of its own, and keeps the
// answer when it is one. A failed evaluation, an answer that isn't a list
// of environments included, leaves the last answer in place to age, and
// returns why.
func (o *OFREP) Refresh(ctx context.Context) humane.Error {
	ctx, span := o.tracer.Start(ctx, "deploygate.freeze.refresh", trace.WithAttributes(
		attribute.String("freeze.flag", o.flag),
		attribute.String("freeze.source", o.base),
	))
	defer span.End()

	environments, herr := o.evaluate(ctx)
	if herr != nil {
		span.RecordError(herr)
		span.SetStatus(codes.Error, herr.Error())
		return herr
	}
	o.last.Store(&answer{at: o.clock.Now(), environments: environments})
	span.SetAttributes(attribute.StringSlice("sigil.freeze.environments", environments))
	span.SetStatus(codes.Ok, "")
	return nil
}

// Run refreshes the freeze every refresh interval until ctx ends. It logs a
// failed refresh as a warning while the last answer is fresh enough, as an
// error once the freeze is unknown, and the first success after a failure.
// It doesn't refresh when it starts; call [OFREP.Refresh] first. It returns
// when ctx ends, which is how it is stopped.
//
//nolint:lifecycle // the context is the stop mechanism, as for Server.Serve; a Stop method would be a second way to do the same
func (o *OFREP) Run(ctx context.Context) {
	tick, stop := o.clock.Tick(o.interval)
	defer stop()

	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			failing = o.refreshAndLog(ctx, failing)
		}
	}
}

// refreshAndLog refreshes once and logs the outcome in one line; see Run.
// failing is whether the refresh before this one failed, and it returns
// whether this one did. A success after a success logs nothing, and a
// refresh the ending context cut short neither logs nor counts.
func (o *OFREP) refreshAndLog(ctx context.Context, failing bool) bool {
	herr := o.Refresh(ctx)
	if herr != nil && ctx.Err() != nil {
		return failing
	}
	if herr == nil && !failing {
		return false
	}

	current := o.Freeze()
	lvl, msg := zapcore.InfoLevel, "the change freeze is known again"
	var advice []string
	switch {
	case herr != nil && current.Unknown:
		lvl, msg = zapcore.ErrorLevel, "the change freeze is unknown, every deploy is denied as frozen until the flag service answers"
		advice = herr.Advice()
	case herr != nil:
		lvl, msg = zapcore.WarnLevel, "refreshing the change freeze failed, keeping the last answer"
		advice = herr.Advice()
	}
	telemetry.Log(ctx, lvl, msg,
		zap.String("flag", o.flag),
		zap.String("source", o.base),
		zap.Strings("environments", current.Environments),
		zap.Bool("unknown", current.Unknown),
		zap.Duration("max_staleness", o.maxStaleness),
		zap.Error(herr),
		zap.Strings("advice", advice),
	)
	return herr != nil
}

// evaluate asks the flag service for the flag and reads the environments
// from its value.
func (o *OFREP) evaluate(ctx context.Context) ([]string, humane.Error) {
	ctx, cancel := context.WithTimeout(ctx, o.interval)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(o.body))
	if err != nil {
		return nil, humane.Wrap(err, "the freeze request to "+o.endpoint+" can't be built", "check the flag service's URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, humane.Wrap(err, "the flag service at "+o.base+" can't be reached",
			"check that the flag service is up and that the freeze's OFREP URL points at it",
			fmt.Sprintf("until it answers, the last freeze holds for up to %s, then every deploy is denied as frozen", o.maxStaleness))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, humane.Wrap(err, "the flag service's answer for "+o.flag+" can't be read", "check the connection to the flag service")
	}
	if len(body) > maxResponseBytes {
		return nil, humane.New(fmt.Sprintf("the flag service's answer for %s is larger than %d bytes", o.flag, maxResponseBytes),
			"check that the freeze's OFREP URL points at an OFREP service")
	}

	var ev evaluation
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, humane.Wrap(err, fmt.Sprintf("the flag service answered %d for %s with a body that isn't an OFREP evaluation", resp.StatusCode, o.flag),
			"check that the freeze's OFREP URL points at an OFREP service")
	}
	if resp.StatusCode != http.StatusOK || ev.ErrorCode != "" || ev.Reason == reasonError {
		return nil, o.failed(resp.StatusCode, ev)
	}
	return environments(o.flag, ev.Value)
}

// failed explains an evaluation the flag service answered with an error.
func (o *OFREP) failed(status int, ev evaluation) humane.Error {
	code := ev.ErrorCode
	if code == "" {
		code = ev.Reason
	}
	msg := fmt.Sprintf("the flag service answered %d evaluating %s", status, o.flag)
	if code != "" {
		msg += ": " + code
	}
	if ev.ErrorDetails != "" {
		msg += " (" + ev.ErrorDetails + ")"
	}

	advice := "check the flag in the flag service; its own logs say why the evaluation failed"
	if status == http.StatusNotFound || code == "FLAG_NOT_FOUND" {
		advice = "create the flag " + o.flag + " in the flag service, or point the freeze at the flag that lists the frozen environments"
	}
	return humane.New(msg, advice)
}

// parseBase checks the flag service's base URL and returns it without a
// trailing slash, ready for the evaluation path.
func parseBase(raw string) (string, humane.Error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", humane.Wrap(err, "the freeze's OFREP URL "+raw+" isn't a URL", "set it to the flag service's base URL, such as http://flagd:8016")
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", humane.New("the freeze's OFREP URL "+raw+" isn't an absolute http or https URL",
			"set it to the flag service's base URL, such as http://flagd:8016")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// environments reads a flag value as the frozen environments: a JSON array
// of strings, or one string of comma-separated names.
func environments(flag string, value json.RawMessage) ([]string, humane.Error) {
	advice := "make the flag's value a list of environment names, or one string of them separated by commas; an empty one freezes nothing"

	if len(bytes.TrimSpace(value)) == 0 || string(value) == "null" {
		return nil, humane.New("the flag "+flag+" has no value", advice)
	}

	var list []string
	errList := json.Unmarshal(value, &list)
	if errList == nil {
		return normalize(list), nil
	}
	var s string
	if err := json.Unmarshal(value, &s); err == nil {
		return normalize(strings.Split(s, ",")), nil
	}
	return nil, humane.Wrap(errList, "the value of the flag "+flag+" isn't a list of environments: "+string(value), advice)
}
