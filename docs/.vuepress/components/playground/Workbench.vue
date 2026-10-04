<template>
  <div class="pg">
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
        <div class="pg-modes" role="radiogroup" aria-label="Mode">
          <label v-for="m in modes" :key="m.id" :class="{ 'pg-modes--active': ws.mode === m.id }" :title="modeHelp[m.id]">
            <input v-model="ws.mode" type="radio" name="pg-mode" :value="m.id" />{{ m.label }}
          </label>
        </div>
        <label v-show="ws.mode === 'evaluate'" class="pg-field">
          <span class="pg-field__label">Policy</span>
          <select v-model="ws.policy" class="pg-select pg-mono" :disabled="policies.length < 2">
            <option v-if="policies.length === 0" value="">none defined</option>
            <option v-for="p in policies" :key="p" :value="p">{{ p }}</option>
          </select>
        </label>
      </div>
      <div class="pg-bar__group">
        <button type="button" class="pg-btn" :title="shareHelp" @click="share">{{ shareLabel }}</button>
        <button type="button" class="pg-btn pg-btn--run" :disabled="!engine || running" :title="modeHelp[ws.mode]" @click="runNow">
          {{ running ? 'Running' : ws.mode === 'test' ? 'Run tests' : 'Run' }}
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
        <div class="pg-files">
          <FileTree ref="tree" class="pg-files__tree" :files="ws.files" :active="active" :marks="fileMarks" @select="openFile" @add="addFile" />
          <div class="pg-files__main">
            <FileBar
              ref="fileBar"
              :path="active"
              :files="ws.files"
              :formattable="activeKind === 'sigil'"
              :can-format="engine !== undefined"
              :deletable="ws.files.length > 1"
              @format="formatActive"
              @rename="(to) => renameFile(active, to)"
              @remove="removeFile(active)"
            />
            <div ref="fileHost" class="pg-editor pg-editor--files" />
          </div>
        </div>
        <ProblemList :diagnostics="diagnostics" :ready="engine !== undefined" @reveal="revealDiagnostic" />
      </section>

      <div v-show="ws.mode === 'evaluate'" class="pg-column">
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
              <FailureNote v-if="lastRun.failure && resultView === 'outcome'" :failure="lastRun.failure" @show="revealDoc" />
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

      <section v-show="ws.mode === 'test'" class="pg-pane pg-pane--tests" aria-label="Test results">
        <div class="pg-testbar">
          <label class="pg-filter">
            <span class="pg-filter__label">Cases matching</span>
            <input
              v-model="ws.run"
              class="pg-filter__input pg-mono"
              placeholder="every case"
              spellcheck="false"
              autocomplete="off"
              aria-describedby="pg-filter-help"
              @keydown.enter.prevent="runNow"
            />
          </label>
          <span id="pg-filter-help" class="pg-visually-hidden">A regular expression matched against case names, like sigil test --run.</span>
          <span v-if="lastTests && staleTests" class="pg-stale">
            edited since this run
            <button type="button" class="pg-link" :disabled="!engine || running" @click="runNow">Run again</button>
          </span>
        </div>
        <div :class="['pg-result', { 'pg-result--stale': staleTests, 'pg-result--running': running }]">
          <EngineStatus v-if="!engine" :progress="progress" :failed="engineError" @retry="startEngine" />
          <div v-else-if="testCount === 0" class="pg-empty">
            <Seal class="pg-empty__seal" hue="brand" :progress="0" />
            <div class="pg-empty__title">No test files yet</div>
            <p class="pg-empty__text">A test file is YAML named like <span class="pg-mono">checkout/alerts_test.yaml</span>, next to the policy it tests. Its cases give an input and the decision they expect.</p>
            <button type="button" class="pg-btn" @click="addTestFile">New test file</button>
          </div>
          <template v-else-if="lastTests">
            <FailureNote v-if="lastTests.failure" :failure="lastTests.failure" />
            <TestResults v-if="lastTests.results" :results="lastTests.results" :run-id="testRunId" :filter="ws.run.trim()" @reveal="revealLine" />
          </template>
          <p v-else class="pg-quiet">Run the tests to see each case.</p>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
// The playground itself, rendered only in the browser. It keeps the
// workspace, drives the CodeMirror editors, and talks to the engine in its
// worker: check as you type; compile, evaluate and explain on Run in
// Evaluate mode; run the test files in Test mode.
import { Compartment, EditorState, Prec, type Extension, type TransactionSpec } from '@codemirror/state'
import { EditorView, keymap, placeholder } from '@codemirror/view'
import {
  SigilError,
  SigilStoppedError,
  SigilTimeoutError,
  type Diagnostic,
  type EvalResult,
  type Explanation,
  type SigilWorker,
  type SourceFile,
  type TestResult,
} from '@spechtlabs/sigil/worker'
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import EngineStatus from './EngineStatus.vue'
import ExplainView from './ExplainView.vue'
import FailureNote from './FailureNote.vue'
import FileBar from './FileBar.vue'
import FileTree from './FileTree.vue'
import OutcomeView from './OutcomeView.vue'
import ProblemList from './ProblemList.vue'
import Seal from './Seal.vue'
import TestResults from './TestResults.vue'
import { DocumentError, parseInput, parseStubs } from './documents.js'
import {
  casesSpec,
  diagnosticsSpec,
  documentState,
  firedSpec,
  looksLikeJson,
  reveal,
  sigilState,
  testState,
  type FiredLine,
  type TestMarks,
} from './editor.js'
import { loadEngine, type Progress } from './engine.js'
import { presets, presetWorkspace } from './presets.js'
import { constructor, type Failure, type FileMarks } from './present.js'
import { isShared, readFragment, shareFragment } from './share.js'
import { definitions, dirOf, fileKind, filesOf, freshPath, modes, parsePosition, policyNames, type FileKind, type Workspace } from './workspace.js'
// The styles come with this chunk and go in when it loads: the site
// bundles every stylesheet into one, which would put them on every page.
import styles from './playground.css?inline'

type DocPane = 'input' | 'stubs'

interface Run {
  result?: EvalResult
  explanation?: Explanation
  failure?: Failure
}

interface TestRun {
  results?: TestResult[]
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
const FILTER_DELAY_MS = 400
const EVAL_TIMEOUT_MS = 2_000

const shareHelp =
  'Copy a link that holds the whole workspace: the files, the input, the stubs, the policy and the mode. It lives in the link itself; nothing is stored on a server.'
// What each mode does, on its segment of the switch and on the Run button.
const modeHelp: Record<Workspace['mode'], string> = {
  evaluate: 'Evaluate the policy against the input: its decision, every candidate the rules produced, and the rules that fired',
  test: 'Run the test files, the ones named *_test.yaml, and mark every case passed or failed, with what it got instead',
}

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
const stale = ref(false)
const lastTests = shallowRef<TestRun>()
const testMarks = shallowRef<Record<string, TestMarks>>({})
const testRunId = ref(0)
const staleTests = ref(false)
const running = ref(false)
const notice = ref<Notice>()
const shareLabel = ref('Share')
// One line for screen readers after each run; the result pane itself is
// too much to read out.
const announcement = ref('')

const tree = ref<InstanceType<typeof FileTree>>()
const fileBar = ref<InstanceType<typeof FileBar>>()
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
let filterTimer: ReturnType<typeof setTimeout> | undefined
let shareTimer: ReturnType<typeof setTimeout> | undefined
// Bumped when the workspace is replaced, so a run that started before
// doesn't show its result over the new one.
let generation = 0
// A run asked for while one is running; it starts when that one ends.
let rerun = false

const modKey = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘' : 'Ctrl'

const policies = computed(() => policyNames(ws.files))
const activeKind = computed(() => fileKind(active.value))
const testCount = computed(() => ws.files.filter((f) => fileKind(f.path) === 'test').length)

const stubCount = computed(() => {
  try {
    return Object.keys(parseStubs(ws.stubs) ?? {}).length
  } catch {
    return 0
  }
})

const fileMarks = computed(() => {
  const marks: Record<string, FileMarks> = {}
  for (const f of ws.files) {
    const t = testMarks.value[f.path]
    marks[f.path] = {
      errors: 0,
      warnings: 0,
      fired: (fired.value[f.path]?.length ?? 0) > 0,
      passed: t?.cases.filter((c) => c.passed).length ?? 0,
      failed: t?.cases.filter((c) => !c.passed).length ?? 0,
      suiteError: t?.error !== undefined,
    }
  }
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

// Switching modes runs the new one, unless its results are current.
watch(
  () => ws.mode,
  (mode) => {
    const current = mode === 'test' ? lastTests.value !== undefined && !staleTests.value : lastRun.value !== undefined && !stale.value
    if (!current) void runNow()
  },
)

// Narrowing the cases runs them again, once typing pauses.
watch(
  () => ws.run,
  () => {
    clearTimeout(filterTimer)
    if (ws.mode === 'test') filterTimer = setTimeout(() => void runNow(), FILTER_DELAY_MS)
  },
)

onMounted(async () => {
  createEditors()
  window.addEventListener('hashchange', onHashChange)
  window.addEventListener('keydown', onKeydown)
  if (isShared(location.hash)) await restore(location.hash)
  else loadedSnapshot = snapshot()
  void startEngine()
})

onBeforeUnmount(() => {
  window.removeEventListener('hashchange', onHashChange)
  window.removeEventListener('keydown', onKeydown)
  clearTimeout(checkTimer)
  clearTimeout(filterTimer)
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
    found = await sigil.check(filesOf(ws.files, 'sigil'))
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
  clearTimeout(filterTimer)
  if (running.value) {
    rerun = true
    return
  }
  running.value = true
  try {
    // A run asked for meanwhile, by an edit of the filter, a new workspace
    // or a switch of mode, runs next, in the mode current by then.
    do {
      rerun = false
      if (ws.mode === 'test') await runTests(sigil)
      else await runPolicy(sigil)
    } while (rerun)
  } finally {
    running.value = false
  }
}

async function runPolicy(sigil: SigilWorker): Promise<void> {
  const started = generation
  const ran = runSnapshot()
  const run: Run = {}
  await evaluate(sigil, run)
  if (started !== generation) return
  lastRun.value = run
  announcement.value = summaryOf(run)
  showFired(run.result ? firedLines(run.result) : {})
  runId.value++
  stale.value = runSnapshot() !== ran
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
    policy = await sigil.compile(filesOf(ws.files, 'sigil'), { policy: ws.policy || undefined, stubs })
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

async function runTests(sigil: SigilWorker): Promise<void> {
  const started = generation
  const ran = testSnapshot()
  const run: TestRun = {}
  try {
    if (testCount.value > 0) {
      run.results = await sigil.test(filesOf(ws.files, 'sigil'), filesOf(ws.files, 'test'), {
        data: filesOf(ws.files, 'data'),
        run: ws.run.trim() || undefined,
      })
    }
  } catch (err) {
    run.failure = failureOf(err, "The tests can't run")
  }
  if (started !== generation) return
  lastTests.value = run
  announcement.value = testSummaryOf(run)
  showCases(run.results ?? [])
  testRunId.value++
  staleTests.value = testSnapshot() !== ran
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

// A test run in one sentence: how many cases passed, and the files that
// can't run.
function testSummaryOf(run: TestRun): string {
  if (run.failure) return `${run.failure.title}.`
  const results = run.results ?? []
  const cases = results.flatMap((s) => s.cases)
  const passed = cases.filter((c) => c.passed).length
  const broken = results.filter((s) => s.error !== undefined).length
  const parts = [`${passed} of ${cases.length} cases passed`]
  if (broken > 0) parts.push(`${broken} test file${broken === 1 ? '' : 's'} can't run`)
  return `${parts.join(', ')}.`
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
  updateStates('sigil', (path, state) => diagnosticsSpec(state, found.filter((d) => d.file === path)))
}

function showFired(lines: Record<string, FiredLine[]>): void {
  fired.value = lines
  updateStates('sigil', (path) => firedSpec(lines[path] ?? []))
}

function showCases(results: TestResult[]): void {
  const marks: Record<string, TestMarks> = {}
  for (const s of results) {
    marks[s.file] = {
      error: s.error,
      cases: s.cases.map((c) => ({ line: c.line, passed: c.passed, name: c.name, messages: c.error ? [c.error] : (c.failures ?? []) })),
    }
  }
  testMarks.value = marks
  updateStates('test', (path) => casesSpec(marks[path] ?? { cases: [] }))
}

function updateStates(kind: FileKind, spec: (path: string, state: EditorState) => TransactionSpec): void {
  for (const [path, state] of fileStates) {
    if (fileKind(path) !== kind) continue
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

// A file's editor state, made the first time it's opened, with the marks
// the last check and runs left on it.
function fileState(path: string): EditorState {
  let state = fileStates.get(path)
  if (state !== undefined) return state
  const source = ws.files.find((f) => f.path === path)?.source ?? ''
  const extra = [
    runKeymap,
    fileLabel.of(labelOf(path)),
    EditorView.updateListener.of((u) => {
      if (u.view !== fileView || u.startState === u.state) return
      fileStates.set(active.value, u.state)
      if (u.docChanged) onFileEdited(u.state.doc.toString())
    }),
  ]
  const kind = fileKind(path)
  if (kind === 'sigil') {
    state = sigilState(source, extra)
    state = state.update(firedSpec(fired.value[path] ?? [])).state
    state = state.update(diagnosticsSpec(state, diagnostics.value.filter((d) => d.file === path))).state
  } else if (kind === 'test') {
    state = testState(source, extra).update(casesSpec(testMarks.value[path] ?? { cases: [] })).state
  } else {
    state = documentState(source, path.endsWith('.json') ? 'json' : 'yaml', extra)
  }
  fileStates.set(path, state)
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
      if (lastRun.value) stale.value = true
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
  if (activeKind.value === 'sigil') scheduleCheck()
}

// Files feed both modes, so an edit makes both results stale.
function markStale(): void {
  if (lastRun.value) stale.value = true
  if (lastTests.value) staleTests.value = true
}

// Cmd/Ctrl+Enter runs from anywhere on the page; the editors handle it
// themselves first.
function onKeydown(e: KeyboardEvent): void {
  if (e.defaultPrevented || e.key !== 'Enter' || !(e.metaKey || e.ctrlKey)) return
  e.preventDefault()
  void runNow()
}

// Files

function openFile(path: string): void {
  if (fileView === undefined || !ws.files.some((f) => f.path === path)) return
  tree.value?.expand(path)
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
  const path = freshPath(ws.files, 'sigil', dirOf(active.value))
  ws.files.push({ path, source: '' })
  openFile(path)
  void fileBar.value?.start()
}

// A test file for the selected policy, next to the file that defines it.
function addTestFile(): void {
  const home = definitions(ws.files).get(ws.policy) ?? active.value
  const named = home.replace(/\.sigil$/, '_test.yaml')
  const path = ws.files.some((f) => f.path === named) ? freshPath(ws.files, 'test', dirOf(home)) : named
  ws.files.push({ path, source: `policy: ${ws.policy || policies.value[0] || ''}\ncases: []\n` })
  openFile(path)
  markStale()
  void runNow()
}

function renameFile(from: string, to: string): void {
  const file = ws.files.find((f) => f.path === from)
  if (file === undefined) return
  if (fileKind(from) !== fileKind(to)) {
    // A file that changes kind needs another editor: a new state, with the
    // source but without the undo history.
    file.path = to
    fileStates.delete(from)
    if (from === active.value) {
      active.value = to
      fileView?.setState(fileState(to))
    }
  } else {
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
  }
  tree.value?.expand(to)
  markStale()
  scheduleCheck()
  fileView?.focus()
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
  revealLine(p.file, p.line, p.column)
}

function revealLine(file: string, line: number, column = 1): void {
  openFile(file)
  if (fileView && active.value === file) reveal(fileView, line, column)
}

function revealDiagnostic(d: Diagnostic): void {
  if (d.file !== undefined) revealLine(d.file, d.line ?? 1, d.column ?? 1)
}

function revealDoc(f: Failure): void {
  if (f.pane === undefined) return
  openDoc(f.pane)
  if (docView) reveal(docView, f.line ?? 1)
}

// The workspace as a whole: presets and shared links

// What a run reads, to tell whether its result still matches the files.
function runSnapshot(): string {
  return JSON.stringify({ files: ws.files, input: ws.input, stubs: ws.stubs, policy: ws.policy })
}

function testSnapshot(): string {
  return JSON.stringify({ files: ws.files, run: ws.run.trim() })
}

function snapshot(): string {
  return JSON.stringify({ files: ws.files, input: ws.input, stubs: ws.stubs })
}

function setWorkspace(next: Workspace): void {
  generation++
  // The old marks go first, so the new files' editors start without them.
  lastRun.value = undefined
  lastTests.value = undefined
  stale.value = false
  staleTests.value = false
  diagnostics.value = []
  fired.value = {}
  testMarks.value = {}
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
  setWorkspace({ ...presetWorkspace(id), mode: ws.mode })
}

async function share(): Promise<void> {
  const fragment = await shareFragment({ ...ws, files: ws.files.map((f) => ({ path: f.path, source: f.source })) })
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
  return definitions(w.files).get(w.policy) ?? filesOf(w.files, 'sigil')[0]?.path ?? w.files[0]?.path ?? ''
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
</script>
