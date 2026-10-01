<template>
  <svg :class="['pg-seal', `pg-hue-${hue}`, { 'pg-seal--forming': progress !== undefined }]" viewBox="0 0 64 64" aria-hidden="true">
    <path class="pg-seal__edge" :d="EDGE" pathLength="100" :style="dash" />
    <circle class="pg-seal__ring" cx="32" cy="32" r="21.5" />
    <text v-if="glyph" class="pg-seal__glyph" x="32" y="33" text-anchor="middle" dominant-baseline="middle">{{ glyph }}</text>
  </svg>
</template>

<script setup lang="ts">
// The seal an outcome is stamped with: a scalloped disc in the decision's
// colour. While the engine loads, its edge draws itself as the download
// progresses.
import { computed } from 'vue'
import type { Hue } from './present.js'

const props = defineProps<{
  hue: Hue
  glyph?: string
  /** 0 to 1 while loading: only the edge shows, drawn this far. */
  progress?: number
}>()

const EDGE = scallops(18, 29.5, 26.5)

const dash = computed(() =>
  props.progress === undefined ? undefined : { strokeDasharray: '100', strokeDashoffset: String(100 - props.progress * 100) },
)

// A closed path of n round lobes between radii inner and outer.
function scallops(n: number, outer: number, inner: number): string {
  const at = (r: number, a: number) => `${(32 + r * Math.cos(a)).toFixed(2)} ${(32 + r * Math.sin(a)).toFixed(2)}`
  const step = (2 * Math.PI) / n
  let d = `M ${at(inner, -Math.PI / 2)}`
  for (let i = 0; i < n; i++) {
    const a = -Math.PI / 2 + i * step
    d += ` Q ${at(outer * 1.06, a + step / 2)} ${at(inner, a + step)}`
  }
  return `${d} Z`
}
</script>
