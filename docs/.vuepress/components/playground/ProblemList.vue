<template>
  <div class="pg-problems">
    <div class="pg-problems__head" :id="headId">
      <span>Problems</span>
      <span v-if="!ready" class="pg-dim">checked once the engine has loaded</span>
      <span v-else-if="diagnostics.length === 0" class="pg-problems__ok">none</span>
      <template v-else>
        <span v-if="errors" class="pg-problems__count pg-problems__count--error">{{ errors }} error{{ errors === 1 ? '' : 's' }}</span>
        <span v-if="warnings" class="pg-problems__count">{{ warnings }} warning{{ warnings === 1 ? '' : 's' }}</span>
      </template>
    </div>
    <ol v-if="ready && diagnostics.length" class="pg-problems__list" :aria-labelledby="headId">
      <li v-for="(d, i) in diagnostics" :key="i">
        <button type="button" :class="['pg-problem', `pg-problem--${d.severity}`]" :disabled="!d.file" @click="emit('reveal', d)">
          <span class="pg-problem__where pg-mono">{{ where(d) }}</span>
          <span class="pg-problem__message">{{ d.message }}<span v-if="d.lint" class="pg-dim"> ({{ d.lint }})</span></span>
          <span v-if="d.help" class="pg-help">{{ d.help }}</span>
        </button>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
// What check finds in the files as they're edited, errors and lints alike.
// Each problem jumps to its place.
import { computed } from 'vue'
import type { Diagnostic } from '@spechtlabs/sigil/worker'

const props = defineProps<{ diagnostics: Diagnostic[]; ready: boolean }>()

const emit = defineEmits<{ reveal: [d: Diagnostic] }>()

const headId = 'pg-problems-head'
const errors = computed(() => props.diagnostics.filter((d) => d.severity === 'error').length)
const warnings = computed(() => props.diagnostics.length - errors.value)

function where(d: Diagnostic): string {
  if (!d.file) return d.severity
  return d.line === undefined ? d.file : `${d.file}:${d.line}${d.column === undefined ? '' : `:${d.column}`}`
}
</script>
