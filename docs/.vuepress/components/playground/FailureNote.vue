<template>
  <div class="pg-failure">
    <div class="pg-failure__title">{{ failure.title }}</div>
    <div class="pg-failure__message">{{ failure.message }}</div>
    <div v-if="failure.help" class="pg-help">{{ failure.help }}</div>
    <button v-if="failure.pane" type="button" class="pg-link" @click="emit('show', failure)">Show it in the {{ failure.pane }}</button>
    <span v-else-if="failure.diagnostics?.length" class="pg-dim">The problems list shows where.</span>
  </div>
</template>

<script setup lang="ts">
// A run that produced no result: what went wrong, how to fix it, and where.
import type { Failure } from './present.js'

defineProps<{ failure: Failure }>()

const emit = defineEmits<{ show: [failure: Failure] }>()
</script>
