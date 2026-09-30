// The console's memory of what alertrouter did: a bounded ring buffer of
// routed alerts, each with the notification it led to, and a stream of new
// entries and policy reloads for GET /api/v1/events. It lives in the
// process, so it starts empty on every restart; it's for people watching
// the console, not a record anyone relies on. Not in the Go service.

import type { ErrorResponse } from "../errors";
import type { AlertResult } from "../http/wire";
import type { DeliveryStatus } from "./dispatcher";

/** How a routed alert reached alertrouter. */
export type AlertSource = "webhook" | "route";

/** One routed alert, as the console shows it. */
export interface HistoryEntry {
  /** Increases by one per entry, from 1; SSE event ids and ?after= use it. */
  id: number;
  /** RFC 3339, when it was routed. */
  at: string;
  source: AlertSource;
  /** The alert as the policy saw it; absent for an alert the router couldn't read. */
  alert?: { name: string; severity: string; labels: Record<string, string>; firing_for: string };
  /** The severity label as sent, even when it isn't one the kind declares. */
  severity: string;
  /** The alert's result, as the API answered it: status, decision, trace and so on. */
  result: AlertResult;
  /** The notification it led to. */
  notification: {
    destination: string;
    status: DeliveryStatus | "skipped";
    error?: string;
  };
  /** The trace of the alert's alertrouter.route span, for a link to Tempo. */
  trace_id?: string;
}

/** A policy load, successful or not, as the console's policies page shows it. */
export interface ReloadEvent {
  at: string;
  trigger: string;
  ok: boolean;
  fingerprint?: string;
  source: string;
  error?: ErrorResponse;
}

/** What GET /api/v1/events streams. */
export type HistoryEvent = { type: "alert"; entry: HistoryEntry } | { type: "reload"; reload: ReloadEvent };

export type Listener = (event: HistoryEvent) => void;

/** The ring buffer and its subscribers. */
export class History {
  readonly #size: number;
  readonly #entries: HistoryEntry[] = [];
  readonly #listeners = new Set<Listener>();
  #nextId = 1;

  constructor(size: number) {
    this.#size = Math.max(0, size);
  }

  /** Records an entry, dropping the oldest past the size, and tells every subscriber. */
  add(entry: Omit<HistoryEntry, "id">): HistoryEntry {
    const full: HistoryEntry = { id: this.#nextId++, ...entry };
    if (this.#size > 0) {
      this.#entries.push(full);
      if (this.#entries.length > this.#size) this.#entries.shift();
    }
    this.#emit({ type: "alert", entry: full });
    return full;
  }

  /** Tells every subscriber about a policy load. Loads aren't kept. */
  reload(event: ReloadEvent): void {
    this.#emit({ type: "reload", reload: event });
  }

  /** The entries kept, oldest first, those after id `after` when it's given. */
  entries(after = 0): HistoryEntry[] {
    return this.#entries.filter((e) => e.id > after);
  }

  /** Calls listener for every event from now on, until the returned function is called. */
  subscribe(listener: Listener): () => void {
    this.#listeners.add(listener);
    return () => {
      this.#listeners.delete(listener);
    };
  }

  /** How many subscribers are listening, for tests and shutdown. */
  get subscribers(): number {
    return this.#listeners.size;
  }

  #emit(event: HistoryEvent): void {
    for (const listener of this.#listeners) {
      try {
        listener(event);
      } catch {
        // A subscriber that throws is a closed stream; it unsubscribes itself.
      }
    }
  }
}
