<template>
  <nav class="pg-tree" aria-label="Files">
    <ul class="pg-tree__list">
      <li v-for="row in rows" :key="row.path">
        <button
          v-if="row.folder"
          type="button"
          class="pg-tree__row pg-tree__row--folder"
          :style="{ '--depth': row.depth }"
          :aria-expanded="!collapsed.has(row.path)"
          @click="toggle(row.path)"
        >
          <svg class="pg-tree__chevron" viewBox="0 0 16 16" aria-hidden="true"><path d="M6 4l4 4-4 4" /></svg>
          <span class="pg-tree__name">{{ row.name }}/</span>
          <span v-if="collapsed.has(row.path)" class="pg-tree__count">{{ row.count }}</span>
        </button>
        <button
          v-else
          type="button"
          :class="['pg-tree__row', `pg-tree__row--${row.kind}`, { 'pg-tree__row--active': row.path === active }]"
          :style="{ '--depth': row.depth }"
          :aria-current="row.path === active ? 'true' : undefined"
          :title="row.path"
          @click="emit('select', row.path)"
        >
          <svg v-if="row.kind === 'sigil'" class="pg-tree__icon" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M8 2.5l1.6 1.2 2-.1.6 1.9 1.6 1.2-.6 1.9.6 1.9-1.6 1.2-.6 1.9-2-.1L8 13.5l-1.6-1.2-2 .1-.6-1.9-1.6-1.2.6-1.9-.6-1.9 1.6-1.2.6-1.9 2 .1z" />
          </svg>
          <svg v-else-if="row.kind === 'test'" class="pg-tree__icon" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M3 8.5l3 3 7-7" />
          </svg>
          <svg v-else class="pg-tree__icon" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M6 2.5c-1.5 0-2 .7-2 2v1.8c0 .9-.5 1.4-1.5 1.7 1 .3 1.5.8 1.5 1.7v1.8c0 1.3.5 2 2 2M10 2.5c1.5 0 2 .7 2 2v1.8c0 .9.5 1.4 1.5 1.7-1 .3-1.5.8-1.5 1.7v1.8c0 1.3-.5 2-2 2" />
          </svg>
          <span class="pg-tree__name">{{ row.name }}</span>
          <span v-if="marks[row.path]?.fired" class="pg-tree__fired" title="A rule here fired in the last run" />
          <span v-if="marks[row.path]?.suiteError" class="pg-tree__badge pg-tree__badge--fail" title="This file's cases can't run">!</span>
          <span v-else-if="marks[row.path]?.failed" class="pg-tree__badge pg-tree__badge--fail" :title="`${marks[row.path].failed} failed`">✗ {{ marks[row.path].failed }}</span>
          <span v-else-if="marks[row.path]?.passed" class="pg-tree__badge pg-tree__badge--pass" :title="`${marks[row.path].passed} passed`">✓ {{ marks[row.path].passed }}</span>
          <span v-if="marks[row.path]?.errors" class="pg-tree__badge pg-tree__badge--error" :title="`${marks[row.path].errors} errors`">{{ marks[row.path].errors }}</span>
          <span v-else-if="marks[row.path]?.warnings" class="pg-tree__badge pg-tree__badge--warning" :title="`${marks[row.path].warnings} warnings`">{{ marks[row.path].warnings }}</span>
        </button>
      </li>
    </ul>
    <button type="button" class="pg-tree__add" @click="emit('add')">
      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 3v10M3 8h10" /></svg>
      New file
    </button>
  </nav>
</template>

<script setup lang="ts">
// The workspace's files as a directory tree. Folders fold, and fixtures
// under testdata/ start folded, since a test file names them and one rarely
// opens them. Each file shows what the last check, run and test run said.
import { computed, reactive, watch } from 'vue'
import type { SourceFile } from '@spechtlabs/sigil/worker'
import type { FileMarks } from './present.js'
import { fileKind, type FileKind } from './workspace.js'

interface Row {
  path: string
  name: string
  depth: number
  folder: boolean
  kind?: FileKind
  count?: number
}

const props = defineProps<{
  files: SourceFile[]
  active: string
  marks: Record<string, FileMarks>
}>()

const emit = defineEmits<{
  select: [path: string]
  add: []
}>()

const collapsed = reactive(new Set<string>())

// A new set of files starts with its testdata folders folded.
watch(
  () => props.files.map((f) => f.path).join('\n'),
  (now, before) => {
    const old = new Set(before?.split('\n') ?? [])
    for (const f of props.files) {
      if (old.has(f.path)) continue
      const parts = f.path.split('/')
      for (let i = 1; i < parts.length; i++) {
        if (parts[i - 1] === 'testdata') collapsed.add(parts.slice(0, i).join('/'))
      }
    }
  },
  { immediate: true },
)

// The visible rows: folders before files at each level, both in byte order
// as a directory listing has them (alerts.sigil before alerts_test.yaml), and
// nothing inside a folded folder.
const rows = computed<Row[]>(() => {
  interface Dir { dirs: Map<string, Dir>; files: string[] }
  const root: Dir = { dirs: new Map(), files: [] }
  for (const f of props.files) {
    const parts = f.path.split('/')
    let dir = root
    for (const part of parts.slice(0, -1)) {
      if (!dir.dirs.has(part)) dir.dirs.set(part, { dirs: new Map(), files: [] })
      dir = dir.dirs.get(part) as Dir
    }
    dir.files.push(f.path)
  }
  const count = (d: Dir): number => d.files.length + [...d.dirs.values()].reduce((n, s) => n + count(s), 0)
  const out: Row[] = []
  const visit = (dir: Dir, prefix: string, depth: number) => {
    for (const [name, sub] of [...dir.dirs].sort(([a], [b]) => (a < b ? -1 : 1))) {
      const path = prefix + name
      out.push({ path, name, depth, folder: true, count: count(sub) })
      if (!collapsed.has(path)) visit(sub, `${path}/`, depth + 1)
    }
    for (const path of [...dir.files].sort((a, b) => (a < b ? -1 : 1))) {
      out.push({ path, name: path.slice(prefix.length), depth, folder: false, kind: fileKind(path) })
    }
  }
  visit(root, '', 0)
  return out
})

function toggle(path: string): void {
  if (collapsed.has(path)) collapsed.delete(path)
  else collapsed.add(path)
}

/** Unfolds the folders a file is in, so it can be seen. */
function expand(path: string): void {
  const parts = path.split('/')
  for (let i = 1; i < parts.length; i++) collapsed.delete(parts.slice(0, i).join('/'))
}

defineExpose({ expand })
</script>
