// The container's health check, for an image without a shell or curl:
// `node healthcheck.mjs` asks the alertrouter on this host whether it's
// ready, GET /readyz on the loopback interface at the port ALERTROUTER_ADDR
// names, and exits 0 on 200 OK and 1 otherwise, like the Go service's
// `alertrouter healthcheck`.

import { listenAddr } from "./listen.mjs";

const TIMEOUT_MS = 2_000;

const { host, port } = listenAddr(process.env);
const target = host === "" || host === "0.0.0.0" || host === "::" ? "127.0.0.1" : host;
const url = `http://${target.includes(":") ? `[${target}]` : target}:${port}/readyz`;

try {
  const res = await fetch(url, { signal: AbortSignal.timeout(TIMEOUT_MS) });
  await res.arrayBuffer();
  if (res.status !== 200) {
    process.stderr.write(
      `alertrouter at ${url} isn't ready: HTTP ${res.status}\n\nAdvice:\n  - wait for the first policy load, or check the alertrouter logs for the policy that fails to compile\n`,
    );
    process.exit(1);
  }
} catch (err) {
  process.stderr.write(
    `alertrouter at ${url} didn't answer: ${err instanceof Error ? err.message : String(err)}\n\nAdvice:\n  - check that alertrouter is running and listens on the same ALERTROUTER_ADDR\n`,
  );
  process.exit(1);
}
