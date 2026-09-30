"use client";

import { createContext, type ReactNode, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type {
  FeedItem,
  HistoryEntry,
  HistoryResponse,
  PolicyFilesResponse,
  PolicyStatus,
  ReloadEvent,
} from "@/lib/ui/api-types";
import { request } from "@/lib/ui/client";
import { applyReload, feedItem, mergeEntries, statusFromFiles } from "@/lib/ui/history";

/** The event stream's state: connecting at first, live once open, reconnecting after it dropped. */
export type StreamState = "connecting" | "live" | "reconnecting";

interface Events {
  entries: FeedItem[];
  /** False until the history loaded, or failed to. */
  loaded: boolean;
  /** Why the history couldn't be loaded, if it couldn't. */
  loadError: string | undefined;
  stream: StreamState;
  policyStatus: PolicyStatus | undefined;
  /** Fetches the policy status again, after a reload the page asked for itself. */
  refreshPolicyStatus: () => Promise<void>;
}

const EventsContext = createContext<Events | undefined>(undefined);

/**
 * Holds the router's history for every page: loaded once, then kept current
 * from the Server-Sent Events stream, so the overview, the inbox and the
 * send form share one connection. Entries arrive in the order alerts finish
 * routing, so they're merged by id rather than appended. The history is
 * fetched again on every (re)connect, and merging drops the entries the
 * page already has.
 */
export function EventsProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<FeedItem[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [loadError, setLoadError] = useState<string | undefined>();
  const [stream, setStream] = useState<StreamState>("connecting");
  const [policyStatus, setPolicyStatus] = useState<PolicyStatus | undefined>();

  const refreshPolicyStatus = useCallback(async () => {
    try {
      setPolicyStatus(statusFromFiles(await request<PolicyFilesResponse>("/api/v1/policies/files")));
    } catch {
      // Before the first load there is no bundle; the reload events say when there is.
    }
  }, []);

  const refresh = useCallback(async () => {
    try {
      const history = await request<HistoryResponse>("/api/v1/history");
      setEntries((current) => mergeEntries(current, history.entries.map(feedItem)));
      setLoadError(undefined);
    } catch (err) {
      setLoadError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoaded(true);
    }
    await refreshPolicyStatus();
  }, [refreshPolicyStatus]);

  useEffect(() => {
    if (typeof EventSource === "undefined") {
      void refresh();
      return;
    }
    let source: EventSource | undefined;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let lastId = 0;
    let failures = 0;
    let stopped = false;

    const connect = () => {
      // ?after= resumes where the last stream stopped, like Last-Event-ID
      // does for EventSource's own reconnects.
      const es = new EventSource(lastId > 0 ? `/api/v1/events?after=${lastId}` : "/api/v1/events");
      source = es;
      es.addEventListener("open", () => {
        failures = 0;
        setStream("live");
        void refresh();
      });
      es.addEventListener("error", () => {
        setStream("reconnecting");
        // EventSource retries a dropped stream by itself, but gives up on a
        // response that isn't one, such as the 503 the server answers when
        // too many streams are open. Try again later, backing off.
        if (es.readyState === EventSource.CLOSED && !stopped) {
          es.close();
          failures++;
          retry = setTimeout(connect, Math.min(30_000, 2_000 * 2 ** Math.min(failures - 1, 4)));
        }
      });
      es.addEventListener("alert", (ev) => {
        const entry = parse<HistoryEntry>(ev);
        if (entry === undefined) return;
        lastId = Math.max(lastId, entry.id);
        setEntries((current) => mergeEntries(current, [feedItem(entry)]));
      });
      es.addEventListener("reload", (ev) => {
        const reload = parse<ReloadEvent>(ev);
        if (reload !== undefined) setPolicyStatus((current) => applyReload(current, reload));
      });
    };
    connect();
    return () => {
      stopped = true;
      clearTimeout(retry);
      source?.close();
    };
  }, [refresh]);

  const value = useMemo(
    () => ({ entries, loaded, loadError, stream, policyStatus, refreshPolicyStatus }),
    [entries, loaded, loadError, stream, policyStatus, refreshPolicyStatus],
  );
  return <EventsContext value={value}>{children}</EventsContext>;
}

function parse<T>(ev: Event): T | undefined {
  try {
    return JSON.parse((ev as MessageEvent<string>).data) as T;
  } catch {
    return undefined;
  }
}

export function useEvents(): Events {
  const events = useContext(EventsContext);
  if (events === undefined) throw new Error("useEvents must be used inside <EventsProvider>");
  return events;
}
