<template>
  <div class="pg-tabs">
    <div ref="list" class="pg-tabs__list" role="tablist" aria-label="Files" @keydown="onKeydown">
      <div v-for="f in files" :key="f.path" :class="['pg-tab', { 'pg-tab--active': f.path === active }]">
        <form v-if="renaming === f.path" class="pg-tab__rename" @submit.prevent="commitRename(f.path)">
          <input
            ref="renameInput"
            v-model="draft"
            :aria-label="`New path for ${f.path}`"
            :aria-invalid="problem !== undefined"
            :aria-describedby="problem ? 'pg-rename-problem' : undefined"
            spellcheck="false"
            autocomplete="off"
            @keydown.esc.prevent="cancelRename"
            @blur="commitRename(f.path)"
          />
        </form>
        <button
          v-else
          type="button"
          role="tab"
          class="pg-tab__name"
          :aria-selected="f.path === active"
          :tabindex="f.path === active ? 0 : -1"
          :title="f.path === active ? 'Double-click or press F2 to rename' : f.path"
          @click="emit('select', f.path)"
          @dblclick="startRename(f.path)"
          @keydown.f2.prevent="startRename(f.path)"
        >
          <span v-if="marks[f.path]?.fired" class="pg-tab__fired" title="A rule here fired in the last run" />
          <span class="pg-tab__dir">{{ dir(f.path) }}</span>{{ base(f.path) }}
          <span v-if="marks[f.path]?.errors" class="pg-tab__count pg-tab__count--error">{{ marks[f.path].errors }}</span>
          <span v-else-if="marks[f.path]?.warnings" class="pg-tab__count">{{ marks[f.path].warnings }}</span>
        </button>
        <template v-if="f.path === active && renaming !== f.path">
          <button type="button" class="pg-tab__action" :aria-label="`Rename ${f.path}`" title="Rename" @click="startRename(f.path)">
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M10.6 2.6a1.4 1.4 0 0 1 2 2L5.5 11.7l-2.8.8.8-2.8z" /></svg>
          </button>
          <button
            type="button"
            class="pg-tab__action"
            :aria-label="`Delete ${f.path}`"
            title="Delete"
            :disabled="files.length === 1"
            @click="emit('remove', f.path)"
          >
            <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" /></svg>
          </button>
        </template>
      </div>
    </div>
    <button type="button" class="pg-tabs__add" aria-label="New file" title="New file" @click="emit('add')">
      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 3v10M3 8h10" /></svg>
    </button>
    <p v-if="problem" id="pg-rename-problem" class="pg-tabs__problem" role="alert">{{ problem }}</p>
  </div>
</template>

<script setup lang="ts">
// The file tabs: pick a file, add one, rename it in place, delete it.
import { nextTick, ref } from 'vue'
import type { SourceFile } from '@spechtlabs/sigil/worker'
import { pathProblem } from './workspace.js'

const props = defineProps<{
  files: SourceFile[]
  active: string
  /** Per path: problems from check, and whether a rule there fired. */
  marks: Record<string, { errors: number; warnings: number; fired: boolean }>
}>()

const emit = defineEmits<{
  select: [path: string]
  add: []
  rename: [from: string, to: string]
  remove: [path: string]
}>()

const list = ref<HTMLElement>()
const renameInput = ref<HTMLInputElement[]>()
const renaming = ref<string>()
const draft = ref('')
const problem = ref<string>()

async function startRename(path: string): Promise<void> {
  renaming.value = path
  draft.value = path
  problem.value = undefined
  await nextTick()
  const input = renameInput.value?.[0]
  input?.focus()
  // Select the name without its extension, the part one usually changes.
  input?.setSelectionRange(path.lastIndexOf('/') + 1, path.length - '.sigil'.length)
}

function commitRename(path: string): void {
  if (renaming.value !== path) return
  const to = draft.value.trim()
  problem.value = pathProblem(to, props.files, path)
  if (problem.value !== undefined) return
  renaming.value = undefined
  if (to !== path) emit('rename', path, to)
  void nextTick(() => focusTab(to))
}

function cancelRename(): void {
  const path = renaming.value
  renaming.value = undefined
  problem.value = undefined
  if (path !== undefined) void nextTick(() => focusTab(path))
}

// Arrow keys move between tabs, as in any tab list.
function onKeydown(e: KeyboardEvent): void {
  if (renaming.value !== undefined || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return
  const i = props.files.findIndex((f) => f.path === props.active)
  const n = props.files.length
  const next = e.key === 'Home' ? 0 : e.key === 'End' ? n - 1 : (i + (e.key === 'ArrowRight' ? 1 : n - 1)) % n
  e.preventDefault()
  emit('select', props.files[next].path)
  void nextTick(() => focusTab(props.files[next].path))
}

function focusTab(path: string): void {
  const i = props.files.findIndex((f) => f.path === path)
  list.value?.querySelectorAll<HTMLElement>('[role="tab"]')[i]?.focus()
}

function dir(path: string): string {
  return path.slice(0, path.lastIndexOf('/') + 1)
}

function base(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

defineExpose({ startRename })
</script>
