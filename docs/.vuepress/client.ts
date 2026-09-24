import { defineClientConfig } from "vuepress/client";
import VPContributorsCustom from "./components/VPContributorsCustom.vue";
import VPListCompare from "./components/VPListCompareCustom.vue";

export default defineClientConfig({
  enhance({ app }) {
    app.component("VPContributors", VPContributorsCustom);
    app.component("VPListCompare", VPListCompare);
  },
});
