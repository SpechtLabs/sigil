import type { NextConfig } from "next";

const config: NextConfig = {
  // The container runs .next/standalone/server.js on Node, with only the
  // traced node_modules copied in.
  output: "standalone",
  // @spechtlabs/sigil stays a plain Node module on the server instead of being
  // bundled: it reads sigil.wasm from next to itself at run time, which a
  // bundle would move away from it.
  // The telemetry packages stay external too: @pyroscope/nodejs and
  // @datadog/pprof carry native addons, and pino, prom-client and the gRPC
  // exporter load workers or modules dynamically, which a bundle breaks.
  serverExternalPackages: [
    "@spechtlabs/sigil",
    "@pyroscope/nodejs",
    "@datadog/pprof",
    "pino",
    "pino-pretty",
    "prom-client",
    "@opentelemetry/exporter-trace-otlp-grpc",
    "@grpc/grpc-js",
  ],
  // The standalone tracer follows imports, not files resolved at run time, so
  // the package's WebAssembly binary and the worker entry the evaluation
  // workers run (with the modules it imports) are listed explicitly.
  outputFileTracingIncludes: {
    "/*": ["./node_modules/@spechtlabs/sigil/dist/*.js", "./node_modules/@spechtlabs/sigil/dist/sigil.wasm"],
  },
  // `tsc --noEmit` and `biome ci` are the typecheck and lint gates (mise run
  // check); running them again inside `next build` would only slow it down.
  typescript: { ignoreBuildErrors: true },
  poweredByHeader: false,
  // `next dev` otherwise writes AGENTS.md and CLAUDE.md into the example.
  agentRules: false,
};

export default config;
