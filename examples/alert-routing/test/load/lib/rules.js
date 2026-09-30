// A model of the example policies, so the generator knows what alertrouter
// must answer for an alert nobody wrote down. It mirrors, rule for rule:
//
//   policies/platform/paging.sigil   critical production alerts page the on-call, and
//                                    production warnings page after page_after
//   policies/platform/routing.sigil  other production warnings go to the team channel;
//                                    staging drops; muted alert names drop
//   policies/teams/<team>/alerts.sigil  each team's params and its own notify rule
//
// and the kind's collect one, precedence and default (policies/alert_routing.sigil).
//
// A model can drift from the policies it copies. setup() checks it against
// every single-alert case in requests/cases.json before any load starts, and
// the Go tests check those cases against the real policies, so a policy edit
// that isn't mirrored here fails the run at once instead of reading as a
// thousand wrong decisions.

// The kind's precedence: decisions first, then reasons within a decision.
const decisionRank = { page: 3, drop: 2, notify: 1 };
const reasonRank = {
  page: { critical_alert: 2, sustained: 1 },
  drop: { muted: 2, not_production: 1 },
  notify: { routine: 2, unrouted: 1 },
};

// The kind's default, which is also what an unowned or invalid alert gets.
export const fallback = { decision: 'notify', reason: 'unrouted', channel: '#alerts' };

// Each team's arguments to platform.paging (pageAfter) and platform.routing
// (muted), and its own rules. The on-call target and channel come from
// alertrouter's team directory at setup.
export const teams = {
  checkout: {
    pageAfter: 10 * 60,
    muted: ['CheckoutCanaryLatency'],
    rules: [
      { severity: 'info', component: 'payments', channel: '#checkout-payments' },
    ],
    names: ['CheckoutLatencyHigh', 'CheckoutErrorRate', 'CheckoutPodRestarted', 'CheckoutCartAbandonment'],
    components: ['payments', 'cart', 'frontend'],
  },
  payments: {
    pageAfter: 5 * 60,
    muted: ['PaymentsSettlementBatchSlow'],
    rules: [
      { severity: 'info', component: 'ledger', channel: '#payments-ledger' },
    ],
    names: ['PaymentsAuthorizationErrors', 'PaymentsLatencyHigh', 'LedgerReplicationLag', 'PaymentsWebhookBacklog'],
    components: ['ledger', 'gateway', 'fraud'],
  },
};

// expect returns what alertrouter must answer for an alert of a team, as
// {decision, reason, target?, channel?}, or {conflict: true} when two rules
// decide the same reason with different payloads. alert is the kind's Alert,
// firing_for in seconds; team is the directory entry {name, oncall, channel}.
export function expect(alert, team) {
  const params = teams[team.name];
  if (!params) throw new Error(`lib/rules.js has no model of team ${team.name}`);

  const production = alert.labels.env === 'production';
  const candidates = [];

  // platform.paging
  if (production && alert.severity === 'critical') {
    candidates.push({ decision: 'page', reason: 'critical_alert', target: team.oncall });
  }
  if (production && alert.severity === 'warning' && alert.firing_for >= params.pageAfter) {
    candidates.push({ decision: 'page', reason: 'sustained', target: team.oncall });
  }

  // platform.routing
  if (production && alert.severity === 'warning') {
    candidates.push({ decision: 'notify', reason: 'routine', channel: team.channel });
  }
  if (!production) candidates.push({ decision: 'drop', reason: 'not_production' });
  if (params.muted.includes(alert.name)) candidates.push({ decision: 'drop', reason: 'muted' });

  // The team's own rules.
  for (const rule of params.rules) {
    if (production && alert.severity === rule.severity && alert.labels.component === rule.component) {
      candidates.push({ decision: 'notify', reason: 'routine', channel: rule.channel });
    }
  }

  if (candidates.length === 0) return { ...fallback };

  // Two candidates with the same decision and reason but a different payload
  // are a conflict, which alertrouter reports as a failed evaluation. The
  // generator never asks for one, so this is conservative: it flags a tie
  // even where a higher-precedence decision would have won anyway.
  const seen = {};
  for (const c of candidates) {
    const key = `${c.decision}/${c.reason}`;
    const payload = JSON.stringify([c.target, c.channel]);
    if (seen[key] !== undefined && seen[key] !== payload) return { conflict: true };
    seen[key] = payload;
  }

  candidates.sort((a, b) =>
    decisionRank[b.decision] - decisionRank[a.decision] ||
    reasonRank[b.decision][b.reason] - reasonRank[a.decision][a.reason]);

  return candidates[0];
}

// parseDuration converts a Go duration string such as "12m" or "1h30m5s"
// into seconds, the way RouteRequest.firing_for is written.
export function parseDuration(text) {
  if (text === undefined || text === null || text === '' || text === '0') return 0;
  const units = { ns: 1e-9, us: 1e-6, 'µs': 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  const pattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let seconds = 0;
  let consumed = 0;
  for (const match of text.matchAll(pattern)) {
    seconds += Number(match[1]) * units[match[2]];
    consumed += match[0].length;
  }
  if (consumed !== text.length) throw new Error(`${text} is not a Go duration`);

  return seconds;
}

// formatDuration writes seconds as a Go duration string, whole seconds only.
export function formatDuration(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const rest = s % 60;

  return `${h ? `${h}h` : ''}${m ? `${m}m` : ''}${rest || (!h && !m) ? `${rest}s` : ''}`;
}
