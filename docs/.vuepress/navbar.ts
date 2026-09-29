import { defineNavbarConfig } from "vuepress-theme-plume";

export const navbar = defineNavbarConfig([
  { text: "Home", link: "/", icon: "mdi:home" },

  {
    text: "Getting Started",
    icon: "mdi:rocket-launch",
    items: [
      { text: "What Sigil is", link: "/getting-started/overview", icon: "mdi:eye" },
      { text: "A tour of the language", link: "/getting-started/tour", icon: "mdi:map-marker-path" },
      { text: "Your first policy", link: "/getting-started/first-policy", icon: "mdi:flash" },
    ],
  },

  {
    text: "Guides",
    icon: "mdi:compass",
    items: [
      {
        text: "Writing policies",
        items: [
          { text: "Per-team policies", link: "/guides/team-policies", icon: "mdi:account-group" },
          { text: "Common patterns", link: "/guides/patterns", icon: "mdi:puzzle" },
          { text: "Test your policies", link: "/guides/test-policies", icon: "mdi:test-tube" },
          { text: "Check policies in CI", link: "/guides/ci", icon: "mdi:check-decagram-outline" },
        ],
      },
      {
        text: "Embedding in Go",
        items: [
          { text: "Embed Sigil in a Go service", link: "/guides/embed-go", icon: "mdi:language-go" },
          { text: "Handle failed evaluations", link: "/guides/handle-errors", icon: "mdi:alert-circle-outline" },
          { text: "Policies in a ConfigMap", link: "/guides/configmaps", icon: "mdi:kubernetes" },
          { text: "Build a host binary", link: "/guides/host-binary", icon: "mdi:console" },
          { text: "Evolve a kind safely", link: "/guides/evolve-a-kind", icon: "mdi:source-branch" },
          { text: "The example service", link: "/guides/example-service", icon: "mdi:rocket-launch-outline" },
        ],
      },
    ],
  },

  {
    text: "Understanding",
    icon: "mdi:lightbulb",
    items: [
      { text: "Design goals", link: "/understanding/design-goals", icon: "mdi:compass-rose" },
      { text: "Prior art", link: "/understanding/prior-art", icon: "mdi:bookshelf" },
      { text: "Why the language looks like this", link: "/understanding/language-choices", icon: "mdi:format-quote-open" },
      { text: "Kinds as contracts", link: "/understanding/kinds", icon: "mdi:file-certificate-outline" },
      { text: "Decisions and reasons", link: "/understanding/decisions", icon: "mdi:directions-fork" },
      { text: "Why rule order never matters", link: "/understanding/order-independence", icon: "mdi:sort-variant-off" },
      { text: "Asserts and decisions", link: "/understanding/asserts", icon: "mdi:alert-octagon-outline" },
      { text: "Strict schema, forgiving data", link: "/understanding/strictness", icon: "mdi:shield-check" },
      { text: "Halting by construction", link: "/understanding/halting", icon: "mdi:timer-sand-complete" },
      { text: "Composition without templating", link: "/understanding/composition", icon: "mdi:layers-triple" },
      { text: "Bundles and trust", link: "/understanding/bundles", icon: "mdi:package-variant-closed" },
    ],
  },

  {
    text: "Reference",
    icon: "mdi:book",
    items: [
      {
        text: "Language",
        items: [
          { text: "Lexical structure", link: "/reference/lexical", icon: "mdi:format-letter-case" },
          { text: "Policy files", link: "/reference/policy-files", icon: "mdi:file-document-outline" },
          { text: "Bundles", link: "/reference/bundles", icon: "mdi:package-variant-closed" },
          { text: "Expressions", link: "/reference/expressions", icon: "mdi:function-variant" },
          { text: "Types", link: "/reference/types", icon: "mdi:shape-outline" },
          { text: "Decisions", link: "/reference/decisions", icon: "mdi:directions-fork" },
          { text: "Evaluation semantics", link: "/reference/evaluation", icon: "mdi:cogs" },
          { text: "Kind files", link: "/reference/kind-files", icon: "mdi:file-certificate-outline" },
          { text: "Grammar", link: "/reference/grammar", icon: "mdi:code-braces" },
        ],
      },
      {
        text: "Tooling",
        items: [
          { text: "CLI", link: "/reference/cli", icon: "mdi:console" },
          { text: "Test files", link: "/reference/test-files", icon: "mdi:test-tube" },
          { text: "Lints", link: "/reference/lints", icon: "mdi:alert-outline" },
        ],
      },
      {
        text: "Go host",
        items: [
          { text: "Go API", link: "/reference/go-api", icon: "mdi:language-go" },
          { text: "Performance", link: "/reference/performance", icon: "mdi:speedometer" },
        ],
      },
    ],
  },

  {
    text: "Project",
    icon: "mdi:dots-horizontal",
    items: [
      { text: "Roadmap", link: "/project/roadmap", icon: "mdi:timeline-outline" },
      { text: "Planned designs", link: "/project/planned", icon: "mdi:pencil-ruler" },
      { text: "Open questions", link: "/project/open-questions", icon: "mdi:help-circle-outline" },
      { text: "Contributing", link: "/project/contributing", icon: "mdi:source-pull" },
      {
        text: "Discuss the design",
        link: "https://github.com/SpechtLabs/sigil/issues",
        target: "_blank",
        rel: "noopener noreferrer",
        icon: "mdi:forum-outline",
      },
    ],
  },
]);
