"use client";

import { CheckCircle2Icon, CircleAlertIcon, CpuIcon, SendIcon, ServerIcon } from "lucide-react";
import Link from "next/link";
import { type FormEvent, useEffect, useId, useMemo, useState } from "react";
import { toast } from "sonner";
import { StatusBadge } from "@/components/badges";
import { useEvents } from "@/components/events-provider";
import { ErrorView, OutcomeLine, TraceView, UndeliveredAlert } from "@/components/trace-view";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Switch } from "@/components/ui/switch";
import { usePreview } from "@/components/use-preview";
import {
  type AlertFormValues,
  DEFAULT_VALUES,
  ENVIRONMENTS,
  type FormErrors,
  NO_ENV,
  NO_TEAM,
  randomFingerprint,
  routeRequest,
  SEVERITIES,
  type Severity,
  validate,
  webhookBody,
} from "@/lib/ui/alert-form";
import type { ErrorResponse, FeedItem, RouteResponse, Team, TeamsResponse, WebhookResponse } from "@/lib/ui/api-types";
import { errorOf, postJSON, request } from "@/lib/ui/client";
import { compareOutcomes, destinationOf, routeResponseFromAlertResult, routingAlert } from "@/lib/ui/preview";

/** The preview of the form's current values, recomputed as they change. */
type PreviewResult =
  | { kind: "idle" }
  | { kind: "ok"; response: RouteResponse; key: string }
  | { kind: "error"; message: string; key: string };

/** What the server answered to the latest send. */
interface ServerAnswer {
  mode: AlertFormValues["mode"];
  httpStatus: number;
  sentAt: string;
  alertname: string;
  /** Set for a webhook send, to find the alert in the history. */
  fingerprint?: string;
  /** For a webhook: routed, unowned, invalid or failed. */
  alertStatus?: string;
  /** The decision the server made, when it made one; the 400 and 404 answers carry none. */
  response?: RouteResponse;
  error?: ErrorResponse;
  /** The preview of the values that were sent, to compare the answer with. */
  preview?: RouteResponse;
}

// How long the form waits after a keystroke before previewing again.
const PREVIEW_DEBOUNCE_MS = 150;

export function SendAlert() {
  const { policyStatus, entries } = useEvents();
  const preview = usePreview(policyStatus?.fingerprint);
  const [teams, setTeams] = useState<Team[] | undefined>();
  const [teamsError, setTeamsError] = useState<string | undefined>();
  const [values, setValues] = useState<AlertFormValues>(DEFAULT_VALUES);
  const [errors, setErrors] = useState<FormErrors>({});
  const [previewResult, setPreviewResult] = useState<PreviewResult>({ kind: "idle" });
  const [sending, setSending] = useState(false);
  const [answer, setAnswer] = useState<ServerAnswer | undefined>();

  useEffect(() => {
    request<TeamsResponse>("/api/v1/teams").then(
      (res) => setTeams(res.teams),
      (err: unknown) => setTeamsError(err instanceof Error ? err.message : String(err)),
    );
  }, []);

  const teamNames = useMemo(() => teams?.map((t) => t.name) ?? [], [teams]);
  // The defaults name the example's checkout team; another directory starts at its first team.
  useEffect(() => {
    const first = teamNames[0];
    if (first === undefined) return;
    setValues((v) => (v.team === NO_TEAM || teamNames.includes(v.team) ? v : { ...v, team: first }));
  }, [teamNames]);
  const set = <K extends keyof AlertFormValues>(key: K, value: AlertFormValues[K]) => {
    setValues((v) => {
      const next = { ...v, [key]: value };
      // A route request always names a team from the directory.
      if (key === "mode" && value === "route" && next.team === NO_TEAM) next.team = teamNames[0] ?? "";
      return next;
    });
    setErrors((e) => ({ ...e, [key]: undefined }));
  };

  // Preview as the values change, once they are valid.
  const previewKey = JSON.stringify(values);
  useEffect(() => {
    if (preview.state.status !== "ready" || teams === undefined) return;
    if (Object.keys(validate(values, teamNames)).length > 0) {
      setPreviewResult({ kind: "idle" });
      return;
    }
    let cancelled = false;
    const t = setTimeout(() => {
      const team = teams.find((x) => x.name === values.team);
      preview.evaluate(routingAlert(values), team).then(
        (response) => !cancelled && setPreviewResult({ kind: "ok", response, key: previewKey }),
        (err: unknown) =>
          !cancelled &&
          setPreviewResult({
            kind: "error",
            message: err instanceof Error ? err.message : String(err),
            key: previewKey,
          }),
      );
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [preview.state, preview.evaluate, teams, teamNames, values, previewKey]);

  async function onSubmit(ev: FormEvent) {
    ev.preventDefault();
    const found = validate(values, teamNames);
    setErrors(found);
    if (Object.keys(found).length > 0) {
      toast.error("Fix the highlighted fields first.");
      return;
    }
    setSending(true);
    const sentAt = new Date();
    // Compare with the preview of exactly the values sent: the one on screen
    // when it's current, else one made now, alongside the send.
    const current =
      previewResult.kind === "ok" && previewResult.key === previewKey ? previewResult.response : undefined;
    const previewAtSend: Promise<RouteResponse | undefined> =
      current !== undefined || preview.state.status !== "ready"
        ? Promise.resolve(current)
        : preview
            .evaluate(
              routingAlert(values),
              teams?.find((x) => x.name === values.team),
            )
            .catch(() => undefined);
    try {
      const [result, previewed] = await Promise.all([sendAlert(values, sentAt), previewAtSend]);
      const next: ServerAnswer = { ...result, preview: previewed };
      setAnswer(next);
      if (next.response !== undefined) {
        toast.success(
          `${next.response.decision} ${next.response.reason} → ${destinationOf(next.response)} (HTTP ${next.httpStatus})`,
        );
      } else {
        toast.error(next.error?.message ?? `The server answered ${next.httpStatus}.`);
      }
    } catch (err) {
      toast.error(`Couldn't reach alertrouter: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setSending(false);
    }
  }

  const historyEntry = answer === undefined ? undefined : findEntry(entries, answer);

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
      <Card className="self-start">
        <CardHeader>
          <CardTitle>Alert</CardTitle>
          <CardDescription>The decision previews in your browser as you type.</CardDescription>
        </CardHeader>
        <CardContent>
          {teamsError !== undefined && (
            <Alert variant="destructive" className="mb-4">
              <AlertTitle>The team directory couldn't be loaded</AlertTitle>
              <AlertDescription>{teamsError}</AlertDescription>
            </Alert>
          )}
          <AlertForm
            values={values}
            errors={errors}
            teams={teams}
            sending={sending}
            onChange={set}
            onSubmit={onSubmit}
          />
        </CardContent>
      </Card>
      <div className="space-y-6">
        <PreviewCard state={preview.state} result={previewResult} currentKey={previewKey} />
        <ServerCard answer={answer} entry={historyEntry} />
      </div>
    </div>
  );
}

function AlertForm({
  values,
  errors,
  teams,
  sending,
  onChange,
  onSubmit,
}: {
  values: AlertFormValues;
  errors: FormErrors;
  teams: Team[] | undefined;
  sending: boolean;
  onChange: <K extends keyof AlertFormValues>(key: K, value: AlertFormValues[K]) => void;
  onSubmit: (ev: FormEvent) => void;
}) {
  const id = useId();
  const fid = (name: string) => `${id}-${name}`;
  return (
    <form onSubmit={onSubmit} noValidate aria-label="Send an alert">
      <FieldGroup>
        <Field orientation="horizontal">
          <Switch
            id={fid("webhook")}
            checked={values.mode === "webhook"}
            onCheckedChange={(on) => onChange("mode", on ? "webhook" : "route")}
          />
          <FieldContent>
            <FieldLabel htmlFor={fid("webhook")}>Send as Alertmanager webhook</FieldLabel>
            <FieldDescription>
              {values.mode === "webhook" ? (
                <>
                  <code>POST /api/v1/alerts</code>, as Alertmanager would: name, severity and team travel as labels.
                </>
              ) : (
                <>
                  <code>POST /api/v1/teams/{values.team || ":team"}/route</code>, one alert for one team.
                </>
              )}
            </FieldDescription>
          </FieldContent>
        </Field>

        <Field data-invalid={errors.team !== undefined}>
          <FieldLabel htmlFor={fid("team")}>Team</FieldLabel>
          {teams === undefined ? (
            <Skeleton className="h-9 w-full" />
          ) : (
            <Select value={values.team} onValueChange={(v) => onChange("team", v)}>
              <SelectTrigger id={fid("team")} className="w-full" aria-invalid={errors.team !== undefined}>
                <SelectValue placeholder="Pick a team" />
              </SelectTrigger>
              <SelectContent>
                {teams.map((t) => (
                  <SelectItem key={t.name} value={t.name}>
                    {t.name}
                  </SelectItem>
                ))}
                {values.mode === "webhook" && <SelectItem value={NO_TEAM}>No team label (unowned)</SelectItem>}
              </SelectContent>
            </Select>
          )}
          <FieldError>{errors.team}</FieldError>
        </Field>

        <Field data-invalid={errors.name !== undefined}>
          <FieldLabel htmlFor={fid("name")}>Alert name</FieldLabel>
          <Input
            id={fid("name")}
            value={values.name}
            onChange={(e) => onChange("name", e.target.value)}
            aria-invalid={errors.name !== undefined}
            autoComplete="off"
            spellCheck={false}
          />
          <FieldDescription>The alerting rule's name; a team can mute alerts by name.</FieldDescription>
          <FieldError>{errors.name}</FieldError>
        </Field>

        <FieldSet data-invalid={errors.severity !== undefined}>
          <FieldLegend variant="label">Severity</FieldLegend>
          <RadioGroup
            value={values.severity}
            onValueChange={(v) => onChange("severity", v as Severity)}
            className="flex flex-wrap gap-4"
          >
            {SEVERITIES.map((s) => (
              <Field key={s} orientation="horizontal" className="w-auto">
                <RadioGroupItem value={s} id={fid(`severity-${s}`)} />
                <FieldLabel htmlFor={fid(`severity-${s}`)} className="font-normal">
                  {s}
                </FieldLabel>
              </Field>
            ))}
          </RadioGroup>
          <FieldError>{errors.severity}</FieldError>
        </FieldSet>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor={fid("env")}>
              <code>env</code> label
            </FieldLabel>
            <Select value={values.env} onValueChange={(v) => onChange("env", v)}>
              <SelectTrigger id={fid("env")} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ENVIRONMENTS.map((e) => (
                  <SelectItem key={e} value={e}>
                    {e}
                  </SelectItem>
                ))}
                <SelectItem value={NO_ENV}>No env label</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field>
            <FieldLabel htmlFor={fid("component")}>
              <code>component</code> label
            </FieldLabel>
            <Input
              id={fid("component")}
              value={values.component}
              placeholder="none"
              onChange={(e) => onChange("component", e.target.value)}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        </div>

        <Field data-invalid={errors.firingFor !== undefined}>
          <FieldLabel htmlFor={fid("firing")}>Firing for</FieldLabel>
          <Input
            id={fid("firing")}
            value={values.firingFor}
            onChange={(e) => onChange("firingFor", e.target.value)}
            aria-invalid={errors.firingFor !== undefined}
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
          />
          <FieldDescription>
            A duration such as 90s, 12m or 1h30m. Warnings page once they fire long enough.
          </FieldDescription>
          <FieldError>{errors.firingFor}</FieldError>
        </Field>

        <Button type="submit" disabled={sending || teams === undefined} className="w-full sm:w-auto">
          {sending ? <Spinner /> : <SendIcon />}
          Send to the router
        </Button>
      </FieldGroup>
    </form>
  );
}

function PreviewCard({
  state,
  result,
  currentKey,
}: {
  state: ReturnType<typeof usePreview>["state"];
  result: PreviewResult;
  currentKey: string;
}) {
  const stale = result.kind !== "idle" && result.key !== currentKey;
  return (
    <Card data-testid="preview">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <CpuIcon className="size-4" aria-hidden /> Preview
        </CardTitle>
        <CardDescription>
          Evaluated in your browser by sigil.wasm, against the policies the server serves.
        </CardDescription>
        <CardAction>
          {state.status === "ready" && (
            <Badge variant="outline" className="font-mono" title="The served bundle's fingerprint">
              {state.fingerprint.slice(0, 12)}
            </Badge>
          )}
        </CardAction>
      </CardHeader>
      <CardContent aria-live="polite" aria-busy={state.status === "loading" || stale}>
        {state.status === "loading" && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Spinner /> Loading the policy engine…
          </div>
        )}
        {state.status === "unavailable" && (
          <Alert data-testid="preview-unavailable">
            <CircleAlertIcon />
            <AlertTitle>Preview unavailable</AlertTitle>
            <AlertDescription>
              <p>{state.reason}</p>
              <p>Sending still works; the server's answer is the one that counts.</p>
            </AlertDescription>
          </Alert>
        )}
        {state.status === "ready" && result.kind === "idle" && (
          <p className="text-sm text-muted-foreground">Fill in the alert to see its decision.</p>
        )}
        {state.status === "ready" && result.kind === "error" && (
          <Alert variant="destructive">
            <AlertTitle>The preview failed</AlertTitle>
            <AlertDescription>{result.message}</AlertDescription>
          </Alert>
        )}
        {state.status === "ready" && result.kind === "ok" && (
          <div className="space-y-4 transition-opacity data-[stale=true]:opacity-60" data-stale={stale}>
            <OutcomeLine response={result.response} />
            <TraceView response={result.response} />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function ServerCard({ answer, entry }: { answer: ServerAnswer | undefined; entry: FeedItem | undefined }) {
  // The route endpoint's answer has no status; the history entry it made does.
  const status = answer?.alertStatus ?? entry?.status;
  return (
    <Card data-testid="server-answer">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ServerIcon className="size-4" aria-hidden /> Server
        </CardTitle>
        <CardDescription>What alertrouter decided and dispatched. This answer is authoritative.</CardDescription>
        {answer !== undefined && (
          <CardAction className="flex items-center gap-2">
            {status !== undefined && <StatusBadge status={status} />}
            <Badge variant={answer.httpStatus < 300 ? "outline" : "destructive"} className="font-mono">
              HTTP {answer.httpStatus}
            </Badge>
          </CardAction>
        )}
      </CardHeader>
      <CardContent aria-live="polite">
        {answer === undefined ? (
          <p className="text-sm text-muted-foreground">Send the alert to see the server's decision.</p>
        ) : (
          <div className="space-y-4">
            {answer.response !== undefined && answer.preview !== undefined && (
              <Agreement preview={answer.preview} server={answer.response} />
            )}
            {answer.response !== undefined ? (
              <>
                {status === "dispatch_failed" && (
                  <UndeliveredAlert decision={answer.response.decision} destination={destinationOf(answer.response)} />
                )}
                <OutcomeLine response={answer.response} />
                <TraceView response={answer.response} status={status} />
              </>
            ) : (
              answer.error !== undefined && <ErrorView error={answer.error} title="The server refused the alert" />
            )}
            {entry !== undefined && (
              <Button asChild variant="link" className="px-0">
                <Link href={`/alerts/${entry.id}`}>Open it in the history</Link>
              </Button>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function Agreement({ preview, server }: { preview: RouteResponse; server: RouteResponse }) {
  const { agrees, differences } = compareOutcomes(preview, server);
  if (agrees) {
    return (
      <Alert data-testid="agreement" data-agrees="true">
        <CheckCircle2Icon />
        <AlertTitle>The preview matches the server</AlertTitle>
        <AlertDescription>Same decision, reason and destination.</AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert variant="destructive" data-testid="agreement" data-agrees="false">
      <CircleAlertIcon />
      <AlertTitle>The preview and the server disagree on the {differences.join(", ")}</AlertTitle>
      <AlertDescription>
        The server's answer stands. The policies may have been reloaded between the preview and the send; the preview
        follows the next reload by itself.
      </AlertDescription>
    </Alert>
  );
}

/** Sends the alert the way the form says and reads the answer, whatever its status. */
async function sendAlert(values: AlertFormValues, sentAt: Date): Promise<Omit<ServerAnswer, "preview">> {
  const base = { mode: values.mode, sentAt: sentAt.toISOString(), alertname: values.name };
  if (values.mode === "route") {
    const { status, body } = await postJSON(
      `/api/v1/teams/${encodeURIComponent(values.team)}/route`,
      routeRequest(values),
    );
    // 200, 422, 500 and 503 carry a decision; 400, 404 and 413 only an error.
    const decided = body !== null && typeof (body as RouteResponse).decision === "string";
    return decided
      ? { ...base, httpStatus: status, response: body as RouteResponse, error: (body as RouteResponse).error }
      : { ...base, httpStatus: status, error: errorOf(status, body) };
  }
  const fingerprint = randomFingerprint();
  const { status, body } = await postJSON("/api/v1/alerts", webhookBody(values, sentAt, fingerprint));
  // 200 and 503 (a notification that didn't go out) both carry the results.
  const result = Array.isArray((body as WebhookResponse | null)?.results)
    ? (body as WebhookResponse).results[0]
    : undefined;
  if (result === undefined) return { ...base, httpStatus: status, fingerprint, error: errorOf(status, body) };
  return {
    ...base,
    httpStatus: status,
    fingerprint,
    alertStatus: result.status,
    response: routeResponseFromAlertResult(result),
  };
}

/** The history entry the send produced, once the event stream brings it. */
function findEntry<T extends { alertname: string; fingerprint: string; at: string; source: string }>(
  entries: readonly T[],
  answer: ServerAnswer,
): T | undefined {
  if (answer.fingerprint !== undefined) return entries.find((e) => e.fingerprint === answer.fingerprint);
  // A route request has no fingerprint: take the newest entry for the alert
  // since the send, allowing for the clocks of browser and server to differ.
  const since = new Date(answer.sentAt).getTime() - 5_000;
  return entries.find(
    (e) => e.source === "route" && e.alertname === answer.alertname && new Date(e.at).getTime() >= since,
  );
}
