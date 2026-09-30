// Randomized alerts, each with the outcome alertrouter must answer for it.
// The named cases in requests/cases.json cover every rule once; this covers
// the combinations nobody wrote down, at volume, and derives each expectation
// from lib/rules.js rather than from a response.

import { expect as expected, fallback, formatDuration, teams } from "./rules.js";

// The mix of what the generator asks for. Production is the common case, as
// in a real Alertmanager; the rest keeps every rule busy under load.
const severities = [
  ["critical", 0.15],
  ["warning", 0.5],
  ["info", 0.35],
];
// The env label: mostly production, some pre-production, and a few alerts
// whose rule sets no env or one the platform doesn't know, which must still
// page and notify like production (null leaves the label out).
const envs = [
  ["production", 0.75],
  ["staging", 0.08],
  ["dev", 0.05],
  [null, 0.06],
  ["prod", 0.06],
];
const mutedShare = 0.1;
const componentShare = 0.4;
// A webhook's alerts are mostly firing, and a few resolved; a few firing ones
// fall outside every team's policy on purpose, to exercise the fallbacks.
const resolvedShare = 0.1;
const unownedShare = 0.02;
const invalidShare = 0.02;

// A margin around each team's page_after. A webhook alert's firing_for is the
// time since its startsAt when alertrouter evaluates it, so the generator
// keeps clear of the boundary the request's own latency could cross.
const boundaryMargin = 30;

// Generator is one VU's source of alerts. It is deterministic for a seed, so
// a run that found a wrong decision can be replayed with the same SEED.
export class Generator {
  constructor(seed, directory) {
    this.random = mulberry32(seed);
    this.directory = directory;
    this.names = Object.keys(teams).filter((name) => directory[name]);
    this.sequence = 0;
  }

  // route returns a single-alert request for POST /api/v1/teams/:team/route
  // and what it must answer.
  route() {
    const { team, alert, expect } = this.alert();
    const body = { alert: { ...alert, firing_for: formatDuration(alert.firing_for) } };

    return { team: team.name, body, expect };
  }

  // webhook returns an Alertmanager webhook of 1 to max alerts and, in the
  // same order, what alertrouter must answer for each.
  webhook(max, now) {
    const size = this.batchSize(max);
    const alerts = [];
    const expects = [];
    for (let i = 0; i < size; i++) {
      const { alert, expect } = this.webhookAlert(now);
      alerts.push(alert);
      expects.push(expect);
    }
    const body = {
      version: "4",
      groupKey: `{}:{k6="${this.sequence}"}`,
      truncatedAlerts: 0,
      status: alerts.some((a) => a.status === "firing") ? "firing" : "resolved",
      receiver: "alertrouter",
      groupLabels: {},
      commonLabels: {},
      commonAnnotations: {},
      externalURL: "http://alertmanager:9093",
      alerts,
    };

    return { body, expects };
  }

  // alert draws one alert of a known team and its expected outcome, drawing
  // again in the rare case the model says two rules would conflict.
  alert() {
    for (;;) {
      const team = this.directory[this.pick(this.names)];
      const params = teams[team.name];
      const labels = {};
      const env = this.weighted(envs);
      if (env !== null) labels.env = env;
      if (this.random() < componentShare) labels.component = this.pick(params.components);
      const alert = {
        name: this.random() < mutedShare ? this.pick(params.muted) : this.pick(params.names),
        severity: this.weighted(severities),
        labels,
        firing_for: this.firingFor(params.pageAfter),
      };
      const expect = expected(alert, team);
      if (!expect.conflict) return { team, alert, expect };
    }
  }

  // webhookAlert draws one alert in Alertmanager's shape.
  webhookAlert(now) {
    const fingerprint = this.fingerprint();
    const roll = this.random();
    const { team, alert, expect } = this.alert();
    const labels = { ...alert.labels, alertname: alert.name, severity: alert.severity, team: team.name };
    let outcome = { status: "routed", ...expect };

    if (roll < resolvedShare) {
      outcome = { status: "resolved" };
    } else if (roll < resolvedShare + unownedShare) {
      delete labels.team;
      outcome = { status: "unowned", ...fallback };
    } else if (roll < resolvedShare + unownedShare + invalidShare) {
      labels.severity = "urgent";
      outcome = { status: "invalid", ...fallback };
    }

    const startsAt = new Date(now - alert.firing_for * 1000);
    const resolved = outcome.status === "resolved";

    return {
      alert: {
        status: resolved ? "resolved" : "firing",
        labels,
        annotations: { summary: `${alert.name} from the k6 load test` },
        startsAt: startsAt.toISOString(),
        endsAt: resolved ? new Date(now).toISOString() : "0001-01-01T00:00:00Z",
        generatorURL: "http://prometheus:9090/graph",
        fingerprint,
      },
      expect: { fingerprint, alertname: alert.name, ...outcome },
    };
  }

  // batchSize draws a webhook's size: mostly a handful of alerts, sometimes
  // an incident's worth, now and then the largest batch allowed.
  batchSize(max) {
    const roll = this.random();
    const upTo = roll < 0.6 ? Math.min(10, max) : roll < 0.9 ? Math.min(50, max) : max;

    return 1 + Math.floor(this.random() * upTo);
  }

  // firingFor draws how long an alert has fired, in seconds: mostly fresh,
  // under ten minutes, otherwise up to an hour, and at least boundaryMargin
  // away from the team's page_after.
  firingFor(pageAfter) {
    for (;;) {
      const upTo = this.random() < 0.6 ? 600 : 3600;
      const seconds = Math.floor(this.random() * upTo);
      if (Math.abs(seconds - pageAfter) >= boundaryMargin) return seconds;
    }
  }

  // fingerprint returns a 16-hex-digit fingerprint unique within the VU.
  fingerprint() {
    this.sequence++;
    const high = Math.floor(this.random() * 0xffffffff);

    return high.toString(16).padStart(8, "0") + this.sequence.toString(16).padStart(8, "0");
  }

  pick(list) {
    return list[Math.floor(this.random() * list.length)];
  }

  weighted(choices) {
    let roll = this.random();
    for (const [value, weight] of choices) {
      if (roll < weight) return value;
      roll -= weight;
    }

    return choices[choices.length - 1][0];
  }
}

// mulberry32 is a small seeded PRNG: k6's Math.random can't be seeded.
function mulberry32(seed) {
  let state = seed >>> 0;

  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);

    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
