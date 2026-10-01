<template>
  <div class="pg-filebar">
    <form v-if="renaming" class="pg-filebar__rename" @submit.prevent="commit">
      <input
        ref="input"
        v-model="draft"
        :aria-label="`New path for ${path}`"
        :aria-invalid="problem !== undefined"
        :aria-describedby="problem ? 'pg-rename-problem' : undefined"
        spellcheck="false"
        autocomplete="off"
        @keydown.esc.prevent="cancel"
        @blur="commit"
      />
    </form>
    <div v-else class="pg-filebar__path pg-mono" :title="path" @dblclick="start">
      <span class="pg-dim">{{ dirOf(path) }}</span>{{ path.slice(dirOf(path).length) }}
    </div>
    <div class="pg-filebar__actions">
      <button v-if="formattable" type="button" class="pg-filebar__action" :disabled="!canFormat" title="Format this file as sigil fmt does" @click="emit('format')">Format</button>
      <button type="button" class="pg-filebar__action pg-filebar__action--icon" :aria-label="`Rename ${path}`" title="Rename" @click="start">
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M10.6 2.6a1.4 1.4 0 0 1 2 2L5.5 11.7l-2.8.8.8-2.8z" /></svg>
      </button>
      <button
        type="button"
        class="pg-filebar__action pg-filebar__action--icon"
        :aria-label="`Delete ${path}`"
        title="Delete"
        :disabled="!deletable"
        @click="emit('remove')"
      >
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 4.5h9M6.5 4.5V3h3v1.5M5 4.5l.6 8.5h4.8l.6-8.5" /></svg>
      </button>
    </div>
    <p v-if="problem" id="pg-rename-problem" class="pg-filebar__problem" role="alert">{{ problem }}</p>
  </div>
</template>

<script setup lang="ts">
// The open file's path, with Format for Sigil files, and renaming and
// deleting it. Renaming happens in place: Enter keeps the new path, Escape
// the old one.
import { nextTick, ref } from 'vue'
import type { SourceFile } from '@spechtlabs/sigil/worker'
import { dirOf, pathProblem } from './workspace.js'

const props = defineProps<{
  path: string
  files: SourceFile[]
  formattable: boolean
  canFormat: boolean
  deletable: boolean
}>()

const emit = defineEmits<{
  format: []
  rename: [to: string]
  remove: []
}>()

const input = ref<HTMLInputElement>()
const renaming = ref(false)
const draft = ref('')
const problem = ref<string>()

async function start(): Promise<void> {
  renaming.value = true
  draft.value = props.path
  problem.value = undefined
  await nextTick()
  input.value?.focus()
  // Select the name without its extension, the part one usually changes.
  const name = props.path.slice(dirOf(props.path).length)
  const ext = /(_test\.ya?ml|\.[a-z]+)$/.exec(name)?.[0] ?? ''
  input.value?.setSelectionRange(dirOf(props.path).length, props.path.length - ext.length)
}

function commit(): void {
  if (!renaming.value) return
  const to = draft.value.trim()
  problem.value = pathProblem(to, props.files, props.path)
  if (problem.value !== undefined) return
  renaming.value = false
  if (to !== props.path) emit('rename', to)
}

function cancel(): void {
  renaming.value = false
  problem.value = undefined
}

defineExpose({ start })
</script>
