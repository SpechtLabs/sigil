// The Sigil API against the real sigil.wasm (mise run wasm-build), with
// parity checks against the stock CLI's -o json records for the same files.

import { beforeAll, describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

import {
  type Diagnostic,
  type EvalResult,
  type JsonValue,
  type SourceFile,
  Sigil,
  SigilError,
  SigilStoppedError,
  type TestResult,
} from "../src/index.js";
import { Minimal } from "./kinds/coverage.js";
import { buildCli, cli, cliTest, DEPLOY_GATES, HAVE_WASM, json, ROOT, sigilFiles, type TestWorkspace, testWorkspace, WASM } from "./fixtures.js";

const CHECK_TESTDATA = join(ROOT, "cmd", "sigil", "command", "check", "testdata");
const TEST_TESTDATA = join(ROOT, "cmd", "sigil", "command", "test", "testdata");
const ALERT_ROUTING = join(ROOT, "examples", "alert-routing", "policies");
const KIND: SourceFile = {
  path: "deploy_approval.sigil",
  source: readFileSync(join(CHECK_TESTDATA, "deploy_approval.sigil"), "utf8"),
};

// The deploy-gates example's sigil.yaml, minus the kinds (they're among the
// files): its requirements and lint levels, for the module and the CLI.
const REQUIRE = [
  { policy: "deploy.guardrails", trusted: ["platform/deploy"], roots: ["payments.*", "checkout.*"] },
  { policy: "access.guardrails", trusted: ["platform/access"], roots: ["access.main"] },
];
const LINTS = { "gated-deny": "error", "gated-assert": "error", "path-matches-name": "error" } as const;
const CONFIG = `require:
  - policy: deploy.guardrails
    trusted: [platform/deploy]
    roots: ["payments.*", "checkout.*"]
  - policy: access.guardrails
    trusted: [platform/access]
    roots: [access.main]
lints:
  gated-deny: error
  gated-assert: error
  path-matches-name: error
`;

// split, as the deploy-gates host (examples/deploy-gates) implements it.
const split = (s: string, sep: string) => s.split(sep);

function inputs(dir: string): [string, Record<string, JsonValue>][] {
  return readdirSync(join(DEPLOY_GATES, dir))
    .filter((f) => f.endsWith(".json"))
    .sort()
    .map((f) => [`${dir}/${f}`, json(DEPLOY_GATES, `${dir}/${f}`)]);
}

/** The stubs that answer split for an input the way the real function does. */
function splitStub(input: Record<string, JsonValue>) {
  const regions = ((input["service"] as { labels: Record<string, string> }).labels["regions"] ?? "") as string;
  return { split: { calls: [{ args: [regions, ","], returns: regions.split(",") }] } };
}

describe.skipIf(!HAVE_WASM)("sigil.wasm", () => {
  let sigil: Sigil;
  const files = sigilFiles(DEPLOY_GATES);

  beforeAll(async () => {
    sigil = await Sigil.load(pathToFileURL(WASM));
    // Build the CLI up front: a cold go build takes longer than a test may.
    buildCli();
  }, 300_000);

  test("version reports a wasip1 build", () => {
    const v = sigil.version();
    expect(v.platform).toBe("wasip1/wasm");
    expect(v.goVersion).toStartWith("go1.");
    expect(typeof v.version).toBe("string");
    expect(typeof v.dirty).toBe("boolean");
  });

  describe("check", () => {
    test("the deploy-gates example passes, like the CLI with its sigil.yaml", () => {
      const diagnostics = sigil.check(files, { require: REQUIRE, lints: LINTS });
      expect(diagnostics).toEqual([]);
      expect(diagnostics).toEqual(cli(files, ["check"], { "sigil.yaml": CONFIG }) as never);
    });

    test("lint findings equal the CLI's", () => {
      const lintFiles = [KIND, ...sigilFiles(join(CHECK_TESTDATA, "lints"))];
      const diagnostics = sigil.check(lintFiles);
      expect(diagnostics.length).toBeGreaterThan(0);
      expect(diagnostics.every((d) => d.severity === "warning")).toBe(true);
      expect(diagnostics).toEqual(cli(lintFiles, ["check"]) as never);
    });

    test("a lint level raises a warning to an error", () => {
      const lintFiles = [KIND, ...sigilFiles(join(CHECK_TESTDATA, "lints"))];
      const diagnostics = sigil.check(lintFiles, { lints: { "unused-let": "error", "unused-import": "off" } });
      expect(diagnostics.filter((d) => d.lint === "unused-let").map((d) => d.severity)).toEqual(["error"]);
      expect(diagnostics.some((d) => d.lint === "unused-import")).toBe(false);
    });

    test("errors equal the CLI's, with positions in the virtual paths", () => {
      const bad = [KIND, { path: "teams/bad.sigil", source: readFileSync(join(CHECK_TESTDATA, "errors", "bad.sigil"), "utf8") }];
      const diagnostics = sigil.check(bad);
      expect(diagnostics.some((d) => d.severity === "error" && d.file === "teams/bad.sigil" && d.line !== undefined)).toBe(true);
      expect(diagnostics).toEqual(cli(bad, ["check"]) as never);
    });

    test("a requirement a policy breaks is an error diagnostic, like the CLI's", () => {
      const require = [{ policy: "deploy.production", trusted: ["platform/deploy"], roots: ["payments.*"] }];
      const diagnostics = sigil.check(files, { require });
      expect(diagnostics.some((d) => d.severity === "error" && d.file === "teams/payments/production.sigil")).toBe(true);
      const config = 'require:\n  - policy: deploy.production\n    trusted: [platform/deploy]\n    roots: ["payments.*"]\n';
      expect(diagnostics).toEqual(cli(files, ["check"], { "sigil.yaml": config }) as never);
    });

    test("a requirement that can't hold is a SigilError, as a configuration error is for the CLI", () => {
      const require = [{ policy: "deploy.guardrails", trusted: ["platform/access"], roots: ["payments.*"] }];
      expect(() => sigil.check(files, { require })).toThrow(
        "require[0]: deploy.guardrails must come from platform/access, but it's defined at platform/deploy/guardrails.sigil",
      );
    });

    test("policies narrows the check", () => {
      const bad = [...files, { path: "teams/bad/production.sigil", source: "policy bad.production: DeployApproval@1\n\nwhen nope {\n  approve(reason: payments_sre)\n}\n" }];
      expect(sigil.check(bad).some((d) => d.severity === "error")).toBe(true);
      expect(sigil.check(bad, { policies: ["payments.*"] })).toEqual([]);
    });
  });

  describe("compile", () => {
    test("returns a policy with its name and warnings", () => {
      using policy = sigil.compile(files, { policy: "payments.production", functions: { split } });
      expect(policy.name).toBe("payments.production");
      expect(policy.handle).toBeNumber();
      expect(policy.diagnostics).toEqual([]);
    });

    test("fails with the diagnostics of files that don't compile", () => {
      const bad = [KIND, { path: "teams/bad.sigil", source: readFileSync(join(CHECK_TESTDATA, "errors", "bad.sigil"), "utf8") }];
      const err = (() => {
        try {
          sigil.compile(bad);
        } catch (e) {
          return e;
        }
      })() as SigilError;
      expect(err).toBeInstanceOf(SigilError);
      expect(err.message).not.toBe("");
      expect(err.diagnostics.length).toBeGreaterThan(0);
      expect(err.diagnostics[0]?.file).toBe("teams/bad.sigil");
    });

    test("fails for a policy the files don't hold", () => {
      expect(() => sigil.compile(files, { policy: "nope.production" })).toThrow(SigilError);
    });

    test("fails for a host function the kind doesn't declare", () => {
      const err = (() => {
        try {
          sigil.compile(files, { policy: "payments.production", functions: { splt: split } });
        } catch (e) {
          return e;
        }
      })() as SigilError;
      expect(err).toBeInstanceOf(SigilError);
      expect(err.message).toContain("has no host function splt");
      expect(err.help).toContain("split");
    });
  });

  describe("eval", () => {
    for (const [path, input] of inputs("teams/payments/testdata")) {
      test(`payments.production on ${path} equals the CLI with stubs`, () => {
        const stubs = splitStub(input);
        using policy = sigil.compile(files, { policy: "payments.production", stubs });
        const result = policy.eval(input);
        const expected = cli(files, ["eval", "--policy", "payments.production", "--input", path, "--stubs", "stubs.json"], {
          [path]: JSON.stringify(input),
          "stubs.json": JSON.stringify(stubs),
        });
        expect(result).toEqual(expected as EvalResult);
      });
    }

    for (const [path, input] of inputs("access/testdata")) {
      test(`access.main on ${path} equals the CLI`, () => {
        using policy = sigil.compile(files, { policy: "access.main" });
        const expected = cli(files, ["eval", "--policy", "access.main", "--input", path], { [path]: JSON.stringify(input) });
        expect(policy.eval(input)).toEqual(expected as EvalResult);
      });
    }

    test("a failing assert returns the fallback with the failure", () => {
      using policy = sigil.compile(files, { policy: "access.main" });
      const result = policy.eval(json(DEPLOY_GATES, "access/testdata/auditor-sre.json"));
      expect(result.error?.kind).toBe("assertion");
      expect(result.error?.asserts?.length).toBeGreaterThan(0);
      expect(result.error?.phase).toBe("outcome");
      expect(result.error?.asserts?.[0]?.policy).toBe("access.guardrails");
    });

    test("host functions implemented in JS give the stubs' answers", () => {
      using real = sigil.compile(files, { policy: "payments.production", functions: { split } });
      for (const [, input] of inputs("teams/payments/testdata")) {
        using stubbed = sigil.compile(files, { policy: "payments.production", stubs: splitStub(input) });
        expect(real.eval(input)).toEqual(stubbed.eval(input));
      }
    });

    test("a host function receives the Sigil arguments", () => {
      const calls: unknown[][] = [];
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: {
          split: (...args: [string, string]) => {
            calls.push(args);
            return split(...args);
          },
        },
      });
      policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json"));
      expect(calls).toContainEqual(["eu,us", ","]);
    });

    test("a throwing host function fails the evaluation at runtime", () => {
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: {
          split: () => {
            throw new Error("region directory unavailable");
          },
        },
      });
      const result = policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json"));
      expect(result.error?.kind).toBe("runtime");
      expect(result.error?.message).toContain("host function split failed: region directory unavailable");
      // The failure's fallback is the kind's default.
      expect(result.decision).toBe("deny");
      expect(result.reason).toBe("no_rule_matched");
    });

    test("a stub error fails the call like the CLI's", () => {
      const stubs = { split: { error: "region directory unavailable" } };
      using policy = sigil.compile(files, { policy: "payments.production", stubs });
      const path = "teams/payments/testdata/sre.json";
      const input = json(DEPLOY_GATES, path);
      const expected = cli(files, ["eval", "--policy", "payments.production", "--input", path, "--stubs", "stubs.json"], {
        [path]: JSON.stringify(input),
        "stubs.json": JSON.stringify(stubs),
      });
      expect(policy.eval(input)).toEqual(expected as EvalResult);
    });

    test("a stub replaces a JS implementation of the same name", () => {
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: {
          split: () => {
            throw new Error("the JS function ran");
          },
        },
        stubs: { split: { returns: ["eu", "us"] } },
      });
      expect(policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json")).error).toBeUndefined();
    });

    test("a host function with neither an implementation nor a stub fails like the CLI", () => {
      using policy = sigil.compile(files, { policy: "payments.production" });
      const path = "teams/payments/testdata/sre.json";
      const input = json(DEPLOY_GATES, path);
      const result = policy.eval(input);
      expect(result.error?.kind).toBe("runtime");
      const expected = cli(files, ["eval", "--policy", "payments.production", "--input", path], { [path]: JSON.stringify(input) });
      expect(result).toEqual(expected as EvalResult);
    });

    test("a host function that returns a promise fails the call", () => {
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: { split: async (s: string, sep: string) => s.split(sep) },
      });
      const result = policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json"));
      expect(result.error?.message).toContain("returned a promise");
    });

    test("a host function can't call back into the instance", () => {
      using inner = sigil.compile(files, { policy: "access.main" });
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: {
          split: (s: string, sep: string) => {
            inner.eval(json(DEPLOY_GATES, "access/testdata/sre.json"));
            return s.split(sep);
          },
        },
      });
      const result = policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json"));
      expect(result.error?.message).toContain("busy");
      // Both work afterwards.
      expect(inner.eval(json(DEPLOY_GATES, "access/testdata/sre.json")).error).toBeUndefined();
    });

    test("an input that doesn't fit the kind is a SigilError", () => {
      using policy = sigil.compile(files, { policy: "payments.production", functions: { split } });
      expect(() => policy.eval({ release: { soak: "a while" } })).toThrow(SigilError);
    });

    test("timeoutMs cancels a slow evaluation", () => {
      using policy = sigil.compile(files, {
        policy: "payments.production",
        functions: {
          split: (s: string, sep: string) => {
            const end = performance.now() + 100;
            while (performance.now() < end) {
              // a slow host
            }
            return s.split(sep);
          },
        },
      });
      const result = policy.eval(json(DEPLOY_GATES, "teams/payments/testdata/sre.json"), { timeoutMs: 20 });
      expect(result.error?.kind).toBe("canceled");
      expect(result.error?.message).toBe("the evaluation was stopped: context deadline exceeded");
      expect(result.trace).toEqual([]);
      expect([result.decision, result.reason]).toEqual(["deny", "no_rule_matched"]);
    });
  });

  describe("explain", () => {
    test("a policy's explanation equals the CLI's", () => {
      using policy = sigil.compile(files, { policy: "payments.production", functions: { split } });
      const [expected] = cli(files, ["explain", "--policy", "payments.production"]) as unknown[];
      expect(policy.explain()).toEqual(expected as never);
    });

    test("explaining files equals the CLI's, for one policy or all", () => {
      expect(sigil.explain(files, { policy: "access.main" })).toEqual(cli(files, ["explain", "--policy", "access.main"]) as never);
      expect(sigil.explain(files)).toEqual(cli(files, ["explain"]) as never);
    });
  });

  describe("format", () => {
    test("leaves the canonical example sources unchanged", () => {
      for (const f of files) expect(sigil.format(f.source, { path: f.path })).toBe(f.source);
    });

    test("formats a messy source", () => {
      const messy = "policy   a.b :DeployApproval@1\nwhen   true {approve(reason:payments_sre)}\n";
      const formatted = sigil.format(messy);
      expect(formatted).not.toBe(messy);
      expect(sigil.format(formatted)).toBe(formatted);
    });

    test("a source that doesn't parse is a SigilError with its diagnostics", () => {
      const err = (() => {
        try {
          sigil.format("policy x {", { path: "x.sigil" });
        } catch (e) {
          return e;
        }
      })() as SigilError;
      expect(err).toBeInstanceOf(SigilError);
      expect(err.diagnostics.length).toBeGreaterThan(0);
      expect(err.diagnostics[0]?.file).toBe("x.sigil");
    });
  });

  describe("test", () => {
    // The CLI's test fixtures: the access kind and policy, and a directory
    // of test files per situation.
    const fixtures = testWorkspace(TEST_TESTDATA);
    const pick = (...prefixes: string[]): TestWorkspace => {
      const keep = (f: SourceFile) => prefixes.some((p) => f.path === p || f.path.startsWith(`${p}/`));
      return { files: fixtures.files.filter(keep), trusted: [], tests: fixtures.tests.filter(keep), data: fixtures.data.filter(keep) };
    };
    const run = (ws: TestWorkspace, filter?: string) =>
      sigil.test(ws.files, ws.tests, {
        data: ws.data,
        ...(ws.trusted.length > 0 ? { trustedFiles: ws.trusted } : {}),
        ...(filter === undefined ? {} : { run: filter }),
      });

    const parity: [string, TestWorkspace, string?][] = [
      ["the deploy-gates example, with its input files and a host function no stub answers", testWorkspace(DEPLOY_GATES)],
      ["the alert-routing example", testWorkspace(ALERT_ROUTING)],
      ["the alert-routing example, with the platform's policies trusted", testWorkspace(ALERT_ROUTING, ["platform"])],
      ["no files", { ...pick("access"), files: [] }],
      ["passing cases", pick("access.sigil", "access")],
      ["failing cases", pick("access.sigil", "access/main.sigil", "failing")],
      ["the cases run selects", pick("access.sigil", "access/main.sigil", "failing"), "^wrong"],
      ["no case run selects", pick("access.sigil", "access/main.sigil", "failing"), "nothing"],
      ["an invalid test file", pick("access.sigil", "access/main.sigil", "invalid")],
      ["a test file that isn't YAML", pick("access.sigil", "access/main.sigil", "badyaml")],
      ["a policy the files don't define", pick("access.sigil", "access/main.sigil", "nopolicy")],
      ["a policy that doesn't compile", pick("access.sigil", "broken")],
      ["file and case stubs", pick("access.sigil", "access/main.sigil", "stubbed")],
      ["stubs that fail", pick("access.sigil", "access/main.sigil", "stubfail")],
      ["stubs that don't fit", pick("access.sigil", "access/main.sigil", "badstubs")],
    ];
    for (const [name, ws, filter] of parity) {
      test(`${name} equal the CLI's results`, () => {
        const results = run(ws, filter);
        expect(results.length).toBe(ws.tests.length);
        expect(results).toEqual(cliTest(ws, filter) as TestResult[]);
      });
    }

    test("an input file the request doesn't hold fails its case, like a missing file for the CLI", () => {
      const ws = { ...pick("access.sigil", "access"), data: [] };
      const results = run(ws);
      const missing = results[0]?.cases.find((c) => c.error !== undefined);
      expect(missing?.passed).toBe(false);
      expect(missing?.error).toContain("couldn't be read (input_file is relative to the test file)");
      expect(results).toEqual(cliTest(ws) as TestResult[]);
    });

    test("reports passing, failing and broken test files apart", () => {
      const ws = pick("access.sigil", "access", "failing", "broken");
      const results = run(ws);
      expect(results.map((r) => r.file)).toEqual(["access/main_test.yaml", "broken/main_test.yaml", "failing/main_test.yaml"]);
      const [pass, broken, failing] = results;
      expect(pass?.cases.every((c) => c.passed)).toBe(true);
      expect(broken?.error).toBeString();
      expect(broken?.cases).toEqual([]);
      expect(failing?.error).toBeUndefined();
      expect(failing?.cases.some((c) => !c.passed && (c.failures?.length ?? 0) > 0)).toBe(true);
    });

    const files = pick("access.sigil", "access/main.sigil").files;
    const suite = fixtures.tests.find((f) => f.path === "access/main_test.yaml") as SourceFile;
    const failures: [string, () => TestResult[], string][] = [
      ["no test files", () => sigil.test(files, []), "the request holds no test files"],
      ["a test file not named like one", () => sigil.test(files, [{ path: "access/main.yaml", source: suite.source }]), "the test file access/main.yaml isn't named like one"],
      ["a test file without a path", () => sigil.test(files, [{ path: "", source: suite.source }]), "test file 1 has no path"],
      ["a run that isn't a regular expression", () => sigil.test(files, [suite], { run: "(" }), "run isn't a valid regular expression"],
      ["a path given twice with two sources", () => sigil.test(files, [suite], { data: [{ path: suite.path, source: "{}" }] }), "access/main_test.yaml is given twice, with two sources"],
      [
        "a path among the files and the trusted files",
        () => sigil.test(files, [suite], { trustedFiles: files }),
        "is among both the files and the trusted files",
      ],
    ];
    for (const [name, call, message] of failures) {
      test(`${name} is a SigilError`, () => {
        const err = (() => {
          try {
            call();
          } catch (e) {
            return e;
          }
        })() as SigilError;
        expect(err).toBeInstanceOf(SigilError);
        expect(err.message).toContain(message);
        expect(err.help).toBeString();
      });
    }
  });

  describe("release", () => {
    test("a released policy can't be evaluated, and releasing twice does nothing", () => {
      const policy = sigil.compile(files, { policy: "access.main" });
      policy.release();
      policy.release();
      expect(policy.handle).toBeUndefined();
      expect(() => policy.eval({})).toThrow("policy access.main was released");
      expect(() => policy.explain()).toThrow(SigilError);
    });

    test("using releases at the end of the block", () => {
      let escaped: { handle: number | undefined } | undefined;
      {
        using policy = sigil.compile(files, { policy: "access.main" });
        escaped = policy;
        expect(policy.handle).toBeNumber();
      }
      expect(escaped?.handle).toBeUndefined();
    });
  });
});

describe.skipIf(!HAVE_WASM)("sigil.wasm memory", () => {
  test("stays flat across 10k evaluations with host function calls", async () => {
    let memory: WebAssembly.Memory | undefined;
    const original = WebAssembly.instantiate;
    WebAssembly.instantiate = (async (m: WebAssembly.Module, i: WebAssembly.Imports) => {
      const instance = await original(m, i);
      memory = instance.exports["memory"] as WebAssembly.Memory;
      return instance;
    }) as typeof WebAssembly.instantiate;
    let sigil: Sigil;
    try {
      sigil = await Sigil.load(pathToFileURL(WASM));
    } finally {
      WebAssembly.instantiate = original;
    }
    const files = sigilFiles(DEPLOY_GATES);
    const input = json(DEPLOY_GATES, "teams/payments/testdata/sre.json");
    const run = (n: number) => {
      for (let i = 0; i < n; i++) {
        using policy = sigil.compile(files, { policy: "payments.production", functions: { split } });
        for (let j = 0; j < 100; j++) policy.eval(input);
      }
    };
    run(10); // warm up: the Go heap settles at its working size
    const before = memory?.buffer.byteLength ?? 0;
    run(100);
    const after = memory?.buffer.byteLength ?? 0;
    expect(before).toBeGreaterThan(0);
    // Allow the Go heap some slack, not growth per evaluation.
    expect(after - before).toBeLessThanOrEqual(4 * 1024 * 1024);
  }, 120_000);
});

// A policy nested 100k deep ran the module out of stack before the parser
// limited nesting; with the limit it's a diagnostic. Either way the
// instance must end up usable or clearly stopped, never half-alive.
const DEEP = 100_000;
const deepPolicy = (n: number) => `policy deep.nesting: Minimal@1\n\nwhen ${"(".repeat(n)}true${")".repeat(n)} {\n  ok(reason: yes)\n}\n`;

describe.skipIf(!HAVE_WASM)("sigil.wasm on a deeply nested policy", () => {
  test("either rejects it with a diagnostic or stops the instance for good", async () => {
    const sigil = await Sigil.load(pathToFileURL(WASM), { output: () => {} });
    const files = [Minimal.file(), { path: "deep.sigil", source: deepPolicy(DEEP) }];
    let diagnostics: Diagnostic[] | undefined;
    let err: unknown;
    try {
      diagnostics = sigil.check(files);
    } catch (e) {
      err = e;
    }
    if (err instanceof SigilStoppedError) {
      expect(err.message).toStartWith("the Sigil module stopped: ");
      expect(sigil.stopped).toBe(err);
      expect(() => sigil.version()).toThrow(err);
      expect(() => sigil.compile(files)).toThrow(err);
    } else {
      expect(err).toBeUndefined();
      expect(diagnostics?.some((d) => d.severity === "error")).toBe(true);
      expect(sigil.stopped).toBeUndefined();
      expect(sigil.version().platform).toBe("wasip1/wasm");
    }
  });
});
