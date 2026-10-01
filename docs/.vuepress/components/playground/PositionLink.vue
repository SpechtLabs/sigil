<template>
  <span v-if="position" class="pg-positions">
    <template v-for="(p, i) in parts" :key="i">
      <span v-if="i > 0" class="pg-dim" aria-hidden="true"> → </span>
      <button type="button" class="pg-position pg-mono" :title="`Show ${p} in the editor`" @click="emit('reveal', p)">{{ p }}</button>
    </template>
  </span>
</template>

<script setup lang="ts">
// A `file:line:column` or `policy:line` that jumps to its place in the
// files. A failed assert's position lists the invocations that reached it
// first, joined with arrows; each is a link of its own.
import { computed } from 'vue'

const props = defineProps<{ position?: string }>()

const emit = defineEmits<{ reveal: [position: string] }>()

const parts = computed(() => props.position?.split(' → ') ?? [])
</script>
