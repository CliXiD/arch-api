package observability

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// SetupTracing exports traces over OTLP to endpoint with protocol ("grpc" or "http/protobuf"),
// as service arch-api, and continues incoming W3C `traceparent` headers. The caller only calls
// it when OTEL_EXPORTER_OTLP_ENDPOINT is set; otherwise no exporter exists. The returned
// function flushes and stops the exporter.
func SetupTracing(ctx context.Context, endpoint, protocol string) (func(context.Context) error, error) {
	var (
		exporter sdktrace.SpanExporter
		err      error
	)
	if protocol == "http/protobuf" {
		exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(strings.TrimRight(endpoint, "/")+"/v1/traces"))
	} else {
		exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(endpoint))
	}
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "arch-api"))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp.Shutdown, nil
}
