<template>
  <dl v-if="entries.length" class="pg-payload pg-mono">
    <div v-for="[k, v] in entries" :key="k" class="pg-payload__field">
      <dt>{{ k }}</dt>
      <dd>{{ v }}</dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
// A decision's payload, one `field = value` per line. Explain gives the
// expressions as strings already; an evaluation gives values.
import { computed } from 'vue'
import type { JsonValue } from '@spechtlabs/sigil/worker'
import { literal } from './present.js'

const props = defineProps<{
  payload?: Record<string, JsonValue>
  /** Explain's `name = expression` strings, instead of values. */
  expressions?: string[]
}>()

const entries = computed<[string, string][]>(() => {
  if (props.expressions) {
    return props.expressions.map((e) => {
      const i = e.indexOf(' = ')
      return i < 0 ? [e, ''] : [e.slice(0, i), e.slice(i + 3)]
    })
  }
  return Object.entries(props.payload ?? {}).map(([k, v]) => [k, literal(v)])
})
</script>
