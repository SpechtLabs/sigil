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

  bundler: viteBundler(),
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
      // Getting Started: the tutorial path, from "what is this" to a first policy.
      "/getting-started/": [
        {
          text: "Getting Started",
          icon: "mdi:rocket-launch",
          prefix: "/getting-started/",
          items: [
            { text: "What Sigil is", link: "overview", icon: "mdi:eye" },
            { text: "A tour of the language", link: "tour", icon: "mdi:map-marker-path", badge: "5 min" },
            { text: "Your first policy", link: "first-policy", icon: "mdi:flash" },
          ],
        },
      ],

      // How-to Guides: task-oriented recipes for policy and kind authors.
      "/guides/": [
        {
          text: "How-to Guides",
          icon: "mdi:compass",
          prefix: "/guides/",
          items: [
            { text: "Per-team policies", link: "team-policies", icon: "mdi:account-group" },
            { text: "Common patterns", link: "patterns", icon: "mdi:puzzle" },
            { text: "Evolve a kind safely", link: "evolve-a-kind", icon: "mdi:source-branch" },
          ],
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
            { text: "Why rule order never matters", link: "order-independence", icon: "mdi:sort-variant-off" },
            { text: "Strict schema, forgiving data", link: "strictness", icon: "mdi:shield-check" },
            { text: "Halting by construction", link: "halting", icon: "mdi:timer-sand-complete" },
            { text: "Composition without templating", link: "composition", icon: "mdi:layers-triple" },
          ],
        },
      ],

      // Reference: the language specification, then the planned host API and tooling.
      "/reference/": [
        {
          text: "Language",
          icon: "mdi:book",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "Lexical structure", link: "lexical", icon: "mdi:format-letter-case" },
            { text: "Policy files", link: "policy-files", icon: "mdi:file-document-outline" },
            { text: "Expressions", link: "expressions", icon: "mdi:function-variant" },
            { text: "Types", link: "types", icon: "mdi:shape-outline" },
            { text: "Decisions", link: "decisions", icon: "mdi:directions-fork" },
            { text: "Evaluation semantics", link: "evaluation", icon: "mdi:cogs" },
            { text: "Kind files", link: "kind-files", icon: "mdi:file-certificate-outline" },
            { text: "Grammar", link: "grammar", icon: "mdi:code-braces" },
          ],
        },
        {
          text: "Host & tooling",
          icon: "mdi:tools",
          collapsed: false,
          prefix: "/reference/",
          items: [
            { text: "Go API", link: "go-api", icon: "mdi:language-go", badge: "planned" },
            { text: "CLI & editor tooling", link: "cli", icon: "mdi:console", badge: "planned" },
          ],
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
            { text: "Open questions", link: "open-questions", icon: "mdi:help-circle-outline" },
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
