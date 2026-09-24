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
      { text: "Per-team policies", link: "/guides/team-policies", icon: "mdi:account-group" },
      { text: "Common patterns", link: "/guides/patterns", icon: "mdi:puzzle" },
      { text: "Evolve a kind safely", link: "/guides/evolve-a-kind", icon: "mdi:source-branch" },
    ],
  },

  {
    text: "Understanding",
    icon: "mdi:lightbulb",
    items: [
      { text: "Design goals", link: "/understanding/design-goals", icon: "mdi:compass-rose" },
      { text: "Prior art", link: "/understanding/prior-art", icon: "mdi:bookshelf" },
      { text: "Why rule order never matters", link: "/understanding/order-independence", icon: "mdi:sort-variant-off" },
      { text: "Strict schema, forgiving data", link: "/understanding/strictness", icon: "mdi:shield-check" },
      { text: "Halting by construction", link: "/understanding/halting", icon: "mdi:timer-sand-complete" },
      { text: "Composition without templating", link: "/understanding/composition", icon: "mdi:layers-triple" },
    ],
  },

  {
    text: "Reference",
    icon: "mdi:book",
    items: [
      { text: "Lexical structure", link: "/reference/lexical", icon: "mdi:format-letter-case" },
      { text: "Policy files", link: "/reference/policy-files", icon: "mdi:file-document-outline" },
      { text: "Expressions", link: "/reference/expressions", icon: "mdi:function-variant" },
      { text: "Types", link: "/reference/types", icon: "mdi:shape-outline" },
      { text: "Decisions", link: "/reference/decisions", icon: "mdi:directions-fork" },
      { text: "Evaluation semantics", link: "/reference/evaluation", icon: "mdi:cogs" },
      { text: "Kind files", link: "/reference/kind-files", icon: "mdi:file-certificate-outline" },
      { text: "Grammar", link: "/reference/grammar", icon: "mdi:code-braces" },
      { text: "Go API", link: "/reference/go-api", icon: "mdi:language-go" },
      { text: "CLI & editor tooling", link: "/reference/cli", icon: "mdi:console" },
    ],
  },

  {
    text: "Project",
    icon: "mdi:dots-horizontal",
    items: [
      { text: "Roadmap", link: "/project/roadmap", icon: "mdi:timeline-outline" },
      { text: "Open questions", link: "/project/open-questions", icon: "mdi:help-circle-outline" },
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
