// The console's view of the router's history: merging the entries the page
// loaded with the ones the event stream brings, counting decisions, and
// grouping notifications by where they went.

import type { FeedItem, HistoryEntry, PolicyFilesResponse, PolicyStatus, ReloadEvent } from "./api-types";
import { routeResponseFromAlertResult } from "./preview";

/** The most entries the browser keeps, whatever the server's ring holds. */
export const MAX_ENTRIES = 500;

/**
 * Adds entries to the list, newest first, once each: the stream replays
 * entries the page may already have loaded, and a reconnect replays again.
 */
export function mergeEntries(current: readonly FeedItem[], incoming: readonly FeedItem[]): FeedItem[] {
  const byId = new Map<string, FeedItem>();
  for (const e of current) byId.set(e.id, e);
  for (const e of incoming) byId.set(e.id, e);
  return [...byId.values()].sort(newestFirst).slice(0, MAX_ENTRIES);
}

function newestFirst(a: FeedItem, b: FeedItem): number {
  const ids = Number(b.id) - Number(a.id);
  return Number.isNaN(ids) ? b.at.localeCompare(a.at) : ids;
}

export type DecisionName = "page" | "drop" | "notify";

export interface DecisionCounts {
  total: number;
  page: number;
  drop: number;
  notify: number;
  /** Alerts no policy decided: unowned, invalid or failed. */
  fallback: number;
  /** Decisions the notifier couldn't deliver: nobody was reached. */
  undelivered: number;
}

export function countDecisions(entries: readonly FeedItem[]): DecisionCounts {
  const counts: DecisionCounts = { total: 0, page: 0, drop: 0, notify: 0, fallback: 0, undelivered: 0 };
  for (const e of entries) {
    counts.total++;
    const d = e.response.decision;
    if (d === "page" || d === "drop" || d === "notify") counts[d]++;
    if (e.status === "dispatch_failed") counts.undelivered++;
    else if (e.status !== "routed") counts.fallback++;
  }
  return counts;
}

/** Where notifications go: whom a page reaches, a channel, or nowhere for a drop. */
export type DestinationKind = "oncall" | "channel" | "dropped";

export interface Inbox {
  destination: string;
  kind: DestinationKind;
  /** Newest first. */
  entries: FeedItem[];
  /** Pages in this inbox, which read as urgent. */
  pages: number;
}

export function destinationKind(e: FeedItem): DestinationKind {
  if (e.response.decision === "page") return "oncall";
  if (e.response.decision === "drop" || e.destination === "-") return "dropped";
  return "channel";
}

/**
 * Groups the entries into one inbox per destination, the busiest first and
 * ties by name, so the order doesn't jump as equal inboxes fill.
 */
export function groupByDestination(entries: readonly FeedItem[]): Inbox[] {
  const inboxes = new Map<string, Inbox>();
  for (const e of entries) {
    const kind = destinationKind(e);
    const key = `${kind}\u0000${e.destination}`;
    let inbox = inboxes.get(key);
    if (inbox === undefined) {
      inbox = { destination: e.destination, kind, entries: [], pages: 0 };
      inboxes.set(key, inbox);
    }
    inbox.entries.push(e);
    if (e.response.decision === "page") inbox.pages++;
  }
  return [...inboxes.values()].sort(
    (a, b) => b.entries.length - a.entries.length || a.destination.localeCompare(b.destination),
  );
}

/** A history entry as the pages read it. */
export function feedItem(e: HistoryEntry): FeedItem {
  const { status, fingerprint, alertname } = e.result;
  const labels = e.alert?.labels ?? {};
  return {
    id: String(e.id),
    at: e.at,
    source: e.source,
    // The history keeps firing alerts only; a resolved one never gets here.
    status: status === "resolved" ? "routed" : status,
    fingerprint,
    alertname: e.alert?.name ?? alertname,
    severity: e.severity,
    labels,
    ...(e.alert !== undefined ? { firing_for: e.alert.firing_for } : {}),
    ...(labels.team !== undefined ? { team_label: labels.team } : {}),
    response: routeResponseFromAlertResult(e.result),
    destination: e.notification.destination,
    delivery: e.notification.status,
    ...(e.notification.error !== undefined ? { notify_error: e.notification.error } : {}),
    ...(e.trace_id !== undefined ? { trace_id: e.trace_id } : {}),
  };
}

/** The store's state as the files endpoint reports it. */
export function statusFromFiles(files: PolicyFilesResponse): PolicyStatus {
  return {
    fingerprint: files.fingerprint,
    loaded_at: files.loaded_at,
    source: files.source,
    ...(files.last_error !== undefined ? { last_error: files.last_error } : {}),
  };
}

/**
 * The store's state after a load the event stream reported: a good one
 * serves and clears the failure, a failed one leaves the bundle serving and
 * becomes the failure to show.
 */
export function applyReload(status: PolicyStatus | undefined, ev: ReloadEvent): PolicyStatus {
  if (ev.ok) {
    return {
      ...(ev.fingerprint !== undefined ? { fingerprint: ev.fingerprint } : {}),
      loaded_at: ev.at,
      source: ev.source,
      last_attempt: ev,
    };
  }
  return {
    ...status,
    last_attempt: ev,
    last_error: { at: ev.at, trigger: ev.trigger, error: ev.error ?? { message: "the reload failed" } },
  };
}
