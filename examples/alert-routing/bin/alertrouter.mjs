// alertrouter's entry point in the container: `node alertrouter.mjs`, next
// to the server.js of Next's standalone output. It maps ALERTROUTER_ADDR to
// the PORT and HOSTNAME Next's server reads, and tells Next to leave
// SIGTERM and SIGINT to the service (src/server/boot.ts), which drains
// requests and flushes telemetry before it exits, instead of exiting at once.

import { listenAddr } from "./listen.mjs";

const { host, port } = listenAddr(process.env);
process.env.PORT = String(port);
process.env.HOSTNAME = host === "" ? "0.0.0.0" : host;
process.env.NEXT_MANUAL_SIG_HANDLE ??= "true";

await import("./server.js");
