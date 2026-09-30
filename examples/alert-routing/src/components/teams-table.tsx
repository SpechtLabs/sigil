"use client";

import { useEffect, useState } from "react";
import { useEvents } from "@/components/events-provider";
import { ErrorView } from "@/components/trace-view";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { ErrorResponse, PoliciesResponse, Team, TeamsResponse } from "@/lib/ui/api-types";
import { ApiError, request } from "@/lib/ui/client";

export function TeamsTable() {
  const { entries } = useEvents();
  const [teams, setTeams] = useState<Team[] | undefined>();
  const [policies, setPolicies] = useState<Map<string, string>>(new Map());
  const [error, setError] = useState<ErrorResponse | undefined>();

  useEffect(() => {
    request<TeamsResponse>("/api/v1/teams").then(
      (res) => setTeams(res.teams),
      (err: unknown) => setError(err instanceof ApiError ? err.response : { message: String(err) }),
    );
    // The policy column is extra: before the first load there is none to show.
    request<PoliciesResponse>("/api/v1/policies").then(
      (res) => setPolicies(new Map(res.kinds.flatMap((k) => k.policies.map((p) => [p.team, p.policy] as const)))),
      () => {},
    );
  }, []);

  if (error !== undefined) return <ErrorView error={error} title="The team directory couldn't be loaded" />;
  if (teams === undefined) {
    return (
      <div aria-busy="true">
        <span className="sr-only">Loading teams…</span>
        <Skeleton className="h-40 w-full" />
      </div>
    );
  }

  const alerts = new Map<string, number>();
  for (const e of entries) if (e.response.team) alerts.set(e.response.team, (alerts.get(e.response.team) ?? 0) + 1);

  return (
    <Card className="py-0">
      <CardContent className="px-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Team</TableHead>
              <TableHead>On-call</TableHead>
              <TableHead>Channel</TableHead>
              <TableHead>Policy</TableHead>
              <TableHead className="pr-4 text-right">Alerts seen</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {teams.map((t) => (
              <TableRow key={t.name} data-testid="team-row">
                <TableCell className="pl-4 font-medium">{t.name}</TableCell>
                <TableCell className="font-mono text-xs">{t.oncall}</TableCell>
                <TableCell className="font-mono text-xs">{t.channel}</TableCell>
                <TableCell className="font-mono text-xs">
                  {policies.get(t.name) ?? <span className="text-muted-foreground">not loaded</span>}
                </TableCell>
                <TableCell className="pr-4 text-right tabular-nums">{alerts.get(t.name) ?? 0}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}
