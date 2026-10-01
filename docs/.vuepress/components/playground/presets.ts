// The workspaces the playground starts from. Every source is copied from
// the docs or the examples, so a preset decides exactly what the page it
// comes from says; update both together.

import type { Workspace } from "./workspace.js";

export interface Preset {
  id: string;
  label: string;
  workspace: Workspace;
}

// The tour (docs/getting-started/tour.md): the AlertRouting kind and the
// checkout team's policy. The input is checkout/testdata/latency.json from
// docs/getting-started/check-and-test.md.
const tourKind = `kind AlertRouting version 1

enum Severity: critical | warning | info

type Alert {
  name: string
  severity: Severity
  labels: map<string, string>
  firing_for: duration
}

type Team {
  name: string
  oncall: string
  channel: string
}

input alert: Alert
input team: Team

decision page {
  reason: critical_alert | sustained
  target: string
}

decision drop {
  reason: muted | not_production
}

decision notify {
  reason: routine | unrouted
  channel: string = "#alerts"
}

collect one
precedence page > drop > notify
precedence page: critical_alert > sustained
precedence drop: muted > not_production
precedence notify: routine > unrouted

default notify(reason: unrouted)
`;
const tourPolicy = `policy checkout.alerts: AlertRouting@1

let pre_production = alert.labels["env"] in ["staging", "dev"]

when not pre_production and alert.severity == critical {
  page(reason: critical_alert, target: team.oncall)
}

when not pre_production and alert.severity == warning {
  when alert.firing_for >= 30m {
    page(reason: sustained, target: team.oncall)
  }

  notify(reason: routine, channel: team.channel)
}

when pre_production {
  drop(reason: not_production)
}

when alert.name in ["CheckoutCanaryLatency"] {
  drop(reason: muted)
}
`;

const latency = `{
  "alert": {
    "name": "CheckoutLatencyHigh",
    "severity": "warning",
    "labels": { "env": "production" },
    "firing_for": "45m"
  },
  "team": { "name": "checkout", "oncall": "checkout-primary", "channel": "#checkout-alerts" }
}
`;

// Deploy approval (examples/deploy-gates/policies, as the guides lay it out):
// payments.production invokes the platform's policies, and deploy.common
// calls the host function split. The stubs are the ones
// docs/guides/test-policies.md gives it.

const deployKind = `kind DeployApproval version 1

enum Tier: critical | standard | internal

type Release {
  soak: duration
  hotfix: bool
}

type Service {
  name: string
  tier: Tier
  owners: list<string>
  labels: map<string, string>
}

type Actor {
  name: string
  teams: list<string>
  roles: list<string>
  regions: list<string>
}

input release: Release
input service: Service
input actor: Actor
input environment: string

fn split(string, string) -> list<string>

decision deny {
  reason: not_eligible | soak_too_short | no_rule_matched
}

decision review {
  reason: service_owner
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}

collect one
precedence deny > review > approve
precedence deny: not_eligible > soak_too_short > no_rule_matched
precedence approve: release_manager > payments_sre

default deny(reason: no_rule_matched)
`;

const deployCommon = `module deploy.common: DeployApproval@1

pub let owns_service = actor.teams any in service.owners
pub let cleared =
  split(service.labels["regions"], ",") all in actor.regions
pub let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }
`;

const deployGuardrails = `policy deploy.guardrails: DeployApproval@1

use deploy.common.{eligible}

param min_soak: duration = 24h, min: 1h, max: 48h

when not eligible {
  deny(reason: not_eligible)
}

when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}
`;

const deployProduction = `policy deploy.production: DeployApproval@1

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<Tier> = [standard, internal]

when cleared {
  when service.tier == critical
    and "release_manager" in actor.roles {
    approve(reason: release_manager)
  }

  when service.tier in tiers
    and owns_service {
    review(reason: service_owner, approvers: approvers)
  }
}
`;

const paymentsProduction = `policy payments.production: DeployApproval@1

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when cleared and "payments-sre" in actor.teams {
  approve(reason: payments_sre, bake: 15m)
}
`;

const deployOwner = `{
  "release": {"soak": "6h", "hotfix": false},
  "service": {
    "name": "ledger",
    "tier": "standard",
    "owners": ["payments"],
    "labels": {
      "app.kubernetes.io/managed-by": "argocd",
      "platform.example.com/lifecycle": "ga",
      "regions": "eu,us",
      "compliance": "pci"
    }
  },
  "actor": {
    "name": "ada",
    "teams": ["payments"],
    "roles": ["deployer"],
    "regions": ["eu", "us"]
  },
  "environment": "production"
}
`;

const splitStubs = `split:
  calls:
    - args: ["eu,us", ","]
      returns: [eu, us]
    - args: ["eu,us,ap", ","]
      returns: [eu, us, ap]
`;

// Access grants (examples/deploy-gates/policies): a kind that collects every
// grant instead of picking one, with asserts guarding the outcome.

const accessKind = `kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor
input team: string
input environment: string

decision reader {
  reason: team_member | everyone_in_staging
}

decision deployer {
  reason: team_member | oncall
  ttl: duration = 8h
}

decision release_manager {
  reason: platform_member
  ttl: duration = 8h
}

decision admin {
  reason: clearance | break_glass
  ttl: duration = 1h
}

decision auditor {
  reason: compliance_member
}

collect all
exclusive admin, release_manager
`;

const accessMain = `policy access.main: AccessGrant@1

use access.guardrails
use access.common.{admin_cleared, break_glass, compliance_member, on_call, platform_member, team_member}

guardrails()

when team_member {
  reader(reason: team_member)
  deployer(reason: team_member)
}

when environment == "staging" {
  reader(reason: everyone_in_staging)
}

// The on-call SRE deploys for two hours, long enough to ship a fix.
when on_call {
  deployer(reason: oncall, ttl: 2h)
}

// An admin already holds everything a release manager does, and the kind
// declares the two exclusive, so the platform grant leaves admins out.
when platform_member and not admin_cleared {
  release_manager(reason: platform_member, ttl: 4h)
}

when admin_cleared {
  admin(reason: clearance)
}

// Break-glass access is short-lived by design. It doesn't check for platform
// membership: a break-glass member in platform is a conflict the host reports,
// because the two groups aren't meant to overlap.
when break_glass {
  admin(reason: break_glass, ttl: 15m)
}

when compliance_member {
  auditor(reason: compliance_member)
}
`;

const accessCommon = `module access.common: AccessGrant@1

// Sigil has no string concatenation, so a policy can't build "<team>-sre"
// from team, and a glob pattern must be a literal. The platform lists each
// team's on-call rotation group instead, and a team without an entry has no
// on-call grant: the lookup is guarded, since a missing key reads as "".
pub let sre_groups = {"payments": "payments-sre", "checkout": "checkout-sre"}

pub let team_member = team in actor.groups
pub let on_call = sre_groups has team and sre_groups[team] in actor.groups
pub let platform_member = "platform" in actor.groups
pub let admin_cleared = actor.clearance == "admin"
pub let break_glass = "break-glass" in actor.groups
pub let compliance_member = "compliance" in actor.groups
`;

const accessGuardrails = `policy access.guardrails: AccessGrant@1

// Every grant is recorded against the actor's name, so a request without one
// is an error, not an empty outcome. It's an input assert: it reads no
// outcome, so it runs before any rule and no role is granted to nobody.
assert("named_actor", actor.name != "")

// Separation of duties: whoever audits a team's deploys can't also deploy
// them. It reads outcome, so it runs once every role has been collected, and
// it catches a deployer grant from any rule, in this policy or the root.
assert("sod_auditor_deployer", [auditor, deployer] exclusive in outcome)
`;

const accessMember = `{
  "actor": {
    "name": "ada",
    "groups": ["payments"],
    "clearance": "standard"
  },
  "team": "payments",
  "environment": "production"
}
`;

export const presets: Preset[] = [
  {
    id: "alert-routing",
    label: "Alert routing",
    workspace: {
      files: [
        { path: "alert_routing.sigil", source: tourKind },
        { path: "checkout/alerts.sigil", source: tourPolicy },
      ],
      input: latency,
      stubs: "",
      policy: "checkout.alerts",
      mode: "evaluate",
    },
  },
  {
    id: "deploy-approval",
    label: "Deploy approval, with a stubbed host function",
    workspace: {
      files: [
        { path: "deploy_approval.sigil", source: deployKind },
        { path: "payments/production.sigil", source: paymentsProduction },
        { path: "deploy/common.sigil", source: deployCommon },
        { path: "deploy/guardrails.sigil", source: deployGuardrails },
        { path: "deploy/production.sigil", source: deployProduction },
      ],
      input: deployOwner,
      stubs: splitStubs,
      policy: "payments.production",
      mode: "evaluate",
    },
  },
  {
    id: "access-grants",
    label: "Access grants, collecting every decision",
    workspace: {
      files: [
        { path: "access_grant.sigil", source: accessKind },
        { path: "access/main.sigil", source: accessMain },
        { path: "platform/access/common.sigil", source: accessCommon },
        { path: "platform/access/guardrails.sigil", source: accessGuardrails },
      ],
      input: accessMember,
      stubs: "",
      policy: "access.main",
      mode: "evaluate",
    },
  },
];

/** A copy of a preset's workspace, safe to edit. */
export function presetWorkspace(id: string): Workspace {
  const preset = presets.find((p) => p.id === id) ?? presets[0];
  return structuredClone(preset.workspace);
}
