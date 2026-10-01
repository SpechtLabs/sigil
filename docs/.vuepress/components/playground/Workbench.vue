<template>
  <div class="pg" @keydown="onKeydown">
    <p class="pg-visually-hidden" aria-live="polite">{{ announcement }}</p>
    <div class="pg-bar">
      <div class="pg-bar__group">
        <label class="pg-field">
          <span class="pg-field__label">Example</span>
          <select class="pg-select" :value="presetId" @change="choosePreset($event.target as HTMLSelectElement)">
            <option v-if="presetId === ''" value="" disabled>Your files</option>
            <option v-for="p in presets" :key="p.id" :value="p.id">{{ p.label }}</option>
          </select>
        </label>
        <label class="pg-field">
          <span class="pg-field__label">Policy</span>
          <select v-model="ws.policy" class="pg-select pg-mono" :disabled="policies.length < 2">
            <option v-if="policies.length === 0" value="">none defined</option>
            <option v-for="p in policies" :key="p" :value="p">{{ p }}</option>
          </select>
        </label>
        <div v-if="modes.length > 1" class="pg-modes" role="radiogroup" aria-label="Mode">
          <label v-for="m in modes" :key="m.id" :class="{ 'pg-modes--active': ws.mode === m.id }">
            <input v-model="ws.mode" type="radio" name="pg-mode" :value="m.id" />{{ m.label }}
          </label>
        </div>
      </div>
      <div class="pg-bar__group">
        <button type="button" class="pg-btn" :disabled="!engine" title="Format the open file as sigil fmt does" @click="formatActive">Format</button>
        <button type="button" class="pg-btn" :title="shareHelp" @click="share">{{ shareLabel }}</button>
        <button type="button" class="pg-btn pg-btn--run" :disabled="!engine || running" :title="runHelp" @click="runNow">
          {{ running ? 'Running' : 'Run' }}
          <kbd class="pg-kbd">{{ modKey }} Enter</kbd>
        </button>
      </div>
    </div>

    <div v-if="notice" class="pg-notice" role="status">
      <span>{{ notice.text }}</span>
      <button v-if="notice.action" type="button" class="pg-link" @click="notice.action.run">{{ notice.action.label }}</button>
      <button type="button" class="pg-notice__close" aria-label="Dismiss" @click="notice = undefined">
        <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" /></svg>
      </button>
    </div>

    <div class="pg-grid">
      <section class="pg-pane pg-pane--files" aria-label="Files">
        <FileTabs
          ref="tabs"
          :files="ws.files"
          :active="active"
          :marks="tabMarks"
          @select="openFile"
          @add="addFile"
          @rename="renameFile"
          @remove="removeFile"
        />
        <div ref="fileHost" class="pg-editor pg-editor--files" />
        <ProblemList :diagnostics="diagnostics" :ready="engine !== undefined" @reveal="revealDiagnostic" />
      </section>

      <div class="pg-column">
        <section class="pg-pane pg-pane--docs" aria-label="Input and stubs">
          <div class="pg-subtabs" role="tablist" aria-label="Input and stubs">
            <button
              v-for="p in docPanes"
              :id="`pg-doc-tab-${p.id}`"
              :key="p.id"
              type="button"
              role="tab"
              :aria-selected="docPane === p.id"
              :aria-controls="'pg-doc-panel'"
              :class="['pg-subtab', { 'pg-subtab--active': docPane === p.id }]"
              @click="openDoc(p.id)"
            >
              {{ p.label }}
              <span v-if="p.id === 'stubs' && stubCount" class="pg-subtab__count">{{ stubCount }}</span>
            </button>
          </div>
          <div id="pg-doc-panel" ref="docHost" role="tabpanel" :aria-labelledby="`pg-doc-tab-${docPane}`" class="pg-editor pg-editor--docs" />
        </section>

        <section class="pg-pane pg-pane--result" aria-label="Result">
          <div class="pg-subtabs" role="tablist" aria-label="Result views">
            <button
              v-for="v in resultViews"
              :key="v.id"
              type="button"
              role="tab"
              :aria-selected="resultView === v.id"
              :class="['pg-subtab', { 'pg-subtab--active': resultView === v.id }]"
              @click="resultView = v.id"
            >
              {{ v.label }}
            </button>
            <span v-if="lastRun && stale" class="pg-stale">
              edited since this run
              <button type="button" class="pg-link" :disabled="!engine || running" @click="runNow">Run again</button>
            </span>
          </div>
          <div :class="['pg-result', { 'pg-result--stale': stale, 'pg-result--running': running }]">
            <EngineStatus v-if="!engine" :progress="progress" :failed="engineError" @retry="startEngine" />
            <template v-else-if="lastRun">
              <div v-if="lastRun.failure && resultView === 'outcome'" class="pg-failure pg-failure--blocking">
                <div class="pg-failure__title">{{ lastRun.failure.title }}</div>
                <div class="pg-failure__message">{{ lastRun.failure.message }}</div>
                <div v-if="lastRun.failure.help" class="pg-help">{{ lastRun.failure.help }}</div>
                <button v-if="lastRun.failure.pane" type="button" class="pg-link" @click="revealDoc(lastRun.failure)">
                  Show it in the {{ lastRun.failure.pane }}
                </button>
                <span v-else-if="lastRun.failure.diagnostics?.length" class="pg-dim">The problems list shows where.</span>
              </div>
              <OutcomeView
                v-if="resultView === 'outcome' && lastRun.result"
                :result="lastRun.result"
                :run-id="runId"
                @reveal="revealPosition"
              />
              <ExplainView v-else-if="resultView === 'explain' && lastRun.explanation" :explanation="lastRun.explanation" @reveal="revealPosition" />
              <p v-else-if="resultView === 'explain'" class="pg-quiet">Explain shows every rule a policy can fire once its files compile.</p>
            </template>
            <p v-else class="pg-quiet">Run the policy to see its decision.</p>
          </div>
        </section>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// The playground itself, rendered only in the browser. It keeps the
// workspace, drives the CodeMirror editors, and talks to the engine in its
// worker: check as you type, compile, evaluate and explain on Run.
import { Compartment, EditorState, Prec, type Extension } from '@codemirror/state'
import { EditorView, keymap, placeholder } from '@codemirror/view'
import { SigilError, SigilStoppedError, SigilTimeoutError, type Diagnostic, type EvalResult, type Explanation, type SigilWorker, type SourceFile } from '@spechtlabs/sigil/worker'
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import EngineStatus from './EngineStatus.vue'
import ExplainView from './ExplainView.vue'
import FileTabs from './FileTabs.vue'
import OutcomeView from './OutcomeView.vue'
import ProblemList from './ProblemList.vue'
import { DocumentError, parseInput, parseStubs } from './documents.js'
import { diagnosticsSpec, documentState, firedSpec, looksLikeJson, reveal, sigilState, type FiredLine } from './editor.js'
import { loadEngine, type Progress } from './engine.js'
import { presets, presetWorkspace } from './presets.js'
import { constructor } from './present.js'
import { isShared, readFragment, shareFragment } from './share.js'
import { definitions, freshPath, modes, parsePosition, policyNames, type Workspace } from './workspace.js'
// The styles come with this chunk and go in when it loads: the site
// bundles every stylesheet into one, which would put them on every page.
import styles from './playground.css?inline'

type DocPane = 'input' | 'stubs'

interface Failure {
  title: string
  message: string
  help?: string
  diagnostics?: Diagnostic[]
  /** The pane the failure is in, for a document that doesn't parse. */
  pane?: DocPane
  line?: number
}

interface Run {
  result?: EvalResult
  explanation?: Explanation
  failure?: Failure
}

interface Notice {
  text: string
  action?: { label: string; run: () => void }
}

if (!document.getElementById('pg-styles')) {
  const style = document.createElement('style')
  style.id = 'pg-styles'
  style.textContent = styles
  document.head.append(style)
}

const CHECK_DELAY_MS = 300
const EVAL_TIMEOUT_MS = 2_000

const shareHelp =
  'Copy a link that holds the whole workspace: the files, the input, the stubs and the policy. It lives in the link itself; nothing is stored on a server.'
const runHelp = 'Evaluate the policy against the input: its decision, every candidate the rules produced, and the rules that fired'

const docPanes: { id: DocPane; label: string }[] = [
  { id: 'input', label: 'Input' },
  { id: 'stubs', label: 'Stubs' },
]
const resultViews = [
  { id: 'outcome', label: 'Outcome' },
  { id: 'explain', label: 'Explain' },
] as const

const presetId = ref(presets[0].id)
const ws = reactive<Workspace>(presetWorkspace(presetId.value))
const active = ref(primaryFile(ws))
const docPane = ref<DocPane>('input')
const resultView = ref<'outcome' | 'explain'>('outcome')

const engine = shallowRef<SigilWorker>()
const progress = ref<Progress>()
const engineError = ref<string>()

const diagnostics = shallowRef<Diagnostic[]>([])
const lastRun = shallowRef<Run>()
const fired = shallowRef<Record<string, FiredLine[]>>({})
const runId = ref(0)
const running = ref(false)
const stale = ref(false)
const notice = ref<Notice>()
const shareLabel = ref('Share')
// One line for screen readers after each run; the result pane itself is
// too much to read out.
const announcement = ref('')

const tabs = ref<InstanceType<typeof FileTabs>>()
const fileHost = ref<HTMLElement>()
const docHost = ref<HTMLElement>()

// Editor states live outside Vue's reactivity: one per file, so each keeps
// its own undo history, and one each for the input and the stubs.
const fileStates = new Map<string, EditorState>()
let fileView: EditorView | undefined
let docView: EditorView | undefined
let inputState: EditorState
let stubsState: EditorState
const fileLabel = new Compartment()
let loadedSnapshot = ''
let checkTimer: ReturnType<typeof setTimeout> | undefined
let checkSeq = 0
let shareTimer: ReturnType<typeof setTimeout> | undefined
// Bumped when the workspace is replaced, so a run that started before
// doesn't show its result over the new one.
let generation = 0
// A run asked for while one is running; it starts when that one ends.
let rerun = false

const modKey = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'

const policies = computed(() => policyNames(ws.files))

const stubCount = computed(() => {
  try {
    return Object.keys(parseStubs(ws.stubs) ?? {}).length
  } catch {
    return 0
  }
})

const tabMarks = computed(() => {
  const marks: Record<string, { errors: number; warnings: number; fired: boolean }> = {}
  for (const f of ws.files) marks[f.path] = { errors: 0, warnings: 0, fired: (fired.value[f.path]?.length ?? 0) > 0 }
  for (const d of diagnostics.value) {
    const m = d.file === undefined ? undefined : marks[d.file]
    if (m) m[d.severity === 'error' ? 'errors' : 'warnings']++
  }
  return marks
})

// A policy that's renamed or deleted can't stay selected.
watch(policies, (names) => {
  if (!names.includes(ws.policy)) ws.policy = names[0] ?? ''
})

onMounted(async () => {
  createEditors()
  window.addEventListener('hashchange', onHashChange)
  if (isShared(location.hash)) await restore(location.hash)
  else loadedSnapshot = snapshot()
  void startEngine()
})

onBeforeUnmount(() => {
  window.removeEventListener('hashchange', onHashChange)
  clearTimeout(checkTimer)
  clearTimeout(shareTimer)
  fileView?.destroy()
  docView?.destroy()
})

// The engine

async function startEngine(): Promise<void> {
  engineError.value = undefined
  try {
    engine.value = await loadEngine((p) => (progress.value = p))
  } catch (err) {
    engineError.value = messageOf(err)
    return
  }
  void checkNow()
  void runNow()
}

async function checkNow(retry = true): Promise<void> {
  const sigil = engine.value
  if (sigil === undefined) return
  const seq = ++checkSeq
  let found: Diagnostic[]
  try {
    found = await sigil.check(files())
  } catch (err) {
    // A worker replaced after another call timed out or stopped fails the
    // calls waiting on it; that says nothing about the files, so check
    // again, once, in the next worker.
    if (retry && (err instanceof SigilTimeoutError || err instanceof SigilStoppedError)) {
      if (seq === checkSeq) {
        clearTimeout(checkTimer)
        checkTimer = setTimeout(() => void checkNow(false), CHECK_DELAY_MS)
      }
      return
    }
    found = [{ severity: 'error', message: messageOf(err) }]
  }
  if (seq === checkSeq) showDiagnostics(found)
}

function scheduleCheck(): void {
  clearTimeout(checkTimer)
  checkTimer = setTimeout(() => void checkNow(), CHECK_DELAY_MS)
}

async function runNow(): Promise<void> {
  const sigil = engine.value
  if (sigil === undefined) return
  if (running.value) {
    rerun = true
    return
  }
  running.value = true
  try {
    do {
      rerun = false
      const started = generation
      const ran = runSnapshot()
      const run: Run = {}
      await evaluate(sigil, run)
      // A workspace replaced mid-run asked for a run of its own.
      if (started !== generation) continue
      lastRun.value = run
      announcement.value = summaryOf(run)
      showFired(run.result ? firedLines(run.result) : {})
      runId.value++
      stale.value = runSnapshot() !== ran
    } while (rerun)
  } finally {
    running.value = false
  }
}

async function evaluate(sigil: SigilWorker, run: Run): Promise<void> {
  let input: Record<string, unknown>
  let stubs: ReturnType<typeof parseStubs>
  try {
    input = parseInput(ws.input)
    stubs = parseStubs(ws.stubs)
  } catch (err) {
    if (!(err instanceof DocumentError)) throw err
    const pane: DocPane = err.message.includes('stub') ? 'stubs' : 'input'
    run.failure = { title: pane === 'input' ? "The input can't be read" : "The stubs can't be read", message: err.message, pane, line: err.line }
    return
  }
  let policy
  try {
    policy = await sigil.compile(files(), { policy: ws.policy || undefined, stubs })
  } catch (err) {
    run.failure = failureOf(err, "The files don't compile")
    return
  }
  try {
    run.explanation = await policy.explain()
    run.result = await policy.eval(input as Parameters<typeof policy.eval>[0], { timeoutMs: EVAL_TIMEOUT_MS })
  } catch (err) {
    run.failure = failureOf(err, "The input doesn't fit the kind")
  } finally {
    void policy.release().catch(() => {})
  }
}

// A run in one sentence: the decision and its reason, what was collected,
// or why there's no result.
function summaryOf(run: Run): string {
  const r = run.result
  if (r === undefined) return run.failure ? `${run.failure.title}.` : ''
  const failed = r.error ? `The evaluation failed with a ${r.error.kind} error. ` : ''
  if (r.collect) return `${failed}${r.policy} collected ${r.outcome.length} decision${r.outcome.length === 1 ? '' : 's'}.`
  return `${failed}${r.policy}: ${r.decision}, reason ${r.reason}.`
}

function failureOf(err: unknown, title: string): Failure {
  if (err instanceof SigilTimeoutError) {
    return { title: "The engine didn't answer in time", message: err.message, help: 'It was restarted; run again to retry.' }
  }
  if (err instanceof SigilError) return { title, message: err.message, help: err.help, diagnostics: err.diagnostics }
  return { title: 'Something went wrong in the engine', message: messageOf(err) }
}

async function formatActive(): Promise<void> {
  const sigil = engine.value
  const view = fileView
  if (sigil === undefined || view === undefined) return
  const source = view.state.doc.toString()
  try {
    const formatted = await sigil.format(source, { path: active.value })
    if (formatted !== source && view.state.doc.toString() === source) {
      view.dispatch({ changes: { from: 0, to: source.length, insert: formatted } })
    }
  } catch (err) {
    notice.value = { text: `${active.value} can't be formatted until it parses: ${messageOf(err)}` }
  }
}

// Marks in the editors

function showDiagnostics(found: Diagnostic[]): void {
  diagnostics.value = found
  updateStates((path, state) => diagnosticsSpec(state, found.filter((d) => d.file === path)))
}

function showFired(lines: Record<string, FiredLine[]>): void {
  fired.value = lines
  updateStates((path) => firedSpec(lines[path] ?? []))
}

function updateStates(spec: (path: string, state: EditorState) => Parameters<EditorState['update']>[0]): void {
  for (const [path, state] of fileStates) {
    if (path === active.value && fileView) fileView.dispatch(spec(path, fileView.state))
    else fileStates.set(path, state.update(spec(path, state)).state)
  }
}

// Every rule that produced a candidate, and every invocation it came
// through, lights up at its line.
function firedLines(result: EvalResult): Record<string, FiredLine[]> {
  const lines: Record<string, FiredLine[]> = {}
  const add = (position: string | undefined, winner: boolean, label: string) => {
    const p = parsePosition(position)
    if (p === undefined) return
    const list = (lines[p.file] ??= [])
    const same = list.find((l) => l.line === p.line)
    if (same === undefined) list.push({ line: p.line, winner, label })
    else same.winner ||= winner
  }
  for (const c of result.trace) {
    const winner = c.outcome === true
    add(c.position, winner, constructor(c))
    for (const p of c.chain ?? []) add(p, winner, `invokes ${constructor(c)}`)
  }
  return lines
}

// Editors

function createEditors(): void {
  fileView = new EditorView({ parent: fileHost.value, state: fileState(active.value) })
  inputState = inputEditorState(ws.input)
  stubsState = documentState(ws.stubs, 'yaml', docExtensions('stubs'))
  docView = new EditorView({ parent: docHost.value, state: inputState })
}

function fileState(path: string): EditorState {
  let state = fileStates.get(path)
  if (state === undefined) {
    const source = ws.files.find((f) => f.path === path)?.source ?? ''
    state = sigilState(source, [
      runKeymap,
      fileLabel.of(labelOf(path)),
      EditorView.updateListener.of((u) => {
        if (u.view !== fileView || u.startState === u.state) return
        fileStates.set(active.value, u.state)
        if (u.docChanged) onFileEdited(u.state.doc.toString())
      }),
    ])
    // A file opened after a run or a check gets their marks too.
    state = state.update(firedSpec(fired.value[path] ?? [])).state
    state = state.update(diagnosticsSpec(state, diagnostics.value.filter((d) => d.file === path))).state
    fileStates.set(path, state)
  }
  return state
}

function inputEditorState(source: string): EditorState {
  return documentState(source, looksLikeJson(source) ? 'json' : 'yaml', docExtensions('input'))
}

function labelOf(path: string): Extension {
  return EditorView.contentAttributes.of({ 'aria-label': `Source of ${path}` })
}

function docExtensions(pane: DocPane): Extension {
  return [
    runKeymap,
    EditorView.contentAttributes.of({ 'aria-label': pane === 'input' ? 'Input, JSON or YAML' : 'Host function stubs, YAML' }),
    placeholder(
      pane === 'input'
        ? 'An object with one key per input the kind declares, in JSON or YAML'
        : "Stand-ins for the kind's host functions, as a test file's stubs: gives them",
    ),
    EditorView.updateListener.of((u) => {
      if (!u.docChanged) return
      const text = u.state.doc.toString()
      if (pane === 'input') {
        inputState = u.state
        ws.input = text
      } else {
        stubsState = u.state
        ws.stubs = text
      }
      markStale()
    }),
  ]
}

const runKeymap = Prec.highest(
  keymap.of([
    {
      key: 'Mod-Enter',
      run: () => {
        void runNow()
        return true
      },
    },
  ]),
)

function onFileEdited(source: string): void {
  const file = ws.files.find((f) => f.path === active.value)
  if (file) file.source = source
  markStale()
  scheduleCheck()
}

function markStale(): void {
  if (lastRun.value) stale.value = true
}

function onKeydown(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.key !== 'Enter' || !(e.metaKey || e.ctrlKey)) return
  e.preventDefault()
  void runNow()
}

// Files

function openFile(path: string): void {
  if (fileView === undefined || !ws.files.some((f) => f.path === path)) return
  if (path !== active.value) {
    fileStates.set(active.value, fileView.state)
    active.value = path
    fileView.setState(fileState(path))
  }
}

function openDoc(pane: DocPane): void {
  if (docView === undefined || pane === docPane.value) return
  docPane.value = pane
  docView.setState(pane === 'input' ? inputState : stubsState)
}

function addFile(): void {
  const path = freshPath(ws.files)
  ws.files.push({ path, source: '' })
  openFile(path)
  void tabs.value?.startRename(path)
}

function renameFile(from: string, to: string): void {
  const file = ws.files.find((f) => f.path === from)
  if (file === undefined) return
  file.path = to
  const label = fileLabel.reconfigure(labelOf(to))
  if (from === active.value) {
    fileStates.delete(from)
    active.value = to
    fileView?.dispatch({ effects: label })
  } else {
    const state = fileStates.get(from)
    fileStates.delete(from)
    if (state) fileStates.set(to, state.update({ effects: label }).state)
  }
  scheduleCheck()
}

function removeFile(path: string): void {
  const index = ws.files.findIndex((f) => f.path === path)
  if (index < 0 || ws.files.length === 1) return
  const [file] = ws.files.splice(index, 1)
  const state = fileStates.get(path)
  if (path === active.value) {
    active.value = ws.files[Math.min(index, ws.files.length - 1)].path
    fileView?.setState(fileState(active.value))
  }
  fileStates.delete(path)
  markStale()
  scheduleCheck()
  notice.value = {
    text: `Deleted ${path}.`,
    action: {
      label: 'Undo',
      run: () => {
        if (ws.files.some((f) => f.path === path)) return
        ws.files.splice(index, 0, file)
        if (state) fileStates.set(path, state)
        openFile(path)
        notice.value = undefined
        scheduleCheck()
      },
    },
  }
}

// Jumping to a place

function revealPosition(position: string): void {
  let p = parsePosition(position)
  if (p === undefined) {
    // Explain's chains name policies, `checkout.alerts:5`.
    const m = /^(.*):(\d+)$/.exec(position)
    const file = m ? definitions(ws.files).get(m[1]) : undefined
    if (m === null || file === undefined) return
    p = { file, line: Number(m[2]), column: 1 }
  }
  openFile(p.file)
  if (fileView && active.value === p.file) reveal(fileView, p.line, p.column)
}

function revealDiagnostic(d: Diagnostic): void {
  if (d.file === undefined) return
  openFile(d.file)
  if (fileView && active.value === d.file) reveal(fileView, d.line ?? 1, d.column ?? 1)
}

function revealDoc(f: Failure): void {
  if (f.pane === undefined) return
  openDoc(f.pane)
  if (docView) reveal(docView, f.line ?? 1)
}

// The workspace as a whole: presets and shared links

function files(): SourceFile[] {
  return ws.files.map((f) => ({ path: f.path, source: f.source }))
}

// What a run reads, to tell whether its result still matches the files.
function runSnapshot(): string {
  return JSON.stringify({ files: ws.files, input: ws.input, stubs: ws.stubs, policy: ws.policy })
}

function snapshot(): string {
  return JSON.stringify({ files: files(), input: ws.input, stubs: ws.stubs })
}

function setWorkspace(next: Workspace): void {
  generation++
  // The old marks go first, so the new files' editors start without them.
  lastRun.value = undefined
  stale.value = false
  diagnostics.value = []
  fired.value = {}
  Object.assign(ws, next)
  fileStates.clear()
  active.value = primaryFile(next)
  fileView?.setState(fileState(active.value))
  inputState = inputEditorState(next.input)
  stubsState = documentState(next.stubs, 'yaml', docExtensions('stubs'))
  docView?.setState(docPane.value === 'input' ? inputState : stubsState)
  loadedSnapshot = snapshot()
  if (engine.value) {
    void checkNow()
    void runNow()
  }
}

function choosePreset(select: HTMLSelectElement): void {
  const id = select.value
  const preset = presets.find((p) => p.id === id)
  if (preset === undefined) return
  if (snapshot() !== loadedSnapshot && !window.confirm(`Replace your files with the ${preset.label.toLowerCase()} example?`)) {
    // Put the select back to what's loaded.
    select.value = presetId.value
    return
  }
  presetId.value = id
  if (isShared(location.hash)) history.replaceState(history.state, '', location.pathname + location.search)
  setWorkspace(presetWorkspace(id))
}

async function share(): Promise<void> {
  const fragment = await shareFragment({ ...ws, files: files() })
  history.replaceState(history.state, '', fragment)
  loadedSnapshot = snapshot()
  try {
    await navigator.clipboard.writeText(location.href)
    shareLabel.value = 'Link copied'
  } catch {
    shareLabel.value = 'Link in the address bar'
  }
  clearTimeout(shareTimer)
  shareTimer = setTimeout(() => (shareLabel.value = 'Share'), 2500)
}

async function restore(fragment: string): Promise<void> {
  try {
    const next = await readFragment(fragment)
    presetId.value = ''
    setWorkspace(next)
  } catch {
    notice.value = { text: "This link's workspace couldn't be read, so the playground opened an example instead." }
    loadedSnapshot = snapshot()
  }
}

// A link opened in this tab replaces the files, after asking, as picking
// an example does.
function onHashChange(): void {
  if (!isShared(location.hash)) return
  if (snapshot() !== loadedSnapshot && !window.confirm('Replace your files with the ones in this link?')) return
  void restore(location.hash)
}

/** The file to open first: the one that defines the selected policy. */
function primaryFile(w: Workspace): string {
  return definitions(w.files).get(w.policy) ?? w.files[0]?.path ?? ''
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
</script>
