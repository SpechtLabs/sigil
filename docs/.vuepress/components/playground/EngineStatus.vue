<template>
  <div class="pg-engine">
    <div
      v-if="!failed && downloading"
      role="progressbar"
      aria-label="Downloading the Sigil engine"
      aria-valuemin="0"
      aria-valuemax="100"
      :aria-valuenow="Math.round(fraction * 100)"
    >
      <Seal class="pg-engine__seal" hue="brand" :progress="fraction" />
    </div>
    <Seal v-else class="pg-engine__seal" :hue="failed ? 'red' : 'brand'" :progress="1" />
    <div class="pg-engine__text" role="status">
      <template v-if="failed">
        <div class="pg-engine__title">The engine didn't load</div>
        <div class="pg-dim">{{ failed }}</div>
        <button type="button" class="pg-btn" @click="emit('retry')">Try again</button>
      </template>
      <template v-else-if="downloading">
        <div class="pg-engine__title">Loading the Sigil engine</div>
        <div v-if="progress" class="pg-dim pg-mono" aria-hidden="true">{{ megabytes(progress.loaded) }} of {{ megabytes(progress.total) }} MB</div>
      </template>
      <template v-else>
        <div class="pg-engine__title">Starting the engine</div>
        <div class="pg-dim">It runs in your browser; nothing you type leaves the page.</div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
// The engine's loading state: the seal draws its edge as sigil.wasm
// downloads. The progress is a progressbar's value, and the status line
// changes only between phases, so a screen reader isn't read every chunk.
import { computed } from 'vue'
import Seal from './Seal.vue'
import type { Progress } from './engine.js'
import { megabytes } from './present.js'

const props = defineProps<{ progress?: Progress; failed?: string }>()

const emit = defineEmits<{ retry: [] }>()

const downloading = computed(() => props.progress === undefined || props.progress.loaded < props.progress.total)
const fraction = computed(() => (props.progress ? props.progress.loaded / props.progress.total : 0))
</script>
