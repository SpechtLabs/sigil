<template>
  <div class="pg-explain">
    <div class="pg-section-title">
      {{ explanation.policy }}
      <span class="pg-dim">{{ summary }}</span>
    </div>
    <ol class="pg-trace">
      <li v-for="(r, i) in rules" :key="i" class="pg-trace__entry">
        <div class="pg-trace__head">
          <span :class="['pg-trace__mark', `pg-hue-${hueOf(r.decision)}`]" />
          <span :class="['pg-mono', 'pg-chip', `pg-hue-${hueOf(r.decision)}`]">{{ constructor({ decision: r.decision ?? '', reason: r.reason }) }}</span>
        </div>
        <div class="pg-trace__chain pg-dim">
          <template v-for="(p, j) in r.chain" :key="j">
            <PositionLink :position="p" @reveal="emit('reveal', $event)" /><span v-if="j < r.chain.length - 1"> then </span>
          </template>
        </div>
        <Conditions v-if="r.conditions.length" :conditions="r.conditions" />
        <Payload :expressions="r.payload" />
      </li>
    </ol>
    <template v-if="asserts.length">
      <div class="pg-section-title">Asserts <span class="pg-dim">{{ asserts.length }}</span></div>
      <ol class="pg-trace">
        <li v-for="(a, i) in asserts" :key="i" class="pg-trace__entry">
          <div class="pg-trace__head">
            <span class="pg-trace__mark pg-trace__mark--assert" />
            <span class="pg-mono pg-chip">assert {{ a.reason }}</span>
            <span class="pg-dim">{{ a.phase === 'outcome' ? 'reads the outcome' : 'checks the input' }}</span>
          </div>
          <div class="pg-trace__chain pg-dim">
            <template v-for="(p, j) in a.chain" :key="j">
              <PositionLink :position="p" @reveal="emit('reveal', $event)" /><span v-if="j < a.chain.length - 1"> then </span>
            </template>
          </div>
          <Conditions v-if="a.conditions.length" :conditions="a.conditions" />
          <div v-if="a.check" class="pg-mono pg-explain__check">{{ a.check }}</div>
        </li>
      </ol>
    </template>
  </div>
</template>

<script setup lang="ts">
// What a policy adds up to, like `sigil explain`: every rule it can fire,
// with the invocations it comes through and every condition on the way,
// then its asserts.
import { computed } from 'vue'
import type { Explanation } from '@spechtlabs/sigil/worker'
import Conditions from './Conditions.vue'
import Payload from './Payload.vue'
import PositionLink from './PositionLink.vue'
import { constructor, hueOf } from './present.js'

const props = defineProps<{ explanation: Explanation }>()

const emit = defineEmits<{ reveal: [position: string] }>()

const rules = computed(() => props.explanation.rules.filter((r) => r.kind === 'decision'))
const asserts = computed(() => props.explanation.rules.filter((r) => r.kind === 'assert'))

const summary = computed(() => {
  const e = props.explanation
  const plural = (n: number, s: string) => `${n} ${s}${n === 1 ? '' : 's'}`
  const from = [plural(e.policies, 'policy').replace('policys', 'policies')]
  if (e.modules > 0) from.push(plural(e.modules, 'module'))
  return `${plural(rules.value.length, 'rule')} from ${from.join(' and ')}`
})
</script>
