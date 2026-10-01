<template>
  <div class="pg-page">
    <header class="pg-page__header">
      <h1 class="pg-page__title">{{ title }}</h1>
      <div class="pg-page__tagline"><slot /></div>
    </header>
    <ClientOnly>
      <Workbench />
    </ClientOnly>
  </div>
</template>

<script setup lang="ts">
// The playground page: its title, the one-line tagline the page passes in
// (also what search engines and readers without JavaScript get), and the
// workbench, which fills the rest of the viewport on wide screens.
// Everything behind the workbench (CodeMirror, the worker, sigil.wasm) loads
// in the browser, on this page only: the server render has nothing to run,
// and other pages never fetch it.
import { defineAsyncComponent, h } from 'vue'
import { usePageData } from 'vuepress/client'

const title = usePageData().value.title

const Workbench = defineAsyncComponent({
  loader: () => import('./playground/Workbench.vue'),
  loadingComponent: { render: () => h('div', { class: 'pg-placeholder' }, 'Loading the playground') },
  delay: 0,
})
</script>

<style>
/* The page is one screen tall below the navbar, and the workbench takes what
   the header leaves; its panes scroll inside. Below 700px of height it stops
   shrinking and the page scrolls instead. */
.pg-page {
  display: flex;
  flex-direction: column;
  max-width: 1600px;
  height: max(700px, calc(100dvh - var(--vp-nav-height, 64px)));
  margin: 0 auto;
  padding: 0.9rem 24px 1.25rem;
}

.pg-page__header {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 0.2rem 1rem;
  margin-bottom: 0.8rem;
}

.vp-doc .pg-page__title {
  margin: 0;
  padding: 0;
  border: 0;
  font-size: 1.35rem;
  line-height: 1.3;
}

.pg-page__tagline {
  flex: 1 1 20rem;
  min-width: 0;
  color: var(--vp-c-text-2);
  font-size: 0.875rem;
}

.vp-doc .pg-page__tagline p {
  margin: 0;
  line-height: 1.5;
}

.pg-placeholder {
  display: grid;
  flex: 1;
  place-items: center;
  min-height: 560px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  background: var(--vp-c-bg-soft);
  color: var(--vp-c-text-3);
  font-size: 0.9rem;
}

@media (min-width: 768px) {
  .pg-page {
    padding: 1rem 32px 1.25rem;
  }
}

/* Phones and narrow windows: the panes stack and the page scrolls. */
@media (max-width: 960px) {
  .pg-page {
    height: auto;
  }
}
</style>
