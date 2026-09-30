"use client";

import type { SigilWorker, WorkerPolicy } from "@spechtlabs/sigil/worker";
import { useCallback, useEffect, useRef, useState } from "react";
import type { PolicyFilesResponse, RouteResponse, Team } from "@/lib/ui/api-types";
import { request } from "@/lib/ui/client";
import {
  type PreviewBundle,
  previewBundle,
  type RoutingAlert,
  routeResponseFromEval,
  routingInput,
  unownedResponse,
} from "@/lib/ui/preview";

// Where the browser loads the engine from: scripts/copy-wasm.ts copies the
// package's worker helper, its worker entry and sigil.wasm to public/sigil/.
const WORKER_MODULE = "/sigil/worker.js";
const WASM_URL = "/sigil/sigil.wasm";

// Evaluations of AlertRouting take microseconds; a preview that takes
// longer than this is stuck, and the worker is replaced.
const EVAL_TIMEOUT_MS = 2_000;

export type PreviewState =
  | { status: "loading" }
  | { status: "ready"; fingerprint: string }
  | { status: "unavailable"; reason: string };

export interface Preview {
  state: PreviewState;
  /**
   * Evaluates the alert against the team's policy in the browser, or
   * answers the kind's default when no team owns it. Throws when the
   * preview is unavailable or the policy doesn't compile in the browser.
   */
  evaluate: (alert: RoutingAlert, team: Team | undefined) => Promise<RouteResponse>;
}

/**
 * The in-browser preview: sigil.wasm in a Web Worker, compiling the same
 * files the server serves (fetched from /api/v1/policies/files), so the
 * page can show a decision and its trace before the server answers. It
 * recompiles when the served fingerprint changes. Anything that goes wrong,
 * from a missing sigil.wasm to a browser without workers, leaves the
 * preview "unavailable"; it never takes the form down with it.
 */
export function usePreview(servedFingerprint: string | undefined): Preview {
  const [state, setState] = useState<PreviewState>({ status: "loading" });
  const worker = useRef<SigilWorker | undefined>(undefined);
  const sources = useRef<PreviewBundle | undefined>(undefined);
  const compiled = useRef(new Map<string, Promise<WorkerPolicy>>());

  const releaseAll = useCallback(() => {
    for (const p of compiled.current.values()) {
      p.then(
        (policy) => policy.release(),
        () => {},
      ).catch(() => {});
    }
    compiled.current.clear();
  }, []);

  const loadSources = useCallback(async () => {
    const next = previewBundle(await request<PolicyFilesResponse>("/api/v1/policies/files"));
    releaseAll();
    sources.current = next;
    return next;
  }, [releaseAll]);

  // Start the worker once per page.
  useEffect(() => {
    let cancelled = false;
    let started: SigilWorker | undefined;
    (async () => {
      try {
        if (typeof Worker === "undefined" || typeof WebAssembly === "undefined") {
          throw new Error("this browser has no Web Workers or WebAssembly");
        }
        // Loaded from the public copy at run time rather than bundled (see
        // scripts/copy-wasm.ts), so the server never evaluates it and a
        // missing file only costs the preview.
        const { SigilWorker } = (await import(
          /* webpackIgnore: true */ /* turbopackIgnore: true */ WORKER_MODULE
        )) as typeof import("@spechtlabs/sigil/worker");
        started = new SigilWorker({ wasm: WASM_URL, timeoutMs: 10_000, startTimeoutMs: 30_000 });
        // version() loads the module, so a missing or broken sigil.wasm shows here.
        await started.version();
        const loaded = await loadSources();
        if (cancelled) return;
        worker.current = started;
        setState({ status: "ready", fingerprint: loaded.fingerprint });
      } catch (err) {
        started?.terminate();
        if (!cancelled) setState({ status: "unavailable", reason: reasonOf(err) });
      }
    })();
    return () => {
      cancelled = true;
      releaseAll();
      started?.terminate();
      worker.current = undefined;
    };
  }, [loadSources, releaseAll]);

  // Follow reloads: fetch the files again when the server serves a new bundle.
  useEffect(() => {
    if (state.status !== "ready" || servedFingerprint === undefined || servedFingerprint === state.fingerprint) return;
    let cancelled = false;
    loadSources().then(
      (loaded) => {
        if (!cancelled) setState({ status: "ready", fingerprint: loaded.fingerprint });
      },
      (err: unknown) => {
        if (!cancelled) setState({ status: "unavailable", reason: reasonOf(err) });
      },
    );
    return () => {
      cancelled = true;
    };
  }, [servedFingerprint, state, loadSources]);

  const evaluate = useCallback(async (alert: RoutingAlert, team: Team | undefined) => {
    const w = worker.current;
    const files = sources.current;
    if (w === undefined || files === undefined) throw new Error("the preview isn't available");
    if (team === undefined) return unownedResponse();
    const entry = files.policies.find((p) => p.team === team.name);
    if (entry === undefined) {
      throw new Error(`the served bundle has no policy for team ${team.name}`);
    }
    let policy = compiled.current.get(entry.policy);
    if (policy === undefined) {
      policy = w.compile(files.files, {
        policy: entry.policy,
        require: [{ policy: files.required }],
        trustedFiles: files.trusted,
      });
      compiled.current.set(entry.policy, policy);
      // A policy that doesn't compile isn't cached, so a retry compiles again.
      policy.catch(() => compiled.current.delete(entry.policy));
    }
    const res = await (await policy).eval(routingInput(alert, team), { timeoutMs: EVAL_TIMEOUT_MS });
    return routeResponseFromEval(team.name, res);
  }, []);

  return { state, evaluate };
}

function reasonOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
