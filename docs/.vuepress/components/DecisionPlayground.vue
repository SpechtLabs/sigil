<template>
  <figure class="playground">
    <figcaption class="playground__title">Route an alert through <code>checkout.alerts</code></figcaption>

    <form class="playground__inputs" @submit.prevent>
      <label class="playground__field">
        <span>alert.name</span>
        <select v-model="name">
          <option v-for="n in names" :key="n" :value="n">{{ n }}</option>
        </select>
      </label>
      <fieldset class="playground__field">
        <legend>alert.severity</legend>
        <div class="playground__segmented">
          <label v-for="s in severities" :key="s" :class="{ active: severity === s }">
            <input v-model="severity" type="radio" name="severity" :value="s" />{{ s }}
          </label>
        </div>
      </fieldset>
      <fieldset class="playground__field">
        <legend>alert.labels["env"]</legend>
        <div class="playground__segmented">
          <label v-for="e in envs" :key="e" :class="{ active: env === e }">
            <input v-model="env" type="radio" name="env" :value="e" />{{ e }}
          </label>
        </div>
      </fieldset>
      <label class="playground__field playground__field--wide">
        <span>alert.firing_for <output>{{ firingFor }}m</output></span>
        <input v-model.number="firingFor" type="range" min="0" max="60" step="1" />
      </label>
    </form>

    <ol class="playground__rules" aria-label="Rules">
      <li v-for="r in evaluated" :key="r.line" :class="['playground__rule', r.fires ? 'fires' : 'quiet']">
        <span class="playground__line">{{ r.line }}</span>
        <code class="playground__cond">when {{ r.cond }}</code>
        <code :class="['playground__decision', r.decision]">{{ r.decision }}({{ r.reason }})</code>
        <span class="playground__state">{{ r.fires ? 'fires' : '–' }}</span>
      </li>
    </ol>

    <div class="playground__result" aria-live="polite">
      <div class="playground__label">
        {{ candidates.length === 0 ? 'No rule fired, so the kind’s default applies' : `${candidates.length} candidate${candidates.length === 1 ? '' : 's'}; precedence picks one` }}
      </div>
      <code :class="['playground__winner', winner.decision]">
        {{ winner.decision }}(reason: {{ winner.reason }}){{ winner.payload ? `  ${winner.payload}` : '' }}
      </code>
      <div v-if="candidates.length > 1" class="playground__losers">
        beats
        <code v-for="c in candidates.slice(1)" :key="c.line" :class="c.decision">{{ c.decision }}({{ c.reason }})</code>
      </div>
    </div>
  </figure>
</template>

<script setup lang="ts">
// DecisionPlayground evaluates the tour's checkout.alerts policy in the
// browser, so a reader can change an alert and watch which rules fire and
// which candidate wins. It mirrors the policy on the tour page by hand;
// the tour's `sigil eval` transcripts are the real thing.
import { computed, ref } from 'vue'

type Decision = 'page' | 'drop' | 'notify'

interface Alert {
  name: string
  severity: string
  env: string
  firingFor: number
}

interface Rule {
  line: number
  cond: string
  decision: Decision
  reason: string
  payload?: string
  holds: (a: Alert) => boolean
}

const names = ['CheckoutErrorRate', 'CheckoutLatencyHigh', 'CheckoutCanaryLatency']
const severities = ['critical', 'warning', 'info']
const envs = ['production', 'staging']

const team = { oncall: 'checkout-primary', channel: '#checkout-alerts' }

// The rules of checkout.alerts, with the line of each constructor.
const rules: Rule[] = [
  { line: 6, cond: 'in_production and alert.severity == critical', decision: 'page', reason: 'critical_alert', payload: `target = "${team.oncall}"`, holds: (a) => a.env === 'production' && a.severity === 'critical' },
  { line: 11, cond: '… warning and alert.firing_for >= 30m', decision: 'page', reason: 'sustained', payload: `target = "${team.oncall}"`, holds: (a) => a.env === 'production' && a.severity === 'warning' && a.firingFor >= 30 },
  { line: 14, cond: 'in_production and alert.severity == warning', decision: 'notify', reason: 'routine', payload: `channel = "${team.channel}"`, holds: (a) => a.env === 'production' && a.severity === 'warning' },
  { line: 18, cond: 'not in_production', decision: 'drop', reason: 'not_production', holds: (a) => a.env !== 'production' },
  { line: 22, cond: 'alert.name in ["CheckoutCanaryLatency"]', decision: 'drop', reason: 'muted', holds: (a) => a.name === 'CheckoutCanaryLatency' },
]

// precedence page > drop > notify, and each decision's reasons in order.
const rank: Record<string, number> = {
  'page:critical_alert': 0,
  'page:sustained': 1,
  'drop:muted': 2,
  'drop:not_production': 3,
  'notify:routine': 4,
  'notify:unrouted': 5,
}

const name = ref('CheckoutLatencyHigh')
const severity = ref('warning')
const env = ref('production')
const firingFor = ref(12)

const alert = computed<Alert>(() => ({ name: name.value, severity: severity.value, env: env.value, firingFor: firingFor.value }))

const evaluated = computed(() => rules.map((r) => ({ ...r, fires: r.holds(alert.value) })))

const candidates = computed(() =>
  evaluated.value
    .filter((r) => r.fires)
    .sort((x, y) => rank[`${x.decision}:${x.reason}`] - rank[`${y.decision}:${y.reason}`]),
)

const winner = computed(() =>
  candidates.value[0] ?? { line: 0, decision: 'notify' as Decision, reason: 'unrouted', payload: 'channel = "#alerts"' },
)
</script>

<style scoped>
.playground {
  text-align: left;
  --pg-page: var(--vp-c-danger-1);
  --pg-drop: var(--vp-c-warning-1);
  --pg-notify: var(--vp-c-brand-1);
  margin: 1.5rem 0;
  padding: 1rem 1.25rem 1.25rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 12px;
  background: var(--vp-c-bg-soft);
  font-family: var(--vp-font-family-base);
}
.playground__title {
  text-align: left;
  margin-bottom: 0.75rem;
  color: var(--vp-c-text-1);
  font-weight: 600;
  font-size: 0.95rem;
}
.playground__inputs {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(210px, 1fr));
  gap: 0.75rem 1rem;
  margin-bottom: 1rem;
}
.playground__field {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
  color: var(--vp-c-text-2);
  font-family: var(--vp-font-family-mono);
  font-size: 0.8rem;
}
.playground__field--wide {
  grid-column: 1 / -1;
}
.playground__field legend {
  margin-bottom: 0.3rem;
  padding: 0;
}
.playground__field output {
  margin-left: 0.4rem;
  color: var(--vp-c-text-1);
  font-weight: 600;
}
.playground__field select {
  padding: 0.3rem 0.5rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 6px;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-1);
  font: inherit;
}
.playground__field input[type='range'] {
  width: 100%;
  accent-color: var(--vp-c-brand-1);
}
.playground__segmented {
  display: flex;
  overflow: hidden;
  border: 1px solid var(--vp-c-divider);
  border-radius: 6px;
}
.playground__segmented label {
  flex: 1;
  white-space: nowrap;
  padding: 0.3rem 0.4rem;
  background: var(--vp-c-bg);
  color: var(--vp-c-text-2);
  text-align: center;
  cursor: pointer;
}
.playground__segmented label + label {
  border-left: 1px solid var(--vp-c-divider);
}
.playground__segmented label.active {
  background: var(--vp-c-brand-soft);
  color: var(--vp-c-brand-1);
  font-weight: 600;
}
.playground__segmented label:has(input:focus-visible) {
  outline: 2px solid var(--vp-c-brand-1);
  outline-offset: -2px;
}
.playground__segmented input {
  position: absolute;
  opacity: 0;
  pointer-events: none;
}
.playground__rules {
  margin: 0 0 1rem;
  padding: 0;
  list-style: none;
}
.playground__rule {
  display: grid;
  grid-template-columns: 2rem 1fr auto 3rem;
  align-items: center;
  gap: 0.5rem;
  margin: 0;
  padding: 0.35rem 0.5rem;
  border-radius: 6px;
  transition: background-color 0.15s, opacity 0.15s;
}
.playground__rule.quiet {
  opacity: 0.5;
}
.playground__rule.fires {
  background: var(--vp-c-bg);
}
.playground__line {
  color: var(--vp-c-text-3);
  font-family: var(--vp-font-family-mono);
  font-size: 0.75rem;
  text-align: right;
}
.playground code.playground__cond {
  overflow: hidden;
  font-size: 0.8rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.playground__state {
  color: var(--vp-c-text-3);
  font-size: 0.75rem;
  text-align: right;
}
.playground__rule.fires .playground__state {
  color: var(--vp-c-text-1);
  font-weight: 600;
}
.playground code {
  padding: 0;
  border-radius: 0;
  background: transparent;
  color: var(--vp-c-text-1);
  font-size: 0.8rem;
}
.playground code.page {
  color: var(--pg-page);
}
.playground code.drop {
  color: var(--pg-drop);
}
.playground code.notify {
  color: var(--pg-notify);
}
.playground__result {
  padding: 0.75rem 1rem;
  border-left: 3px solid var(--vp-c-brand-1);
  border-radius: 0 6px 6px 0;
  background: var(--vp-c-bg);
}
.playground__label {
  margin-bottom: 0.3rem;
  color: var(--vp-c-text-2);
  font-size: 0.8rem;
}
.playground code.playground__winner {
  font-size: 0.95rem;
  font-weight: 600;
  white-space: pre-wrap;
}
.playground__losers {
  margin-top: 0.4rem;
  color: var(--vp-c-text-3);
  font-size: 0.8rem;
}
.playground__losers code {
  margin-left: 0.4rem;
}
@media (max-width: 640px) {
  .playground__rule {
    grid-template-columns: 1.5rem 1fr 2.5rem;
  }
  .playground__decision {
    grid-column: 2;
  }
}
</style>
