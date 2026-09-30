// The listen address, ALERTROUTER_ADDR in the Go service's syntax (":8080",
// "127.0.0.1:8080", "[::1]:8080"), split into what Node's server takes.
// Plain JavaScript, so the container runs it next to Next's server.js
// without a build step; src/lib/config/config.ts validates the same
// variable with the same pattern when the service boots.

export const DEFAULT_ADDR = ":8080";

/** { host, port } for addr, host empty for every interface; undefined when it isn't host:port. */
export function parseAddr(addr) {
  const m = /^(?:\[([^\]]+)\]|([^:[\]]*)):(\d{1,5})$/.exec(addr);
  if (m === null) return undefined;
  const port = Number(m[3]);
  if (port > 65_535) return undefined;
  return { host: m[1] ?? m[2] ?? "", port };
}

/** The address, or exits with a message that says what to set. */
export function listenAddr(env) {
  const addr = env.ALERTROUTER_ADDR || DEFAULT_ADDR;
  const parsed = parseAddr(addr);
  if (parsed === undefined) {
    process.stderr.write(
      `the listen address ${JSON.stringify(addr)} isn't host:port\n\nAdvice:\n  - set ALERTROUTER_ADDR to a port with an optional host, such as :8080 or 127.0.0.1:8080\n`,
    );
    process.exit(1);
  }
  return parsed;
}
