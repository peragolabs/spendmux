// Package telemetry exports OpenTelemetry traces for proxied LLM calls.
//
// It is off unless an OTLP endpoint is configured with the standard
// variables, so a self-hosted install sends nothing anywhere by default:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT          e.g. https://otlp.example.com (traces go to /v1/traces)
//	OTEL_EXPORTER_OTLP_TRACES_ENDPOINT   full traces URL, wins over the above
//	OTEL_EXPORTER_OTLP_HEADERS           e.g. "x-honeycomb-team=abc,x-other=1"
//	OTEL_SERVICE_NAME                    default "spendmux"
//	OTEL_TRACES_SAMPLER_ARG              ratio 0..1 for new traces, default 1
//
// Each /v1 request gets a server span that joins the caller's trace when it
// sends a W3C traceparent header. Each provider call (and cache hit) gets a
// child span named after the OpenTelemetry GenAI conventions, "chat gpt-4o",
// with gen_ai.* attributes plus Spendmux's cost, cache and wallet fields.
// Prompts and completions are never put on spans.
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const ctxLocal = "otel_ctx"

var (
	enabled    bool
	tracer     trace.Tracer = otel.Tracer("spendmux")
	propagator              = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
)

// Enabled reports whether traces are being exported.
func Enabled() bool { return enabled }

// Init starts the OTLP/HTTP exporter when an endpoint is configured. The
// returned function flushes and stops it; call it on shutdown.
func Init(ctx context.Context) func(context.Context) error {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" && strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return func(context.Context) error { return nil }
	}
	exp, err := otlptracehttp.New(ctx) // reads the OTEL_EXPORTER_OTLP_* variables itself
	if err != nil {
		slog.Error("otel_exporter_failed", "error", err)
		return func(context.Context) error { return nil }
	}
	name := strings.TrimSpace(os.Getenv("OTEL_SERVICE_NAME"))
	if name == "" {
		name = "spendmux"
	}
	res, _ := resource.Merge(resource.Default(), resource.NewSchemaless(attribute.String("service.name", name)))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio()))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagator)
	tracer = tp.Tracer("spendmux")
	enabled = true
	slog.Info("otel_tracing_enabled", "service", name)
	return tp.Shutdown
}

func sampleRatio() float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("OTEL_TRACES_SAMPLER_ARG")), 64); err == nil && v >= 0 && v <= 1 {
		return v
	}
	return 1
}

// fiberCarrier reads trace headers from a Fiber request.
type fiberCarrier struct{ c *fiber.Ctx }

func (f fiberCarrier) Get(key string) string { return f.c.Get(key) }
func (f fiberCarrier) Set(string, string)    {}
func (f fiberCarrier) Keys() []string        { return []string{"traceparent", "tracestate", "baggage"} }

// Middleware wraps each request in a server span, continuing the caller's
// trace when it sends traceparent. Handlers read the context with FromFiber.
func Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !enabled {
			return c.Next()
		}
		parent := propagator.Extract(context.Background(), fiberCarrier{c})
		route := c.Method() + " " + c.Path()
		ctx, span := tracer.Start(parent, route, trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", c.Method()), attribute.String("url.path", c.Path())))
		c.Locals(ctxLocal, ctx)
		err := c.Next()
		status := c.Response().StatusCode()
		span.SetAttributes(attribute.Int("http.response.status_code", status))
		if status >= 500 || err != nil {
			span.SetStatus(codes.Error, strconv.Itoa(status))
		}
		if sc := span.SpanContext(); sc.IsValid() {
			c.Set("X-Spendmux-Trace-Id", sc.TraceID().String())
		}
		span.End()
		return err
	}
}

// FromFiber returns the request's trace context (background when tracing is off).
func FromFiber(c *fiber.Ctx) context.Context {
	if c != nil {
		if ctx, ok := c.Locals(ctxLocal).(context.Context); ok {
			return ctx
		}
	}
	return context.Background()
}

// LLMCall is one finished provider call, as logged for usage.
type LLMCall struct {
	Endpoint, Provider, Model         string
	Start                             time.Time
	Latency, TTFT                     time.Duration
	PromptTokens, CompletionTokens    int
	CachedTokens, ReasoningTokens     int
	CostUSD, SavedUSD                 float64
	Status                            int
	Streamed, CacheHit                bool
	Error                             string
	TokenID, Session, Tenant, Account string
}

// operation maps a usage endpoint to gen_ai.operation.name.
func operation(endpoint string) string {
	switch endpoint {
	case "chat.completions":
		return "chat"
	case "embeddings":
		return "embeddings"
	case "images.generations":
		return "image_generation"
	}
	return endpoint
}

// RecordLLMCall emits a finished client span for one provider call.
func RecordLLMCall(ctx context.Context, call LLMCall) {
	if !enabled {
		return
	}
	op := operation(call.Endpoint)
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.operation.name", op),
		attribute.String("gen_ai.provider.name", call.Provider),
		attribute.String("gen_ai.system", call.Provider),
		attribute.String("gen_ai.request.model", call.Model),
		attribute.Int("gen_ai.usage.input_tokens", call.PromptTokens),
		attribute.Int("gen_ai.usage.output_tokens", call.CompletionTokens),
		attribute.Int("http.response.status_code", call.Status),
		attribute.Float64("spendmux.cost_usd", call.CostUSD),
		attribute.Bool("spendmux.cache_hit", call.CacheHit),
		attribute.Bool("spendmux.streamed", call.Streamed),
	}
	if call.CachedTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.usage.cache_read.input_tokens", call.CachedTokens))
	}
	if call.ReasoningTokens > 0 {
		attrs = append(attrs, attribute.Int("gen_ai.usage.reasoning.output_tokens", call.ReasoningTokens))
	}
	if call.SavedUSD > 0 {
		attrs = append(attrs, attribute.Float64("spendmux.cost_saved_usd", call.SavedUSD))
	}
	if call.TTFT > 0 {
		attrs = append(attrs, attribute.Int64("spendmux.ttft_ms", call.TTFT.Milliseconds()))
	}
	for k, v := range map[string]string{"spendmux.token_id": call.TokenID, "spendmux.session_id": call.Session, "spendmux.tenant_id": call.Tenant, "spendmux.account_id": call.Account} {
		if v != "" {
			attrs = append(attrs, attribute.String(k, v))
		}
	}
	_, span := tracer.Start(ctx, op+" "+call.Model, trace.WithSpanKind(trace.SpanKindClient), trace.WithTimestamp(call.Start), trace.WithAttributes(attrs...))
	if call.Status >= 400 {
		span.SetAttributes(attribute.String("error.type", strconv.Itoa(call.Status)))
		span.SetStatus(codes.Error, call.Error)
	}
	span.End(trace.WithTimestamp(call.Start.Add(call.Latency)))
}
