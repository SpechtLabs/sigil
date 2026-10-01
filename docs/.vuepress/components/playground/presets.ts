// The workspaces the playground starts from. Each preset is a directory
// under presets/, laid out as a policy repository is, and every file in it
// is copied from the docs or the examples:
//
//   alert-routing    The tour (docs/getting-started/tour.md): the
//                    AlertRouting kind and checkout.alerts, with the test
//                    file and fixture of docs/getting-started/check-and-test.md,
//                    whose last case fails on purpose.
//   deploy-approval  examples/deploy-gates as the guides lay it out:
//                    payments.production invokes the platform's policies, and
//                    deploy.common calls the host function split, stubbed as
//                    docs/guides/test-policies.md stubs it.
//   access-grants    examples/deploy-gates' AccessGrant, a kind that collects
//                    every grant, with asserts guarding the outcome.
//
// Update a preset with the page it comes from.

import type { SourceFile } from "@spechtlabs/sigil/worker";
import type { Workspace } from "./workspace.js";

export interface Preset {
  id: string;
  label: string;
  /** The policy Evaluate runs. */
  policy: string;
  /** The data file Evaluate's input starts as. */
  input: string;
  /** Evaluate's stubs, YAML. */
  stubs: string;
}

const sources = import.meta.glob<string>("./presets/**/*", { query: "?raw", import: "default", eager: true });

// The test file's stubs, for evaluating with the same answers.
const splitStubs = `split:
  calls:
    - args: ["eu,us", ","]
      returns: [eu, us]
    - args: ["eu,us,ap", ","]
      returns: [eu, us, ap]
`;

export const presets: Preset[] = [
  {
    id: "alert-routing",
    label: "Alert routing",
    policy: "checkout.alerts",
    input: "checkout/testdata/latency.json",
    stubs: "",
  },
  {
    id: "deploy-approval",
    label: "Deploy approval, with a stubbed host function",
    policy: "payments.production",
    input: "payments/testdata/owner.json",
    stubs: splitStubs,
  },
  {
    id: "access-grants",
    label: "Access grants, collecting every decision",
    policy: "access.main",
    input: "access/testdata/team-member.json",
    stubs: "",
  },
];

/** A copy of a preset's workspace, safe to edit. */
export function presetWorkspace(id: string): Workspace {
  const preset = presets.find((p) => p.id === id) ?? presets[0];
  const files = presetFiles(preset.id);
  return {
    files,
    input: files.find((f) => f.path === preset.input)?.source ?? "",
    stubs: preset.stubs,
    policy: preset.policy,
    mode: "evaluate",
    run: "",
  };
}

// The files by path, as a directory lists them.
function presetFiles(id: string): SourceFile[] {
  const prefix = `./presets/${id}/`;
  return Object.entries(sources)
    .filter(([key]) => key.startsWith(prefix))
    .map(([key, source]) => ({ path: key.slice(prefix.length), source }))
    .sort((a, b) => (a.path < b.path ? -1 : 1));
}
