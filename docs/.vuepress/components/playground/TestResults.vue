<template>
  <div class="pg-tests">
    <div :key="runId" :class="['pg-verdict', `pg-hue-${hue}`]">
      <Seal class="pg-verdict__seal" :hue="hue" :glyph="glyph" />
      <div class="pg-verdict__body">
        <div class="pg-verdict__label">{{ files }}</div>
        <div class="pg-verdict__decision">{{ headline }}</div>
        <div class="pg-verdict__reason">{{ detail }}</div>
      </div>
    </div>

    <section v-for="s in results" :key="s.file" class="pg-suite" :aria-label="s.file">
      <div class="pg-suite__head">
        <button type="button" class="pg-suite__file pg-mono" :title="`Open ${s.file}`" @click="emit('reveal', s.file, 1)">{{ s.file }}</button>
        <span v-if="s.policy" class="pg-dim pg-mono">{{ s.policy }}</span>
        <span v-if="!s.error" :class="['pg-suite__count', { 'pg-suite__count--fail': failedIn(s) > 0 }]">
          {{ failedIn(s) > 0 ? `${failedIn(s)} of ${s.cases.length} failed` : s.cases.length === 0 ? 'no cases ran' : `${s.cases.length} passed` }}
        </span>
      </div>
      <div v-if="s.error" class="pg-failure">
        <div class="pg-failure__title">This file's cases can't run</div>
        <div class="pg-failure__message">{{ s.error }}</div>
      </div>
      <ol v-else-if="s.cases.length" class="pg-cases">
        <li v-for="c in s.cases" :key="c.line" :class="['pg-case', c.passed ? 'pg-case--pass' : 'pg-case--fail']">
          <button type="button" class="pg-case__head" :title="`Show the case at ${s.file}:${c.line}`" @click="emit('reveal', s.file, c.line)">
            <span class="pg-case__mark" aria-hidden="true">{{ c.passed ? '✓' : '✗' }}</span>
            <span class="pg-case__name">{{ c.name }}<span class="pg-visually-hidden">{{ c.passed ? ', passed' : ', failed' }}</span></span>
            <span class="pg-case__line pg-mono">:{{ c.line }}</span>
          </button>
          <div v-if="c.error" class="pg-case__messages pg-mono">{{ c.error }}</div>
          <div v-else-if="c.failures?.length" class="pg-case__messages pg-mono">
            <div v-for="(f, i) in c.failures" :key="i">{{ f }}</div>
          </div>
        </li>
      </ol>
    </section>
  </div>
</template>

<script setup lang="ts">
// The results of a test run, like `sigil test -v`: a seal with the totals,
// then each test file with its cases, failures under the case that has
// them, and a file that can't run with why.
import { computed } from 'vue'
import type { TestResult } from '@spechtlabs/sigil/worker'
import Seal from './Seal.vue'

const props = defineProps<{
  results: TestResult[]
  /** Changes with every run, so the seal is stamped again. */
  runId: number
  /** The run filter the results are for. */
  filter: string
}>()

const emit = defineEmits<{ reveal: [file: string, line: number] }>()

const cases = computed(() => props.results.flatMap((s) => s.cases))
const failed = computed(() => cases.value.filter((c) => !c.passed).length)
const broken = computed(() => props.results.filter((s) => s.error !== undefined).length)
const ok = computed(() => failed.value === 0 && broken.value === 0)
const ran = computed(() => cases.value.length > 0 || broken.value > 0)
const hue = computed(() => (!ran.value ? 'brand' : ok.value ? 'green' : 'red'))
const glyph = computed(() => (!ran.value ? '0' : ok.value ? '✓' : '✗'))

const plural = (n: number, s: string) => `${n} ${s}${n === 1 ? '' : 's'}`

const files = computed(() => (props.results.length === 1 ? props.results[0].file : `${props.results.length} test files`))

const headline = computed(() => {
  if (cases.value.length === 0 && broken.value === 0) return 'no cases ran'
  if (failed.value > 0) return `${failed.value} failed`
  if (broken.value > 0) return `${plural(broken.value, 'file')} can't run`
  return cases.value.length === 1 ? 'passed' : `all ${cases.value.length} passed`
})

const detail = computed(() => {
  if (cases.value.length === 0) {
    if (broken.value > 0) return 'Fix the error below, then run again.'
    return props.filter ? `No case name matches ${props.filter}.` : 'The test files have no cases yet.'
  }
  if (ok.value) return `${plural(cases.value.length, 'case')} in ${plural(props.results.length, 'file')}`
  const parts = [`${cases.value.length - failed.value} of ${plural(cases.value.length, 'case')} passed`]
  if (broken.value > 0 && failed.value > 0) parts.push(`${plural(broken.value, 'file')} can't run`)
  return parts.join(', ')
})

function failedIn(s: TestResult): number {
  return s.cases.filter((c) => !c.passed).length
}
</script>
