"use client";

import type { ExplainEntry } from "@spechtlabs/sigil";
import { CheckCircle2Icon, RefreshCwIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { DecisionBadge } from "@/components/badges";
import { useEvents } from "@/components/events-provider";
import { PageHeader } from "@/components/page-header";
import { ErrorView } from "@/components/trace-view";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useNow } from "@/components/use-now";
import type {
  ErrorResponse,
  Explanation,
  KindPolicies,
  PoliciesResponse,
  PolicyStatus,
  TeamExplanation,
} from "@/lib/ui/api-types";
import { ApiError, errorOf, postJSON, request } from "@/lib/ui/client";
import { clockTime, relativeTime } from "@/lib/ui/format";

export function PoliciesView() {
  const { policyStatus, refreshPolicyStatus } = useEvents();
  const [bundle, setBundle] = useState<KindPolicies | undefined>();
  const [explanations, setExplanations] = useState<TeamExplanation[] | undefined>();
  const [error, setError] = useState<ErrorResponse | undefined>();
  const [reloading, setReloading] = useState(false);

  const load = useCallback(async () => {
    try {
      const policies = await request<PoliciesResponse>("/api/v1/policies");
      const kind = policies.kinds[0];
      const explained = await Promise.all(
        (kind?.policies ?? []).map(async (p) => ({
          ...p,
          explanation: await request<Explanation>(`/api/v1/policies/${encodeURIComponent(p.team)}/explain`),
        })),
      );
      setBundle(kind);
      setExplanations(explained);
      setError(undefined);
    } catch (err) {
      setError(err instanceof ApiError ? err.response : { message: String(err) });
    }
  }, []);

  // Load again whenever another bundle starts serving, however it was reloaded.
  const fingerprint = policyStatus?.fingerprint;
  useEffect(() => {
    void fingerprint;
    void load();
  }, [fingerprint, load]);

  async function reload() {
    setReloading(true);
    try {
      const { status, body } = await postJSON("/api/v1/policies/reload");
      if (status === 200) {
        const kind = (body as PoliciesResponse).kinds[0];
        toast.success(`Policies reloaded${kind ? `: ${kind.fingerprint.slice(0, 12)}` : ""}`);
      } else {
        toast.error(`The reload failed; the previous bundle keeps serving. ${errorOf(status, body).message}`);
      }
      await refreshPolicyStatus();
      await load();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setReloading(false);
    }
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title="Policies"
        description="The bundle that serves, each team's policy flattened into its rules, and how the latest reload went."
        actions={
          <Button onClick={reload} disabled={reloading}>
            {reloading ? <Spinner /> : <RefreshCwIcon />}
            Reload policies
          </Button>
        }
      />
      <ReloadStatus status={policyStatus} />
      {error !== undefined && <ErrorView error={error} title="The policies couldn't be loaded" />}
      {bundle === undefined && error === undefined && <Skeleton className="h-64 w-full" />}
      {bundle !== undefined && <BundleCard bundle={bundle} />}
      {bundle !== undefined && explanations !== undefined && (
        <Tabs defaultValue={bundle.policies[0]?.team}>
          <TabsList>
            {bundle.policies.map((p) => (
              <TabsTrigger key={p.team} value={p.team}>
                {p.team}
              </TabsTrigger>
            ))}
          </TabsList>
          {bundle.policies.map((p) => {
            const explained = explanations.find((x) => x.team === p.team)?.explanation;
            return (
              <TabsContent key={p.team} value={p.team} className="mt-4">
                {explained === undefined ? (
                  <p className="text-sm text-muted-foreground">No explanation for {p.policy}.</p>
                ) : (
                  <ExplanationCard explanation={explained} />
                )}
              </TabsContent>
            );
          })}
        </Tabs>
      )}
    </div>
  );
}

/** The latest reload: fine, or the error that kept the previous bundle serving. */
function ReloadStatus({ status }: { status: PolicyStatus | undefined }) {
  const now = useNow();
  const failure = status?.last_error;
  if (failure === undefined) return null;
  // A failure is current until a load after it succeeded.
  const recovered = status?.loaded_at !== undefined && new Date(status.loaded_at) > new Date(failure.at);
  if (recovered) {
    return (
      <Alert>
        <CheckCircle2Icon />
        <AlertTitle>The last failed reload has been fixed</AlertTitle>
        <AlertDescription>
          A {failure.trigger} reload failed {relativeTime(failure.at, now)}; a later load succeeded.
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <div data-testid="reload-error">
      <ErrorView
        error={failure.error}
        title={`The ${failure.trigger} reload ${relativeTime(failure.at, now)} failed; the previous bundle keeps serving`}
      />
    </div>
  );
}

function BundleCard({ bundle }: { bundle: KindPolicies }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          {bundle.kind} <span className="text-muted-foreground">version {bundle.version}</span>
        </CardTitle>
        <CardDescription>The bundle that serves.</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)]">
          <dt className="text-muted-foreground">Fingerprint</dt>
          <dd className="font-mono break-all" data-testid="fingerprint">
            {bundle.fingerprint}
          </dd>
          <dt className="text-muted-foreground">Source</dt>
          <dd className="font-mono break-all">{bundle.source}</dd>
          <dt className="text-muted-foreground">Loaded</dt>
          <dd>
            <time dateTime={bundle.loaded_at}>
              {new Date(bundle.loaded_at).toLocaleDateString()} {clockTime(bundle.loaded_at)}
            </time>
          </dd>
          <dt className="text-muted-foreground">Team policies</dt>
          <dd className="flex flex-wrap gap-1">
            {bundle.policies.map((p) => (
              <Badge key={p.team} variant="outline" className="font-mono font-normal">
                {p.policy}
              </Badge>
            ))}
          </dd>
        </dl>
      </CardContent>
    </Card>
  );
}

/** `sigil explain` for one policy: every rule it can reach, flattened, then its asserts. */
function ExplanationCard({ explanation }: { explanation: Explanation }) {
  return (
    <Card className="pb-0">
      <CardHeader>
        <CardTitle className="font-mono">{explanation.policy}</CardTitle>
        <CardDescription>
          {explanation.rules.length} rule{explanation.rules.length === 1 ? "" : "s"} across {explanation.policies} polic
          {explanation.policies === 1 ? "y" : "ies"} and {explanation.modules} module
          {explanation.modules === 1 ? "" : "s"}, as <code>sigil explain</code> flattens them.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Outcome</TableHead>
              <TableHead>When</TableHead>
              <TableHead className="pr-4">Where</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {explanation.rules.map((r) => (
              // The chain ends at the rule's own line, so it names the rule.
              <RuleRow key={`${r.kind}:${r.decision ?? ""}:${r.reason}@${r.chain.join(">")}`} rule={r} />
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );
}

function RuleRow({ rule }: { rule: ExplainEntry }) {
  return (
    <TableRow className="align-top">
      <TableCell className="pl-4">
        <div className="flex flex-wrap items-center gap-2">
          {rule.kind === "assert" ? (
            <Badge variant="outline">assert{rule.phase ? ` (${rule.phase})` : ""}</Badge>
          ) : (
            <DecisionBadge decision={rule.decision ?? ""} />
          )}
          <code className="font-mono text-xs">{rule.reason}</code>
        </div>
        {rule.payload !== undefined && rule.payload.length > 0 && (
          <div className="mt-1 font-mono text-xs text-muted-foreground">{rule.payload.join(", ")}</div>
        )}
      </TableCell>
      <TableCell className="font-mono text-xs whitespace-normal">
        {rule.conditions.length === 0 && rule.check === undefined && (
          <span className="text-muted-foreground">always</span>
        )}
        {rule.conditions.map((c, i) => (
          // biome-ignore lint/suspicious/noArrayIndexKey: a positional list, rendered whole
          <div key={i}>
            {i > 0 && <span className="text-muted-foreground">and </span>}
            {c}
          </div>
        ))}
        {rule.check !== undefined && (
          <div>
            <span className="text-muted-foreground">check </span>
            {rule.check}
          </div>
        )}
      </TableCell>
      <TableCell className="pr-4 font-mono text-xs whitespace-normal text-muted-foreground">
        {rule.chain.join(" → ")}
      </TableCell>
    </TableRow>
  );
}
