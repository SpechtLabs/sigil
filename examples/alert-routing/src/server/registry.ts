// The handle between the composition root and Next's route handlers. Next
// loads instrumentation.ts and each route in bundles of their own, so a
// module-level variable would exist once per bundle; the service is kept on
// globalThis under a registered symbol instead. Nothing is created here: boot()
// builds the service and puts it here, route handlers take it from here.

import type { Service } from "./service";

const KEY = Symbol.for("alertrouter.service");

type Holder = { [KEY]?: Service };

/** Installs the service the route handlers answer with. Called once, by boot(). */
export function setService(service: Service | undefined): void {
  (globalThis as Holder)[KEY] = service;
}

/** The running service, or undefined before boot() finished or after it failed. */
export function getService(): Service | undefined {
  return (globalThis as Holder)[KEY];
}
