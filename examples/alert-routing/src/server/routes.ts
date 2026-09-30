// The adapter Next's route files export: every method of every API, health
// and metrics route goes to the service's Api, which routes the request
// itself, so a method a route doesn't serve answers the Go service's 404
// rather than Next's 405, and the integration tests exercise the same
// routing without Next.

import { humane } from "@/lib/errors";
import { errorJSON } from "@/lib/http/handlers";
import { getService } from "./registry";

type RouteHandler = (req: Request) => Promise<Response>;

const handle: RouteHandler = async (req) => {
  const service = getService();
  if (service === undefined) {
    return errorJSON(
      503,
      humane("alertrouter is starting or stopping", "retry shortly; GET /readyz reports when it takes traffic"),
    );
  }
  return service.api.fetch(req);
};

/** The handlers a route file exports, one per method. */
export const methods: Readonly<Record<"GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE" | "OPTIONS", RouteHandler>> =
  {
    GET: handle,
    HEAD: handle,
    POST: handle,
    PUT: handle,
    PATCH: handle,
    DELETE: handle,
    OPTIONS: handle,
  };
