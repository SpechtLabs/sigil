// The example services' kinds, defined with the TypeScript builder the way
// their Go hosts define them with policy.NewKind. Their schema() must be
// byte-identical to the kind files the Go hosts exported.

import { decision, defineKind, enumType, fn, struct, t } from "../../src/index.js";

// examples/alert-routing: internal/routing/kind.go in the Go service.
export const Severity = enumType("Severity", ["critical", "warning", "info"]);
export const Alert = struct("Alert", {
  name: t.string,
  severity: Severity,
  labels: t.map(t.string, t.string),
  firing_for: t.duration,
});
export const Team = struct("Team", { name: t.string, oncall: t.string, channel: t.string });
export const Page = decision("page", ["critical_alert", "sustained"], { target: t.string });
export const Drop = decision("drop", ["muted", "not_production"]);
export const Notify = decision("notify", ["routine", "unrouted"], { channel: t.string.default("#alerts") });
export const AlertRouting = defineKind("AlertRouting", {
  version: 1,
  inputs: { alert: Alert, team: Team },
  decisions: [Page, Drop, Notify],
  reasonPrecedence: [Page, Drop, Notify],
  default: Notify.reason("unrouted"),
});

// examples/deploy-gates: internal/deploy/kind.go.
const Tier = enumType("Tier", ["critical", "standard", "internal"]);
const Release = struct("Release", { soak: t.duration, hotfix: t.bool });
const Service = struct("Service", {
  name: t.string,
  tier: Tier,
  owners: t.list(t.string),
  labels: t.map(t.string, t.string),
});
const DeployActor = struct("Actor", {
  name: t.string,
  teams: t.list(t.string),
  roles: t.list(t.string),
  regions: t.list(t.string),
});
const Freeze = struct("Freeze", { environments: t.list(t.string), unknown: t.bool });
export const Deny = decision("deny", ["not_eligible", "change_freeze", "soak_too_short", "no_rule_matched"]);
export const Review = decision("review", ["service_owner"], { approvers: t.list(t.string) });
export const Approve = decision("approve", ["release_manager", "payments_sre"], { bake: t.duration.default("1h") });
export const DeployApproval = defineKind("DeployApproval", {
  version: 2,
  inputs: { release: Release, service: Service, actor: DeployActor, environment: t.string, freeze: Freeze },
  functions: { split: fn([t.string, t.string], t.list(t.string), (s, sep) => s.split(sep)) },
  decisions: [Deny, Review, Approve],
  reasonPrecedence: [Deny, Approve],
  default: Deny.reason("no_rule_matched"),
});

// examples/deploy-gates: internal/access/kind.go.
const AccessActor = struct("Actor", { name: t.string, groups: t.list(t.string), clearance: t.string });
export const Reader = decision("reader", ["team_member", "everyone_in_staging"]);
export const Deployer = decision("deployer", ["team_member", "oncall"], { ttl: t.duration.default("8h") });
export const ReleaseManager = decision("release_manager", ["platform_member"], { ttl: t.duration.default("8h") });
export const Admin = decision("admin", ["clearance", "break_glass"], { ttl: t.duration.default("1h") });
export const Auditor = decision("auditor", ["compliance_member"]);
export const AccessGrant = defineKind("AccessGrant", {
  version: 1,
  inputs: { actor: AccessActor, team: t.string, environment: t.string },
  collect: [Reader, Deployer, ReleaseManager, Admin, Auditor],
  exclusive: [[Admin, ReleaseManager]],
});
