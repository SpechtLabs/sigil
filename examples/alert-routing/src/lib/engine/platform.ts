// The platform engine: platform.paging, compiled from the platform's own
// documents into a Sigil instance of its own, in this process. No team file
// ever reaches this instance, so a team bundle that breaks an engine can't
// break the page every critical alert is owed. It answers one question for
// every firing alert a team owns: does the platform page for it, and whom?
//
// When the instance fails (the module stopped, or a call ran out of stack in
// Go code), the engine reports itself down, which /readyz answers with 503,
// and loads a fresh instance from the compiled module. After three failed
// attempts in a row it gives up and calls onFatal, which exits the process:
// a router that can't compute the platform's page must not look healthy.

import type { Policy, Sigil, SourceFile } from "@spechtlabs/sigil";

import { HumaneError, humane, messageOf, wrap } from "../errors";
import { AlertRouting, type Input, Page } from "../routing/kind";
import { hasStopped, isEngineFailure } from "../sigil";
import type { Telemetry } from "../telemetry/types";

/** The policy the platform engine evaluates. */
export const PAGING_POLICY = "platform.paging";

/** An alert every version of platform.paging pages for: critical, and not in pre-production. */
const CANARY: Input = {
  alert: { name: "AlertrouterReadinessProbe", severity: "critical", labels: {}, firing_for: "0s" },
  team: { name: "alertrouter", oncall: "alertrouter-readiness-probe", channel: "#alertrouter" },
};

/** How often a replacement is tried before the engine gives up. */
const RECOVERY_ATTEMPTS = 3;

/** The pause before each retry, doubled every time. */
const RECOVERY_BACKOFF_MS = 100;

/** What platform.paging says about one alert. */
export type PlatformVerdict =
  /** It pages target for reason. */
  | { kind: "page"; reason: string; target: string }
  /** It doesn't page. */
  | { kind: "none" }
  /** It couldn't be evaluated; the alert's page can't be vouched for. */
  | { kind: "unavailable"; error: HumaneError };

export interface PlatformEngineOptions {
  /** The platform's trusted documents. */
  platform: readonly SourceFile[];
  telemetry: Telemetry;
  /** Loads a fresh instance; the tests pass one they can break. */
  load: () => Promise<Sigil>;
  /** How long one evaluation may take. */
  timeoutMs: number;
  /** Called when no replacement instance loads; the process should exit. */
  onFatal: (err: HumaneError) => void;
}

export class PlatformEngine {
  readonly #opts: PlatformEngineOptions;
  #sigil: Sigil | undefined;
  #paging: Policy<Input> | undefined;
  #recovering: Promise<void> | undefined;
  #closed = false;

  constructor(opts: PlatformEngineOptions) {
    this.#opts = opts;
  }

  /** Loads the first instance. Throws when platform.paging doesn't compile. */
  async start(): Promise<void> {
    await this.#replace();
    this.#opts.telemetry.metrics.setEngineUp("platform", true);
  }

  /** Whether the engine can evaluate: started, and not being replaced. */
  get up(): boolean {
    const sigil = this.#sigil;
    if (sigil !== undefined && hasStopped(sigil)) this.#failed(new Error("the platform engine's module stopped"));
    return this.#paging !== undefined && this.#recovering === undefined && !this.#closed;
  }

  /**
   * Whether the engine answers: up, and a canary alert that platform.paging
   * pages for gets its page. /readyz asks this, so an instance that broke
   * without anyone noticing yet is found and replaced by the probe.
   */
  probe(): boolean {
    if (!this.up) return false;
    return this.page(CANARY).kind === "page" && this.up;
  }

  /**
   * Resolves once a replacement is in, or at once when none is under way.
   * The tests wait on it; requests never do.
   */
  async recovered(): Promise<void> {
    await this.#recovering;
  }

  /** platform.paging's verdict for input, with its params at the platform's defaults. */
  page(input: Input): PlatformVerdict {
    const paging = this.#paging;
    if (!this.up || paging === undefined) {
      return {
        kind: "unavailable",
        error: humane(
          `${PAGING_POLICY} can't be evaluated while the platform engine is being replaced`,
          "alertrouter replaces a failed engine by itself within a second; the retry of this request is answered normally",
        ),
      };
    }
    try {
      const res = paging.eval(input, { timeoutMs: this.#opts.timeoutMs });
      if (res.error !== undefined) {
        return {
          kind: "unavailable",
          error: humane(
            `${PAGING_POLICY} failed for this alert: ${res.error.message}`,
            "the platform's documents are bundled with alertrouter; report the alert that makes them fail",
          ),
        };
      }
      const page = Page.match(res);
      return page === undefined ? { kind: "none" } : { kind: "page", reason: res.reason ?? "", target: page.target };
    } catch (err) {
      if (isEngineFailure(err)) this.#failed(err);
      return {
        kind: "unavailable",
        error: wrap(
          err,
          `${PAGING_POLICY} couldn't be evaluated: ${messageOf(err)}`,
          "alertrouter replaces a failed engine by itself; the retry of this request is answered normally",
        ),
      };
    }
  }

  /** Releases the instance. The engine answers unavailable afterwards. */
  close(): void {
    this.#closed = true;
    this.#paging?.release();
    this.#paging = undefined;
    this.#sigil = undefined;
  }

  /** Marks the instance failed and starts replacing it, once. */
  #failed(err: unknown): void {
    if (this.#recovering !== undefined || this.#closed) return;
    const { telemetry } = this.#opts;
    telemetry.metrics.setEngineUp("platform", false);
    telemetry.metrics.observeEngineRestart("platform");
    telemetry.logger.error("the platform engine failed; loading a new one", { error: messageOf(err) });
    this.#paging = undefined;
    this.#sigil = undefined;
    this.#recovering = this.#recover().finally(() => {
      this.#recovering = undefined;
    });
  }

  async #recover(): Promise<void> {
    const { telemetry } = this.#opts;
    let last: unknown;
    for (let attempt = 0; attempt < RECOVERY_ATTEMPTS && !this.#closed; attempt++) {
      if (attempt > 0) await new Promise((resolve) => setTimeout(resolve, RECOVERY_BACKOFF_MS * 2 ** (attempt - 1)));
      try {
        await this.#replace();
        telemetry.metrics.setEngineUp("platform", true);
        telemetry.logger.info("the platform engine is running again", { attempts: attempt + 1 });
        return;
      } catch (err) {
        last = err;
      }
    }
    if (this.#closed) return;
    this.#opts.onFatal(
      wrap(
        last,
        `the platform engine couldn't be replaced after ${RECOVERY_ATTEMPTS} attempts: ${messageOf(last)}`,
        "alertrouter exits so that its supervisor restarts it; check the logs above for why the engine failed",
      ),
    );
  }

  async #replace(): Promise<void> {
    const sigil = await this.#opts.load();
    try {
      const paging = AlertRouting.compile(sigil, [...this.#opts.platform], { policy: PAGING_POLICY });
      if (this.#closed) {
        paging.release();
        return;
      }
      this.#sigil = sigil;
      this.#paging = paging;
    } catch (err) {
      throw err instanceof HumaneError
        ? err
        : wrap(
            err,
            `the platform's ${PAGING_POLICY} doesn't compile on its own: ${messageOf(err)}`,
            "this is a bug in the documents bundled with alertrouter; run `bun run generate` and `mise run policies`",
          );
    }
  }
}
