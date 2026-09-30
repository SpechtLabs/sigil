// Sends every sample request in requests/cases.json to a running alertrouter,
// the single alerts to their team's route endpoint and the Alertmanager
// batches to the webhook, so the console's feed and the Grafana dashboard
// have something to show. Each line says what alertrouter decided and
// whether that's what the case expects.
//
//   bun run scripts/demo.ts [--url http://localhost:8080]
//
// The URL comes from --url, else ALERTROUTER_URL, else localhost:8080.

import { readFileSync } from "node:fs";

interface Case {
  name: string;
  kind: "route" | "webhook";
  team?: string | null;
  file: string;
  status?: number;
  expect: Record<string, unknown> & { results?: Record<string, unknown>[] };
}

const requestsDir = new URL("../requests/", import.meta.url);
const flag = process.argv.indexOf("--url");
const base = (flag >= 0 ? process.argv[flag + 1] : undefined) ?? process.env.ALERTROUTER_URL ?? "http://localhost:8080";

const cases = JSON.parse(readFileSync(new URL("cases.json", requestsDir), "utf8")) as Case[];
let wrong = 0;

for (const c of cases) {
  const path = c.kind === "route" ? `/api/v1/teams/${c.team}/route` : "/api/v1/alerts";
  let res: Response;
  try {
    res = await fetch(new URL(path, base), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: readFileSync(new URL(c.file, requestsDir)),
    });
  } catch (err) {
    console.error(
      `alertrouter at ${base} didn't answer (${err instanceof Error ? err.message : String(err)}); start it with \`mise run up\` or \`mise run dev\`, or pass --url`,
    );
    process.exit(1);
  }
  const body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
  const ok = res.status === (c.status ?? 200) && matches(c, body);
  if (!ok) wrong++;
  console.log(`${ok ? "✓" : "✗"} ${c.name.padEnd(28)} ${res.status} ${summary(c, body)}`);
}

console.log(`\n${cases.length - wrong} of ${cases.length} cases answered as requests/cases.json expects`);
process.exit(wrong === 0 ? 0 : 1);

/** Whether the answer has every field the case expects; for a batch, per alert by fingerprint. */
function matches(c: Case, body: Record<string, unknown>): boolean {
  if (c.kind === "route") return Object.entries(c.expect).every(([k, v]) => body[k] === v);
  const got = new Map(((body.results as Record<string, unknown>[]) ?? []).map((r) => [r.fingerprint, r]));
  return (c.expect.results ?? []).every((want) => {
    const r = got.get(want.fingerprint);
    return r !== undefined && Object.entries(want).every(([k, v]) => r[k] === v);
  });
}

/** One line of what alertrouter answered. */
function summary(c: Case, body: Record<string, unknown>): string {
  const decision = (r: Record<string, unknown>) =>
    r.decision ? `${r.decision}(${r.reason}) ${r.target ?? r.channel ?? ""}`.trim() : String(r.status ?? "");
  if (typeof body.error === "object" && body.error !== null)
    return String((body.error as { message?: string }).message);
  if (c.kind === "route") return decision(body);
  const results = (body.results as Record<string, unknown>[]) ?? [];
  return `${body.received} received, ${body.routed} routed: ${results.map(decision).join(", ")}`;
}
