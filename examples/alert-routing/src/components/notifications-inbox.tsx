"use client";

import { BellRingIcon, HashIcon, InboxIcon, VolumeXIcon } from "lucide-react";
import Link from "next/link";
import { useState } from "react";
import { DecisionBadge, StatusBadge } from "@/components/badges";
import { useEvents } from "@/components/events-provider";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useNow } from "@/components/use-now";
import type { FeedItem } from "@/lib/ui/api-types";
import { relativeTime } from "@/lib/ui/format";
import { type DestinationKind, groupByDestination, type Inbox } from "@/lib/ui/history";

const TABS: { value: DestinationKind | "all"; label: string }[] = [
  { value: "all", label: "All" },
  { value: "oncall", label: "On-call" },
  { value: "channel", label: "Channels" },
  { value: "dropped", label: "Dropped" },
];

const KIND_ICON = { oncall: BellRingIcon, channel: HashIcon, dropped: VolumeXIcon } as const;
const KIND_LABEL = { oncall: "on-call target", channel: "channel", dropped: "dropped" } as const;

// An inbox shows its newest notifications; the rest are a click away.
const COLLAPSED = 5;

export function NotificationsInbox() {
  const { entries, loaded } = useEvents();
  const now = useNow();
  const inboxes = groupByDestination(entries);
  if (!loaded) {
    return (
      <div className="grid gap-4 md:grid-cols-2" aria-busy="true">
        <span className="sr-only">Loading notifications…</span>
        <Skeleton className="h-48" />
        <Skeleton className="h-48" />
      </div>
    );
  }
  return (
    <Tabs defaultValue="all">
      <TabsList>
        {TABS.map((t) => {
          const n = t.value === "all" ? inboxes.length : inboxes.filter((i) => i.kind === t.value).length;
          return (
            <TabsTrigger key={t.value} value={t.value}>
              {t.label}
              <span className="ml-1 text-muted-foreground tabular-nums">{n}</span>
            </TabsTrigger>
          );
        })}
      </TabsList>
      {TABS.map((t) => {
        const shown = t.value === "all" ? inboxes : inboxes.filter((i) => i.kind === t.value);
        return (
          <TabsContent key={t.value} value={t.value} className="mt-4">
            {shown.length === 0 ? (
              <Empty className="border">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <InboxIcon />
                  </EmptyMedia>
                  <EmptyTitle>Nothing here yet</EmptyTitle>
                  <EmptyDescription>Every firing alert ends in one notification; they collect here.</EmptyDescription>
                </EmptyHeader>
              </Empty>
            ) : (
              <div className="grid gap-4 md:grid-cols-2">
                {shown.map((inbox) => (
                  <InboxCard key={`${inbox.kind}:${inbox.destination}`} inbox={inbox} now={now} />
                ))}
              </div>
            )}
          </TabsContent>
        );
      })}
    </Tabs>
  );
}

function InboxCard({ inbox, now }: { inbox: Inbox; now: Date }) {
  const [expanded, setExpanded] = useState(false);
  const Icon = KIND_ICON[inbox.kind];
  const shown = expanded ? inbox.entries : inbox.entries.slice(0, COLLAPSED);
  return (
    <Card data-testid="inbox" data-destination={inbox.destination}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 font-mono">
          <Icon className="size-4 text-muted-foreground" aria-hidden />
          {inbox.kind === "dropped" ? "dropped" : inbox.destination}
        </CardTitle>
        <CardDescription>
          {KIND_LABEL[inbox.kind]} · {inbox.entries.length} notification{inbox.entries.length === 1 ? "" : "s"}
        </CardDescription>
        {inbox.pages > 0 && (
          <CardAction>
            <Badge variant="destructive">{inbox.pages} paged</Badge>
          </CardAction>
        )}
      </CardHeader>
      <CardContent>
        <ul className="divide-y">
          {shown.map((e) => (
            <InboxItem key={e.id} entry={e} now={now} />
          ))}
        </ul>
        {inbox.entries.length > COLLAPSED && (
          <Button variant="ghost" size="sm" className="mt-2" onClick={() => setExpanded((x) => !x)}>
            {expanded ? "Show fewer" : `Show all ${inbox.entries.length}`}
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

function InboxItem({ entry: e, now }: { entry: FeedItem; now: Date }) {
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-sm">
      <DecisionBadge decision={e.response.decision} />
      <Link href={`/alerts/${e.id}`} className="font-medium underline-offset-4 hover:underline">
        {e.alertname || "(no name)"}
      </Link>
      <StatusBadge status={e.status} />
      <span className="text-muted-foreground">{e.response.team ?? e.team_label ?? "unowned"}</span>
      <code className="font-mono text-xs text-muted-foreground">{e.response.reason}</code>
      <time dateTime={e.at} className="ml-auto text-xs text-muted-foreground tabular-nums">
        {relativeTime(e.at, now)}
      </time>
      {e.notify_error !== undefined && (
        <p className="w-full text-xs text-destructive">Delivery failed: {e.notify_error}</p>
      )}
    </li>
  );
}
