"use client";

import { ArrowLeftIcon } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";
import { DecisionBadge, SeverityBadge, StatusBadge } from "@/components/badges";
import { useEvents } from "@/components/events-provider";
import { ErrorView, OutcomeLine, TraceView, UndeliveredAlert } from "@/components/trace-view";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import type { ErrorResponse, FeedItem, HistoryResponse } from "@/lib/ui/api-types";
import { ApiError, request } from "@/lib/ui/client";
import { clockTime } from "@/lib/ui/format";
import { feedItem } from "@/lib/ui/history";

const STATUS_TEXT: Record<FeedItem["status"], string> = {
  routed: "The team's policy decided this alert.",
  unowned: "No team in the directory owns this alert, so it went to the kind's default without an evaluation.",
  invalid:
    "The router couldn't read all of this alert. When its team and severity were readable, the platform's paging rules still decided it; otherwise the kind's default did.",
  failed:
    "The evaluation failed or ran out of time. The platform's paging rules still decide it when they page; otherwise the kind's default does.",
  dispatch_failed: "The policy decided, but the notifier couldn't deliver it; Alertmanager was asked to retry.",
};

export function AlertDetail({ id }: { id: string }) {
  const { entries } = useEvents();
  const cached = entries.find((e) => e.id === id);
  const [fetched, setFetched] = useState<FeedItem | undefined>();
  const [error, setError] = useState<ErrorResponse | undefined>();

  useEffect(() => {
    if (cached !== undefined) return;
    let cancelled = false;
    // The history lists the entries after an id, so the one before this
    // entry's id makes it the first, if the ring still holds it.
    const after = Number(id) - 1;
    request<HistoryResponse>(`/api/v1/history?after=${Number.isFinite(after) ? after : 0}`).then(
      (res) => {
        if (cancelled) return;
        const found = res.entries.find((e) => String(e.id) === id);
        if (found !== undefined) setFetched(feedItem(found));
        else
          setError({
            message: `alert ${id} isn't in the history`,
            advice: ["the history is bounded and in memory: older alerts and those before a restart are gone"],
          });
      },
      (err: unknown) => !cancelled && setError(err instanceof ApiError ? err.response : { message: String(err) }),
    );
    return () => {
      cancelled = true;
    };
  }, [id, cached]);

  const entry = cached ?? fetched;
  return (
    <div className="space-y-6">
      <Button asChild variant="ghost" size="sm" className="-ml-2">
        <Link href="/">
          <ArrowLeftIcon /> Overview
        </Link>
      </Button>
      {entry === undefined && error !== undefined && (
        <ErrorView error={error} title="This alert isn't in the history" />
      )}
      {entry === undefined && error === undefined && (
        <div className="space-y-4" aria-busy="true">
          <span className="sr-only">Loading the alert…</span>
          <Skeleton className="h-8 w-64" />
          <Skeleton className="h-40 w-full" />
        </div>
      )}
      {entry !== undefined && <EntryView entry={entry} />}
    </div>
  );
}

function EntryView({ entry: e }: { entry: FeedItem }) {
  const r = e.response;
  return (
    <>
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">{e.alertname || "(no name)"}</h1>
          <DecisionBadge decision={r.decision} />
          <StatusBadge status={e.status} />
        </div>
        <p className="text-muted-foreground">{STATUS_TEXT[e.status]}</p>
      </div>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,4fr)_minmax(0,8fr)]">
        <Card className="self-start">
          <CardHeader>
            <CardTitle>Alert</CardTitle>
            <CardDescription>
              {e.source === "webhook" ? "From an Alertmanager webhook" : "Posted to the route endpoint"} at{" "}
              <time dateTime={e.at}>{clockTime(e.at)}</time>
            </CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Team</dt>
              <dd>{r.team ?? (e.team_label ? `${e.team_label} (not in the directory)` : "none")}</dd>
              <dt className="text-muted-foreground">Severity</dt>
              <dd>
                <SeverityBadge severity={e.severity} />
              </dd>
              {e.firing_for !== undefined && (
                <>
                  <dt className="text-muted-foreground">Firing for</dt>
                  <dd className="font-mono">{e.firing_for}</dd>
                </>
              )}
              <dt className="text-muted-foreground">Policy</dt>
              <dd className="font-mono">{r.policy || "none"}</dd>
              {e.fingerprint !== "" && (
                <>
                  <dt className="text-muted-foreground">Fingerprint</dt>
                  <dd className="font-mono break-all">{e.fingerprint}</dd>
                </>
              )}
              {e.trace_id !== undefined && (
                <>
                  <dt className="text-muted-foreground">Trace ID</dt>
                  <dd className="font-mono break-all">{e.trace_id}</dd>
                </>
              )}
              <dt className="text-muted-foreground">Labels</dt>
              <dd className="flex flex-wrap gap-1">
                {Object.keys(e.labels).length === 0 && <span className="text-muted-foreground">none</span>}
                {Object.entries(e.labels)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([k, v]) => (
                    <Badge key={k} variant="outline" className="font-mono font-normal">
                      {k}={v}
                    </Badge>
                  ))}
              </dd>
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Decision</CardTitle>
            <CardDescription>
              Every candidate the policy produced, the winner first, with the rules that reached it.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <OutcomeLine response={r} />
            {e.status === "dispatch_failed" && (
              <UndeliveredAlert decision={r.decision} destination={e.destination} late={e.delivery === "late"} />
            )}
            {e.notify_error !== undefined && e.status !== "dispatch_failed" && (
              <Alert variant="destructive">
                <AlertTitle>The notification wasn't delivered</AlertTitle>
                <AlertDescription>{e.notify_error}</AlertDescription>
              </Alert>
            )}
            <TraceView response={r} status={e.status} />
          </CardContent>
        </Card>
      </div>
    </>
  );
}
