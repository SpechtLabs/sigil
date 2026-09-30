// compile's require with trusted files: the platform's guardrails come
// from the host's own documents, and a team bundle can't omit, gate,
// redefine or loosen them. The deploy-gates example's payments policy
// stands in for a team bundle, platform/deploy for the platform.

import { beforeAll, describe, expect, test } from "bun:test";
import { pathToFileURL } from "node:url";

import { type CompileRequirement, type InputOf, Sigil, SigilError, type SourceFile } from "../src/index.js";
import { SigilWorker } from "../src/worker.js";
import { DEPLOY_GATES, HAVE_WASM, json, sigilFiles, WASM } from "./fixtures.js";
import { Approve, DeployApproval } from "./kinds/examples.js";

const all = sigilFiles(DEPLOY_GATES);
const platform = all.filter((f) => f.path.startsWith("platform/deploy/"));
const payments = all.find((f) => f.path === "teams/payments/production.sigil") as SourceFile;
const require: CompileRequirement[] = [{ policy: "deploy.guardrails" }];
const input = json(DEPLOY_GATES, "teams/payments/testdata/sre.json") as InputOf<typeof DeployApproval>;

/** The payments bundle with its source rewritten. */
function team(rewrite: (source: string) => string, extra: SourceFile[] = []): SourceFile[] {
  return [{ ...payments, source: rewrite(payments.source) }, ...extra];
}

function compileError(fn: () => unknown): SigilError {
  try {
    fn();
  } catch (err) {
    expect(err).toBeInstanceOf(SigilError);
    return err as SigilError;
  }
  throw new Error("the compile succeeded");
}

describe.skipIf(!HAVE_WASM)("compile with require and trusted files", () => {
  let sigil: Sigil;

  beforeAll(async () => {
    sigil = await Sigil.load(pathToFileURL(WASM));
  });

  // null compiles without trusted files.
  const compile = (files: SourceFile[], trustedFiles: SourceFile[] | null = platform) =>
    DeployApproval.compile(sigil, files, { policy: "payments.production", require, ...(trustedFiles === null ? {} : { trustedFiles }) });

  test("a bundle that invokes the trusted guardrails compiles and evaluates as before", () => {
    using policy = compile(team((s) => s));
    using plain = DeployApproval.compile(sigil, all, { policy: "payments.production" });
    expect(Approve.reason("payments_sre").is(policy.eval(input))).toBe(true);
    expect(policy.eval(input)).toEqual(plain.eval(input));
  });

  test.each([
    [
      "omits the guardrails",
      team((s) => s.replace("use deploy.guardrails\n", "").replace("guardrails(min_soak: 4h)\n", "")),
      "the policy doesn't compile, so nothing was compiled",
      "payments.production doesn't invoke deploy.guardrails",
    ],
    [
      "gates the guardrails",
      team((s) => s.replace("guardrails(min_soak: 4h)\n", 'when environment == "production" {\n  guardrails(min_soak: 4h)\n}\n')),
      "the policy doesn't compile, so nothing was compiled",
      "deploy.guardrails must be invoked unconditionally",
    ],
    [
      "passes a param below its minimum",
      team((s) => s.replace("guardrails(min_soak: 4h)", "guardrails(min_soak: 1m)")),
      "the policy doesn't compile, so nothing was compiled",
      "min_soak: 1m is below the minimum 1h",
    ],
    [
      "redefines the guardrails",
      team((s) => s, [{ path: "teams/payments/guardrails.sigil", source: "policy deploy.guardrails: DeployApproval@1\n" }]),
      "doesn't check, so nothing was compiled",
      "policy deploy.guardrails is defined twice",
    ],
  ])("a bundle that %s doesn't compile", (_case, files, message, diagnostic) => {
    const err = compileError(() => compile(files));
    expect(err.message).toContain(message);
    expect(err.diagnostics.map((d) => d.message)).toContain(diagnostic);
    expect(err.diagnostics.find((d) => d.message === diagnostic)?.file).toStartWith("teams/payments/");
  });

  test("the required policy must come from the trusted files", () => {
    const guardrails = platform.filter((f) => f.path.endsWith("guardrails.sigil"));
    const rest = platform.filter((f) => !f.path.endsWith("guardrails.sigil"));
    const err = compileError(() => compile(team((s) => s, guardrails), rest));
    expect(err.message).toBe("require[0]: deploy.guardrails isn't among the trusted files");
    expect(err.diagnostics.map((d) => d.message)).toContain("deploy.guardrails must come from the trusted files, but it's defined here");
  });

  test("a required policy nobody defines fails", () => {
    const rest = platform.filter((f) => !f.path.endsWith("guardrails.sigil"));
    const err = compileError(() => compile(team((s) => s), rest));
    expect(err.message).toBe("require[0]: deploy.guardrails is required, but the trusted files define no policy deploy.guardrails");
  });

  test("a path is in the files or the trusted files, not both", () => {
    const err = compileError(() => compile([...team((s) => s), ...platform]));
    expect(err.message).toMatch(/^platform\/deploy\/\w+\.sigil is among both the files and the trusted files$/);
  });

  test("without trusted files, any policy of the bundle satisfies a requirement", () => {
    using policy = compile([...team((s) => s), ...platform], null);
    expect(policy.name).toBe("payments.production");
    const err = compileError(() => compile([...team((s) => s.replace("use deploy.guardrails\n", "").replace("guardrails(min_soak: 4h)\n", "")), ...platform], null));
    expect(err.diagnostics.map((d) => d.message)).toContain("payments.production doesn't invoke deploy.guardrails");
  });

  test("the kind file may be among the trusted files", () => {
    using policy = DeployApproval.compile(sigil, team((s) => s), {
      policy: "payments.production",
      require,
      trustedFiles: [DeployApproval.file(), ...platform],
    });
    expect(Approve.match(policy.eval(input))).toEqual({ bake: "15m" });
  });

  test("a stale kind file among the trusted files fails", () => {
    const stale = { ...DeployApproval.file(), source: DeployApproval.schema().replace("= 1h", "= 2h") };
    expect(() => compile(team((s) => s), [stale, ...platform])).toThrow("declares kind DeployApproval, but not as this program defines it");
  });

  test("compile's requirements take only a policy", () => {
    const err = compileError(() =>
      sigil.compile([DeployApproval.file(), ...team((s) => s)], {
        policy: "payments.production",
        require: [{ policy: "deploy.guardrails", roots: ["payments.*"] } as CompileRequirement],
        trustedFiles: platform,
      }),
    );
    expect(err.message).toBe("require[0]: compile's requirements take only a policy");
  });

  test("check and explain read trusted files too", () => {
    const files = [DeployApproval.file(), ...team((s) => s)];
    expect(sigil.check(files, { trustedFiles: platform })).toEqual([]);
    expect(sigil.explain(files, { policy: "payments.production", trustedFiles: platform })).toEqual(
      sigil.explain([DeployApproval.file(), ...team((s) => s), ...platform], { policy: "payments.production" }),
    );
  });

  test("the worker helper passes require and trusted files through", async () => {
    const worker = new SigilWorker({
      wasm: pathToFileURL(WASM),
      functions: new URL("./host-functions.ts", import.meta.url),
      worker: () => new Worker(new URL("../src/worker-entry.ts", import.meta.url), { type: "module" }),
    });
    try {
      const policy = await DeployApproval.compile(worker, team((s) => s), {
        policy: "payments.production",
        require,
        trustedFiles: platform,
        functions: ["split"],
      });
      expect(Approve.match(await policy.eval(input))).toEqual({ bake: "15m" });
      const err = await DeployApproval.compile(worker, team((s) => s.replace("guardrails(min_soak: 4h)", "guardrails(min_soak: 1m)")), {
        policy: "payments.production",
        require,
        trustedFiles: platform,
      }).catch((e) => e);
      expect(err).toBeInstanceOf(SigilError);
      expect(err.diagnostics.map((d: { message: string }) => d.message)).toContain("min_soak: 1m is below the minimum 1h");
    } finally {
      worker.terminate();
    }
  });
});
