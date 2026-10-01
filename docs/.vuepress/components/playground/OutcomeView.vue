<template>
  <div class="pg-outcome">
    <div v-if="result.error" class="pg-failure">
      <div class="pg-failure__title">{{ failureTitle }}</div>
      <div class="pg-failure__message">{{ result.error.message }}</div>
      <div v-if="result.error.help" class="pg-help">{{ result.error.help }}</div>
      <ol v-if="result.error.asserts?.length" class="pg-failure__list">
        <li v-for="(a, i) in result.error.asserts" :key="i">
          <span class="pg-mono">assert {{ a.reason }}</span>
          <span class="pg-dim"> in {{ a.policy }}</span>
          <PositionLink :position="a.position" @reveal="emit('reveal', $event)" />
          <div v-if="a.cause" class="pg-mono pg-failure__cause">{{ a.cause }}</div>
          <div v-if="a.help" class="pg-help">{{ a.help }}</div>
        </li>
      </ol>
      <ol v-if="result.error.candidates?.length" class="pg-failure__list">
        <li v-for="(c, i) in result.error.candidates" :key="i">
          <span :class="['pg-mono', 'pg-chip', `pg-hue-${hueOf(c.decision)}`]">{{ constructor(c) }}</span>
          <PositionLink :position="c.position" @reveal="emit('reveal', $event)" />
        </li>
      </ol>
    </div>

    <div :key="runId" :class="['pg-verdict', `pg-hue-${verdictHue}`, { 'pg-verdict--fallback': result.error }]">
      <Seal class="pg-verdict__seal" :hue="verdictHue" :glyph="glyph" />
      <div class="pg-verdict__body">
        <div class="pg-verdict__label">{{ verdictLabel }}</div>
        <template v-if="!result.collect">
          <div class="pg-verdict__decision pg-mono">{{ result.decision }}</div>
          <div class="pg-verdict__reason pg-mono">reason: {{ result.reason }}</div>
          <Payload :payload="result.payload" />
        </template>
        <ol v-else-if="result.outcome.length" class="pg-verdict__entries">
          <li v-for="(e, i) in result.outcome" :key="i">
            <span :class="['pg-mono', 'pg-chip', `pg-hue-${hueOf(e.decision)}`]">{{ constructor(e) }}</span>
            <Payload :payload="e.payload" />
          </li>
        </ol>
      </div>
    </div>

    <div v-if="beaten.length" class="pg-beats">
      beats
      <span v-for="(c, i) in beaten" :key="i" :class="['pg-mono', 'pg-chip', `pg-hue-${hueOf(c.decision)}`]">{{ constructor(c) }}</span>
    </div>

    <div v-if="result.trace.length || !result.error" class="pg-section-title">
      Trace
      <span class="pg-dim">{{ result.trace.length === 0 ? 'no rule fired' : `${result.trace.length} candidate${result.trace.length === 1 ? '' : 's'}` }}</span>
    </div>
    <ol v-if="result.trace.length" class="pg-trace">
      <li v-for="(c, i) in result.trace" :key="i" :class="['pg-trace__entry', c.outcome ? 'pg-trace__entry--won' : 'pg-trace__entry--lost']">
        <div class="pg-trace__head">
          <span :class="['pg-trace__mark', `pg-hue-${hueOf(c.decision)}`]" :title="c.outcome ? 'In the outcome' : 'Outranked'" />
          <span :class="['pg-mono', 'pg-chip', `pg-hue-${hueOf(c.decision)}`]">{{ constructor(c) }}</span>
          <PositionLink :position="c.position" @reveal="emit('reveal', $event)" />
        </div>
        <div v-if="c.chain?.length" class="pg-trace__chain pg-dim">
          via
          <template v-for="(p, j) in c.chain" :key="j">
            <PositionLink :position="p" @reveal="emit('reveal', $event)" /><span v-if="j < c.chain.length - 1"> then </span>
          </template>
        </div>
        <Conditions v-if="c.conditions?.length" :conditions="c.conditions" />
        <Payload :payload="c.payload" />
      </li>
    </ol>
    <p v-else-if="!result.error" class="pg-quiet">The kind's default applies when no rule fires.</p>
  </div>
</template>

<script setup lang="ts">
// The result of one evaluation: a failure first, if there was one, then the
// outcome stamped with its seal, what it beat, and the full trace.
import { computed } from 'vue'
import type { EvalResult } from '@spechtlabs/sigil/worker'
import Conditions from './Conditions.vue'
import Payload from './Payload.vue'
import PositionLink from './PositionLink.vue'
import Seal from './Seal.vue'
import { constructor, hueOf } from './present.js'

const props = defineProps<{
  result: EvalResult
  /** Changes with every run, so the seal is stamped again. */
  runId: number
}>()

const emit = defineEmits<{ reveal: [position: string] }>()

const FAILURES: Record<string, string> = {
  runtime: 'A runtime error stopped the evaluation',
  conflict: 'The candidates conflict',
  assertion: 'An assert failed',
  canceled: 'The evaluation ran out of time',
}

const failureTitle = computed(() => {
  const e = props.result.error
  if (e === undefined) return ''
  if (e.kind === 'assertion') return e.asserts && e.asserts.length > 1 ? `${e.asserts.length} asserts failed` : FAILURES.assertion
  return FAILURES[e.kind] ?? e.kind
})

const verdictHue = computed(() => (props.result.collect ? 'brand' : hueOf(props.result.decision)))

const glyph = computed(() => {
  if (props.result.collect) return String(props.result.outcome.length)
  return props.result.decision?.[0] ?? ''
})

const verdictLabel = computed(() => {
  const r = props.result
  if (r.error) return r.collect ? 'The outcome is empty after a failure' : "The kind's fallback applies"
  if (r.collect) {
    const n = r.outcome.length
    return n === 0 ? `${r.policy} grants nothing` : `${r.policy} collected ${n} decision${n === 1 ? '' : 's'}`
  }
  if (r.trace.length === 0) return `${r.policy}: no rule fired, so the default applies`
  return r.policy
})

// For a kind that picks one decision: the candidates the winner outranked.
const beaten = computed(() => (props.result.collect || props.result.error ? [] : props.result.trace.filter((c) => !c.outcome)))
</script>
