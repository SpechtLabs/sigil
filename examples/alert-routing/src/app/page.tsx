"use client";

import { AlertFeed } from "@/components/alert-feed";
import { useEvents } from "@/components/events-provider";
import { PageHeader } from "@/components/page-header";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { countDecisions } from "@/lib/ui/history";

export default function OverviewPage() {
  const { entries, loaded, loadError } = useEvents();
  const counts = countDecisions(entries);
  const stats = [
    { label: "Alerts", value: counts.total, hint: "firing alerts decided" },
    { label: "Paged", value: counts.page, hint: "went to an on-call" },
    { label: "Notified", value: counts.notify, hint: "posted to a channel" },
    { label: "Dropped", value: counts.drop, hint: "deliberately silenced" },
    { label: "Fallback", value: counts.fallback, hint: "unowned, invalid or failed" },
    { label: "Undelivered", value: counts.undelivered, hint: "decided, but no one was reached" },
  ];
  return (
    <div className="space-y-6">
      <PageHeader title="Overview" description="Every firing alert the router decided, newest first, as it happens." />
      {loadError !== undefined && (
        <Alert variant="destructive">
          <AlertTitle>The history couldn't be loaded</AlertTitle>
          <AlertDescription>{loadError}</AlertDescription>
        </Alert>
      )}
      <section aria-label="Decisions" className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {stats.map((s) => (
          <Card
            key={s.label}
            className={s.label === "Undelivered" && s.value > 0 ? "ring-destructive/50 text-destructive" : undefined}
          >
            <CardHeader>
              <CardDescription>{s.label}</CardDescription>
              <CardTitle className="text-2xl tabular-nums" data-testid={`count-${s.label.toLowerCase()}`}>
                {s.value}
              </CardTitle>
              <p className="text-xs text-muted-foreground">{s.hint}</p>
            </CardHeader>
          </Card>
        ))}
      </section>
      <section aria-labelledby="feed-heading" className="space-y-3">
        <h2 id="feed-heading" className="text-lg font-semibold">
          Live feed
        </h2>
        <AlertFeed entries={entries} loaded={loaded} />
      </section>
    </div>
  );
}
