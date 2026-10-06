import { statSync } from "node:fs";
import { createRequire } from "node:module";
import { viteBundler } from "@vuepress/bundler-vite";
import { registerComponentsPlugin } from "@vuepress/plugin-register-components";
import { path } from "@vuepress/utils";
import container from "markdown-it-container";
import { defineUserConfig } from "vuepress";
import { plumeTheme } from "vuepress-theme-plume";
import sigilGrammar from "./sigil.tmLanguage.json" with { type: "json" };

export default defineUserConfig({
  base: "/",
  lang: "en-US",
  title: "Sigil",
  description:
    "A small, statically typed policy language for Go hosts. Rules evaluate typed input to a typed decision, and every decision carries a reason.",

  head: [
    [
      "meta",
      {
        name: "description",
        content:
          "Sigil is a policy language embedded in Go applications. Engineers write rules that evaluate host-provided input to a typed decision such as approve, deny or review. Policies are type-checked against a host contract, always halt, and compose through typed parameters instead of text templating.",
      },
    ],
    ["link", { rel: "icon", type: "image/png", href: "/images/specht.png" }],
  ],

  bundler: viteBundler({
    viteOptions: {
      // The playground runs @spechtlabs/sigil in a worker. Pre-bundling the
      // package would move its files away from sigil.wasm and its worker
      // entry, so the dev server serves it as published.
      optimizeDeps: { exclude: ["@spechtlabs/sigil"] },
      // The worker entry imports modules on demand, which only an ES module
      // worker can do.
      worker: { format: "es" },
      define: {
        // The playground shows the engine's download progress against this.
        __SIGIL_WASM_SIZE__: JSON.stringify(
          statSync(createRequire(import.meta.url).resolve("@spechtlabs/sigil/sigil.wasm")).size,
        ),
      },
    },
  }),
  shouldPrefetch: false,

  extendsMarkdown: (md) => {
    md.use(container, "terminal", {
      validate: (params: string) => {
        const info = params.trim();
        return /^terminal(?:\s+.*)?$/.test(info);
      },
      render: (tokens: any[], idx: number) => {
        const token = tokens[idx];
        if (token.nesting === 1) {
          const info = token.info.trim();
          const rest = info.replace(/^terminal\s*/, "");
          const attrs: Record<string, string> = {};
          const attrRegex = /(\w+)=((?:\"[^\"]*\")|(?:'[^']*')|(?:[^\s]+))/g;
          let consumed = "";
          let m: RegExpExecArray | null;
          while ((m = attrRegex.exec(rest)) !== null) {
            const key = m[1];
            let val = m[2];
            if (
              (val.startsWith('"') && val.endsWith('"')) ||
              (val.startsWith("'") && val.endsWith("'"))
            ) {
              val = val.slice(1, -1);
            }
            attrs[key] = val;
            consumed += m[0] + " ";
          }
          const positional = rest.replace(consumed, "").trim();
          const titleRaw = attrs.title ?? positional ?? "";
          const title = titleRaw ? md.utils.escapeHtml(titleRaw) : "";
          const titleAttr = title ? ` title=\"${title}\"` : "";
          return `\n<Terminal${titleAttr}>\n`;
        }
        return `\n</Terminal>\n`;
      },
    });
  },

  plugins: [
    registerComponentsPlugin({
      componentsDir: path.resolve(__dirname, "./components"),
      // Top-level components only: the playground's pieces under
      // components/playground/ are imported where they're used.
      componentsPatterns: ["*.vue"],
    }),
  ],

  theme: plumeTheme({
    docsRepo: "https://github.com/SpechtLabs/sigil",
    docsDir: "docs",
    docsBranch: "main",

    editLink: true,
    lastUpdated: false,
    contributors: false,

    cache: "filesystem",
    search: { provider: "local" },

    // Sigil has no upstream grammar, so the docs ship their own TextMate
    // grammar and every ```sigil fence (policy and kind files alike) uses it.
    codeHighlighter: {
      langs: [sigilGrammar as any],
    },

    sidebar: {
      // Getting Started: the tutorial path, from "what is this" to guardrails.
      "/getting-started/": [
        {
          text: "Getting Started",
          icon: "mdi:rocket-launch",
          prefix: "/getting-started/",
          items: [
            { text: "What Sigil is", link: "overview", icon: "mdi:eye" },
            { text: "A tour of the language", link: "tour", icon: "mdi:map-marker-path", badge: "5 min" },
          ],
        },
        {
          // Steps 1-4: everything a service needs when one team owns its policies.
          text: "Step by step",
          icon: "mdi:stairs",
          collapsed: false,
          prefix: "/getting-started/",
          items: [
            { text: "1. Define the input", link: "define-the-input", icon: "mdi:language-go" },
            { text: "2. Write policies", link: "write-policies", icon: "mdi:file-document-edit-outline" },
            { text: "3. Export the kind", link: "export-the-kind", icon: "mdi:file-export-outline" },
            { text: "4. Check, evaluate and test", link: "check-and-test", icon: "mdi:check-decagram-outline" },
          ],
        },
        {
          // Steps 5-7: for several teams writing policies against one kind.
          text: "Sharing across teams",
          icon: "mdi:account-group",
          collapsed: false,
          prefix: "/getting-started/",
          items: [
            { text: "5. Share rules across teams", link: "share-rules", icon: "mdi:library-shelves" },
            { text: "6. See what a policy adds up to", link: "explain", icon: "mdi:file-tree-outline" },
            { text: "7. Require guardrails", link: "require-guardrails", icon: "mdi:shield-lock-outline" },
          ],
        },
      ],

      // How-to Guides: task-oriented recipes for policy authors and Go hosts.
      "/guides/": [
        {
          text: "Writing policies",
          icon: "mdi:file-document-edit-outline",
          collapsed: false,
          prefix: "/guides/",
          items: [
            { text: "Per-team policies", link: "team-policies", icon: "mdi:account-group" },
            { text: "Common patterns", link: "patterns", icon: "mdi:puzzle" },
            { text: "Test your policies", link: "test-policies", icon: "mdi:test-tube" },
            { text: "Check policies in CI", link: "ci", icon: "mdi:check-decagram-outline" },
            { text: "Ship policies as a binary", link: "compile", icon: "mdi:package-variant-closed-check" },
          ],
        },
        {
          text: "Embedding in Go",
          icon: "mdi:language-go",
          collapsed: false,
          prefix: "/guides/",
          items: [
            { text: "Embed Sigil in a Go service", link: "embed-go", icon: "mdi:language-go" },
            { text: "Handle failed evaluations", link: "handle-errors", icon: "mdi:alert-circle-outline" },
            { text: "Policies in a ConfigMap", link: "configmaps", icon: "mdi:kubernetes" },
            { text: "Build a host binary", link: "host-binary", icon: "mdi:console" },
            { text: "Build policies in Go", link: "build-policies-in-go", icon: "mdi:code-braces-box" },
            { text: "Evolve a kind safely", link: "evolve-a-kind", icon: "mdi:source-branch" },
            { text: "Use a kind from another Go service", link: "generate-go", icon: "mdi:file-code-outline" },
            { text: "The example service", link: "example-service", icon: "mdi:rocket-launch-outline" },
          ],
        },
        {
          text: "Embedding in TypeScript",
          icon: "mdi:language-typescript",
          collapsed: false,
          prefix: "/guides/",
          items: [
            { text: "Embed Sigil in TypeScript", link: "embed-typescript", icon: "mdi:language-typescript" },
          ],
        },
        {
          text: "Embedding in Rust",
          icon: "simple-icons:rust",
          collapsed: false,
          prefix: "/guides/",
          items: [{ text: "Embed Sigil in Rust", link: "embed-rust", icon: "simple-icons:rust" }],
        },
      ],

      // Understanding: why the language is shaped the way it is.
      "/understanding/": [
        {
          text: "Understanding Sigil",
          icon: "mdi:lightbulb",
          collapsed: false,
          prefix: "/understanding/",
          items: [
            { text: "Design goals", link: "design-goals", icon: "mdi:compass-rose" },
            { text: "Prior art", link: "prior-art", icon: "mdi:bookshelf" },
            { text: "Why the language looks like this", link: "language-choices", icon: "mdi:format-quote-open" },
            { text: "Kinds as contracts", link: "kinds", icon: "mdi:file-certificate-outline" },
            { text: "Decisions and reasons", link: "decisions", icon: "mdi:directions-fork" },
            { text: "Why rule order never matters", link: "order-independence", icon: "mdi:sort-variant-off" },
            { text: "Asserts and decisions", link: "asserts", icon: "mdi:alert-octagon-outline" },
            { text: "Strict schema, forgiving data", link: "strictness", icon: "mdi:shield-check" },
            { text: "Halting by construction", link: "halting", icon: "mdi:timer-sand-complete" },
            { text: "Composition without templating", link: "composition", icon: "mdi:layers-triple" },
            { text: "Bundles and trust", link: "bundles", icon: "mdi:package-variant-closed" },
            { text: "Facts, vocabulary and rules", link: "facts-vocabulary-rules", icon: "mdi:layers-outline" },
            { text: "One engine for every host", link: "one-engine", icon: "mdi:cube-outline" },
            { text: "How compile works", link: "compile", icon: "mdi:package-variant-closed-check" },
          ],
        },
      ],

      // Reference: the language specification, host API and tooling.
      "/reference/": [
        {
          text: "Language",
          icon: "mdi:book",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "Lexical structure", link: "lexical", icon: "mdi:format-letter-case" },
            { text: "Kind files", link: "kind-files", icon: "mdi:file-certificate-outline" },
            { text: "Policy files", link: "policy-files", icon: "mdi:file-document-outline" },
            { text: "Bundles", link: "bundles", icon: "mdi:package-variant-closed" },
            { text: "Expressions", link: "expressions", icon: "mdi:function-variant" },
            { text: "Types", link: "types", icon: "mdi:shape-outline" },
            { text: "Decisions", link: "decisions", icon: "mdi:directions-fork" },
            { text: "Evaluation semantics", link: "evaluation", icon: "mdi:cogs" },
            { text: "Grammar", link: "grammar", icon: "mdi:code-braces" },
          ],
        },
        {
          text: "Tooling",
          icon: "mdi:tools",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "CLI", link: "cli", icon: "mdi:console" },
            { text: "Test files", link: "test-files", icon: "mdi:test-tube" },
            { text: "Configuration file", link: "config", icon: "mdi:file-cog-outline" },
            { text: "Lints", link: "lints", icon: "mdi:alert-outline" },
          ],
        },
        {
          text: "Go host",
          icon: "mdi:language-go",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "Go API", link: "go-api", icon: "mdi:language-go" },
            { text: "Go builder", link: "go-builder", icon: "mdi:hammer-wrench" },
            { text: "Performance", link: "performance", icon: "mdi:speedometer" },
          ],
        },
        {
          text: "Other hosts",
          icon: "mdi:cube-outline",
          collapsed: false,
          prefix: "/reference/",
          items: [{ text: "WebAssembly module", link: "wasm", icon: "mdi:cube-outline" }],
        },
      ],

      // Project: where the design stands and what is still undecided.
      "/project/": [
        {
          text: "Project",
          icon: "mdi:clipboard-text-outline",
          collapsed: false,
          prefix: "/project/",
          items: [
            { text: "Roadmap", link: "roadmap", icon: "mdi:timeline-outline" },
            { text: "Planned designs", link: "planned", icon: "mdi:pencil-ruler" },
            { text: "Open questions", link: "open-questions", icon: "mdi:help-circle-outline" },
            { text: "Contributing", link: "contributing", icon: "mdi:source-pull" },
          ],
        },
      ],
    },

    markdown: {
      collapse: true,
      timeline: true,
      mermaid: true,
      image: {
        figure: true,
        lazyload: true,
        mark: true,
        size: true,
      },
    },

    watermark: false,
  }),
});
