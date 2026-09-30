import { ArrowRightIcon, BellOffIcon, CheckIcon, CornerDownRightIcon, ShieldAlertIcon } from "lucide-react";
import { DecisionBadge } from "@/components/badges";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import type { CandidateResult, ErrorResponse, RouteResponse } from "@/lib/ui/api-types";
import { errorLines } from "@/lib/ui/client";
import { locationSteps, payloadValue } from "@/lib/ui/format";
import { destinationOf, platformOverride } from "@/lib/ui/preview";
import { cn } from "@/lib/utils";

/** The outcome in one line: the decision, why, and where it goes. */
export function OutcomeLine({ response }: { response: RouteResponse }) {
  const destination = destinationOf(response);
  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="outcome">
      <DecisionBadge decision={response.decision} />
      <code className="font-mono text-sm" data-testid="outcome-reason">
        {response.reason}
      </code>
      <ArrowRightIcon className="size-4 text-muted-foreground" aria-label="goes to" />
      <code className="font-mono text-sm" data-testid="outcome-destination">
        {destination === "-" ? "nowhere (dropped)" : destination}
      </code>
    </div>
  );
}

/** A humane error: the message, what to do, and its causes, indented. */
export function ErrorView({ error, title }: { error: ErrorResponse; title: string }) {
  return (
    <Alert variant="destructive">
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <div className="space-y-2">
          {errorLines(error).map((line, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: a positional list, rendered whole
            <div key={i} className={cn(i > 0 && "border-l-2 border-destructive/30 pl-3")}>
              <p className="font-mono text-xs break-words whitespace-pre-wrap">{line.message}</p>
              {line.advice.length > 0 && (
                <ul className="mt-1 list-disc pl-5 text-foreground/80">
                  {line.advice.map((a) => (
                    <li key={a}>{a}</li>
                  ))}
                </ul>
              )}
            </div>
          ))}
        </div>
      </AlertDescription>
    </Alert>
  );
}

/**
 * A routing decision and the trace that explains it: every candidate the
 * policy produced, the winner first and marked, each with the path of
 * policies that reached it, the conditions that held and its payload.
 */
/** What the error of a result means, by the alert's status. */
export function errorTitle(status: string | undefined): string {
  switch (status) {
    case "dispatch_failed":
      return "The decision stands, but the notification wasn't delivered";
    case "unowned":
      return "No team owns this alert";
    case "invalid":
      return "The router couldn't read this alert";
    default:
      return "The evaluation failed, so the fallback decided";
  }
}

/**
 * Says loudly that nobody was reached: a page or a notification the policy
 * decided that the notifier couldn't deliver.
 */
export function UndeliveredAlert({
  decision,
  destination,
  late = false,
}: {
  decision: string;
  destination: string;
  late?: boolean;
}) {
  return (
    <Alert variant="destructive" data-testid="undelivered">
      <BellOffIcon />
      <AlertTitle>
        {late ? "Not confirmed: no one is known to be reached" : "Not delivered: no one was reached"}
      </AlertTitle>
      <AlertDescription>
        {late ? (
          <>
            The {decision || "notification"} to <code>{destination}</code> hadn't gone out when the webhook had to
            answer; it keeps trying in the background, and Alertmanager sends the alert again.
          </>
        ) : (
          <>
            The {decision || "notification"} to <code>{destination}</code> failed to go out. Alertmanager retries a
            webhook that answered 503; an alert posted to the route endpoint has to be sent again.
          </>
        )}
      </AlertDescription>
    </Alert>
  );
}

/** Says that platform.paging's page replaced the team's own decision, and what that was. */
function OverrideAlert({ team, response }: { team: CandidateResult; response: RouteResponse }) {
  return (
    <Alert data-testid="override">
      <ShieldAlertIcon />
      <AlertTitle>Overridden by platform.paging</AlertTitle>
      <AlertDescription>
        <p>
          {team.policy || "The team's policy"} decided{" "}
          <code>
            {team.decision}({team.reason})
          </code>
          , but the platform pages <code>{response.target ?? "the on-call"}</code> for this alert (
          <code>{response.reason}</code>), and a team can't decide otherwise.
        </p>
      </AlertDescription>
    </Alert>
  );
}

export function TraceView({ response, status }: { response: RouteResponse; status?: string | undefined }) {
  const overridden = platformOverride(response);
  return (
    <div className="space-y-4">
      {overridden !== undefined && <OverrideAlert team={overridden} response={response} />}
      {response.error !== undefined && (
        <ErrorView
          error={response.error}
          title={overridden !== undefined ? "The team's decision broke the platform's paging rule" : errorTitle(status)}
        />
      )}
      {response.asserts !== undefined && response.asserts.length > 0 && (
        <section aria-label="Failed asserts" className="space-y-2">
          <h3 className="text-sm font-medium">Asserts that didn't hold</h3>
          <ul className="space-y-1 text-sm">
            {response.asserts.map((a) => (
              <li key={`${a.reason}@${a.location}`} className="font-mono text-xs">
                {a.reason} <span className="text-muted-foreground">at {a.location}</span>
                {a.cause !== undefined && <span className="text-destructive"> — {a.cause}</span>}
              </li>
            ))}
          </ul>
        </section>
      )}
      {response.conflict !== undefined && (
        <section aria-label="Conflict" className="space-y-2">
          <h3 className="text-sm font-medium">Candidates that can't fire together</h3>
          <CandidateList candidates={response.conflict.candidates} />
        </section>
      )}
      {response.trace.length === 0 ? (
        <p className="text-sm text-muted-foreground" data-testid="trace-empty">
          {!response.policy
            ? "No team policy ran for this alert, so its decision came without a trace."
            : `No rule of ${response.policy} fired, so the AlertRouting kind's default decided.`}
        </p>
      ) : (
        <CandidateList candidates={response.trace} overridden={overridden !== undefined} />
      )}
    </div>
  );
}

function CandidateList({ candidates, overridden = false }: { candidates: CandidateResult[]; overridden?: boolean }) {
  return (
    <ol className="space-y-3" aria-label="Candidates" data-testid="trace">
      {candidates.map((c) => (
        <li
          // The location includes the call chain, so it names the candidate.
          key={`${c.decision}:${c.reason}@${c.location}`}
          className={cn("rounded-lg border p-3", c.winner ? "border-foreground/30 bg-muted/40" : "border-dashed")}
          data-testid="trace-candidate"
          data-winner={c.winner}
        >
          <div className="flex flex-wrap items-center gap-2">
            <DecisionBadge decision={c.decision} />
            <code className="font-mono text-sm">{c.reason}</code>
            {c.winner ? (
              <Badge variant="outline" className="gap-1">
                <CheckIcon aria-hidden /> {overridden ? "team's decision" : "winner"}
              </Badge>
            ) : (
              <span className="text-xs text-muted-foreground">outranked</span>
            )}
            {c.policy !== "" && <span className="ml-auto font-mono text-xs text-muted-foreground">{c.policy}</span>}
          </div>
          <Location location={c.location} />
          {c.conditions !== undefined && c.conditions.length > 0 && (
            <div className="mt-2 space-y-1">
              {c.conditions.map((cond, j) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: a positional list, rendered whole
                <div key={j} className={cn("font-mono text-xs", indent(j))}>
                  <span className="text-muted-foreground">when </span>
                  {cond}
                </div>
              ))}
            </div>
          )}
          {Object.keys(c.payload).length > 0 && (
            <dl className="mt-2 flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs">
              {Object.entries(c.payload).map(([k, v]) => (
                <div key={k} className="flex gap-1">
                  <dt className="text-muted-foreground">{k}:</dt>
                  <dd>{payloadValue(v)}</dd>
                </div>
              ))}
            </dl>
          )}
        </li>
      ))}
    </ol>
  );
}

/** The call chain, outermost first, down to the rule. */
function Location({ location }: { location: string }) {
  const steps = locationSteps(location);
  if (steps.length === 0) return null;
  return (
    <ol className="mt-2 space-y-0.5 font-mono text-xs text-muted-foreground" aria-label="Where">
      {steps.map((s, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a positional list, rendered whole
        <li key={i} className={cn("flex items-center gap-1", indent(i))}>
          {i > 0 && <CornerDownRightIcon className="size-3" aria-hidden />}
          <span className={cn(i === steps.length - 1 && "text-foreground")}>{s.raw}</span>
          <span className="sr-only">{i === steps.length - 1 ? "(the rule)" : "(calls)"}</span>
        </li>
      ))}
    </ol>
  );
}

// Nesting as fixed steps, so the classes exist for Tailwind to generate.
const INDENT = ["", "pl-3", "pl-6", "pl-9", "pl-12"] as const;

function indent(depth: number): string {
  return INDENT[Math.min(depth, INDENT.length - 1)] ?? "";
}
