"use client";

import { InboxIcon } from "lucide-react";
import Link from "next/link";
import { DecisionBadge, SeverityBadge, StatusBadge } from "@/components/badges";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useNow } from "@/components/use-now";
import type { FeedItem } from "@/lib/ui/api-types";
import { clockTime, relativeTime } from "@/lib/ui/format";

export function AlertFeed({ entries, loaded }: { entries: readonly FeedItem[]; loaded: boolean }) {
  const now = useNow();
  if (!loaded) {
    return (
      <div className="space-y-2" aria-busy="true">
        <span className="sr-only">Loading the feed…</span>
        {Array.from({ length: 5 }, (_, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: placeholders have no identity
          <Skeleton key={i} className="h-10 w-full" />
        ))}
      </div>
    );
  }
  if (entries.length === 0) {
    return (
      <Empty className="border">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <InboxIcon />
          </EmptyMedia>
          <EmptyTitle>No alerts yet</EmptyTitle>
          <EmptyDescription>
            Alerts appear here the moment the router decides them, from Alertmanager or from the form.
          </EmptyDescription>
        </EmptyHeader>
        <EmptyContent>
          <Button asChild>
            <Link href="/send">Send an alert</Link>
          </Button>
        </EmptyContent>
      </Empty>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead className="w-24">Time</TableHead>
          <TableHead>Alert</TableHead>
          <TableHead>Team</TableHead>
          <TableHead>Severity</TableHead>
          <TableHead>Decision</TableHead>
          <TableHead>Reason</TableHead>
          <TableHead>Destination</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {entries.map((e) => (
          <TableRow
            key={e.id}
            data-testid="feed-row"
            data-alertname={e.alertname}
            className={e.status === "dispatch_failed" ? "bg-destructive/5 hover:bg-destructive/10" : undefined}
          >
            <TableCell className="text-muted-foreground tabular-nums">
              <time dateTime={e.at} title={clockTime(e.at)}>
                {relativeTime(e.at, now)}
              </time>
            </TableCell>
            <TableCell className="font-medium">
              <Link href={`/alerts/${e.id}`} className="underline-offset-4 hover:underline focus-visible:underline">
                {e.alertname || "(no name)"}
              </Link>
              <span className="ml-2 align-middle">
                <StatusBadge status={e.status} />
              </span>
            </TableCell>
            <TableCell>
              {e.response.team ?? <span className="text-muted-foreground">{e.team_label || "—"}</span>}
            </TableCell>
            <TableCell>
              <SeverityBadge severity={e.severity} />
            </TableCell>
            <TableCell>
              <DecisionBadge decision={e.response.decision} />
            </TableCell>
            <TableCell className="font-mono text-xs">{e.response.reason}</TableCell>
            <TableCell className="font-mono text-xs">{e.destination}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
