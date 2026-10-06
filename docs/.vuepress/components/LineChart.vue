<template>
  <figure ref="rootRef" class="line-chart">
    <figcaption v-if="title" class="line-chart__title">{{ title }}</figcaption>
    <ul class="line-chart__legend">
      <li v-for="(s, i) in series" :key="s.label" class="line-chart__legend-item">
        <svg width="20" height="10" aria-hidden="true">
          <line x1="1" y1="5" x2="19" y2="5" :style="{ stroke: color(i) }" class="line-chart__key" />
        </svg>
        {{ s.label }}
      </li>
    </ul>
    <div class="line-chart__plot">
      <svg
        :width="width"
        :height="height"
        :viewBox="`0 0 ${width} ${height}`"
        role="img"
        :aria-label="ariaLabel"
        tabindex="0"
        @keydown="onKey"
        @focus="onFocus"
        @blur="active = -1"
      >
        <g class="line-chart__grid">
          <line v-for="t in yTicks" :key="'y' + t" :x1="m.left" :x2="width - m.right" :y1="sy(t)" :y2="sy(t)" />
        </g>
        <line class="line-chart__axis" :x1="m.left" :x2="width - m.right" :y1="sy(0)" :y2="sy(0)" />
        <g class="line-chart__ticks">
          <text v-for="t in yTicks" :key="'yl' + t" :x="m.left - 8" :y="sy(t)" text-anchor="end" dominant-baseline="middle">
            {{ t.toLocaleString('en-US') }}
          </text>
          <text v-for="t in xTickValues" :key="'xl' + t" :x="sx(t)" :y="sy(0) + 18" text-anchor="middle">{{ t }}</text>
          <text :x="m.left + plotWidth / 2" :y="height - 4" text-anchor="middle" class="line-chart__axis-label">{{ xLabel }}</text>
          <text x="0" :y="m.top - 20" text-anchor="start" class="line-chart__axis-label">{{ yLabel }}</text>
        </g>
        <line
          v-if="active >= 0"
          class="line-chart__crosshair"
          :x1="sx(x[active])"
          :x2="sx(x[active])"
          :y1="m.top"
          :y2="sy(0)"
        />
        <g v-for="(s, i) in series" :key="'s' + s.label">
          <polyline :points="points(s.values)" class="line-chart__line" :style="{ stroke: color(i) }" />
          <circle
            v-for="(v, j) in s.values"
            :key="j"
            :cx="sx(x[j])"
            :cy="sy(v)"
            :r="j === active ? 5 : 4"
            class="line-chart__dot"
            :style="{ fill: color(i) }"
          />
        </g>
        <text
          v-for="(y, i) in endLabelYs"
          :key="'e' + series[i].label"
          :x="sx(x[x.length - 1]) + 10"
          :y="y"
          dominant-baseline="middle"
          class="line-chart__end"
        >
          {{ format(series[i].values[series[i].values.length - 1]) }}
        </text>
        <rect
          class="line-chart__hit"
          :x="m.left"
          :y="m.top"
          :width="plotWidth"
          :height="plotHeight"
          @pointermove="onMove"
          @pointerleave="active = -1"
        />
      </svg>
      <div v-if="active >= 0" class="line-chart__tooltip" :style="tooltipStyle" aria-hidden="true">
        <div class="line-chart__tooltip-title">{{ x[active] }} {{ x[active] === 1 ? xUnit : xUnit + 's' }}</div>
        <div v-for="(s, i) in series" :key="'t' + s.label" class="line-chart__tooltip-row">
          <svg width="14" height="10" aria-hidden="true">
            <line x1="1" y1="5" x2="13" y2="5" :style="{ stroke: color(i) }" class="line-chart__key" />
          </svg>
          <strong>{{ format(s.values[active]) }}</strong>
          <span>{{ s.label }}</span>
        </div>
      </div>
    </div>
  </figure>
</template>

<script setup lang="ts">
// LineChart plots up to four series against one numeric x, for the
// performance page. Values are microseconds, a count or KiB, as unit
// says. Every value it shows is also in the table the page puts next to
// it; the hover readout only saves looking it up.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

interface Series {
  label: string
  values: number[]
}

type Unit = 'time' | 'count' | 'bytes'

const props = withDefaults(defineProps<{
  title?: string
  x: number[]
  xTicks?: number[]
  xLabel: string
  xUnit: string
  unit?: Unit
  yUnit?: string
  series: Series[]
}>(), { unit: 'time' })

const yLabels: Record<Unit, string> = { time: 'µs', count: 'allocations', bytes: 'KiB' }

const height = 300
const m = { top: 36, right: 64, bottom: 44, left: 48 }

const rootRef = ref<HTMLElement | null>(null)
const width = ref(640)
const active = ref(-1)
let observer: ResizeObserver | null = null

onMounted(() => {
  const el = rootRef.value
  if (!el) return
  observer = new ResizeObserver(() => { width.value = Math.max(300, el.clientWidth) })
  observer.observe(el)
})
onBeforeUnmount(() => observer?.disconnect())

const plotWidth = computed(() => width.value - m.left - m.right)
const plotHeight = height - m.top - m.bottom
const xMax = computed(() => Math.max(...(props.xTicks ?? props.x)))
const yTicks = computed(() => niceTicks(Math.max(...props.series.flatMap((s) => s.values))))
const yMax = computed(() => yTicks.value[yTicks.value.length - 1])
const xTickValues = computed(() => props.xTicks ?? props.x)
const yLabel = computed(() => props.yUnit ?? yLabels[props.unit])

// endLabelYs places each series' last value beside its line, pushed apart
// where lines end close together so no two labels overlap.
const endLabelYs = computed(() => {
  const gap = 14
  const ys = props.series.map((s) => sy(s.values[s.values.length - 1]))
  const order = ys.map((_, i) => i).sort((a, b) => ys[a] - ys[b])
  const placed = [...ys]
  order.forEach((i, k) => {
    if (k > 0) placed[i] = Math.max(placed[i], placed[order[k - 1]] + gap)
  })
  const overflow = placed[order[order.length - 1]] - sy(0)
  if (overflow > 0) order.forEach((i) => { placed[i] -= overflow })
  return placed
})

const ariaLabel = computed(() => {
  const parts = props.series.map((s) => `${s.label}: ${s.values.map((v, i) => `${format(v)} at ${props.x[i]}`).join(', ')}`)
  return `${props.title ?? 'Line chart'}. ${parts.join('. ')}.`
})

const tooltipStyle = computed(() => {
  const px = sx(props.x[active.value])
  const left = px > width.value / 2 ? px - 12 : px + 12
  return {
    left: `${left}px`,
    top: `${m.top}px`,
    transform: px > width.value / 2 ? 'translateX(-100%)' : 'none',
  }
})

function sx(v: number): number {
  return m.left + (v / xMax.value) * plotWidth.value
}

function sy(v: number): number {
  return m.top + plotHeight - (v / yMax.value) * plotHeight
}

function points(values: number[]): string {
  return values.map((v, i) => `${sx(props.x[i])},${sy(v)}`).join(' ')
}

// color returns the series' slot color; the palette is four validated
// categorical slots, set per theme in the style block.
function color(i: number): string {
  return `var(--line-chart-series-${i + 1})`
}

// format renders a value the way the page's tables do.
function format(v: number): string {
  if (props.unit === 'count') return v.toLocaleString('en-US')
  if (props.unit === 'bytes') {
    if (v < 1) return `${Math.round(v * 1024)} B`
    if (v < 1024) return `${threeDigits(v)} KiB`
    return `${threeDigits(v / 1024)} MiB`
  }
  if (v < 1) return `${Math.round(v * 1000)} ns`
  if (v < 1000) return `${threeDigits(v)} µs`
  return `${threeDigits(v / 1000)} ms`
}

// threeDigits rounds to three significant digits, keeping whole numbers whole.
function threeDigits(v: number): string {
  if (v < 10) return v.toFixed(2)
  if (v < 100) return v.toFixed(1)
  return `${Math.round(v)}`
}

// niceTicks returns about five evenly spaced ticks from 0 to a round
// number at or above max.
function niceTicks(max: number): number[] {
  const raw = max / 5
  const mag = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 2.5, 5, 10].map((f) => f * mag).find((s) => s >= raw) ?? 10 * mag
  const ticks: number[] = []
  for (let t = 0; t < max + step; t += step) ticks.push(Number(t.toPrecision(6)))
  return ticks
}

function onMove(e: PointerEvent) {
  const svg = (e.currentTarget as SVGElement).ownerSVGElement
  if (!svg) return
  const px = e.clientX - svg.getBoundingClientRect().left
  let best = 0
  props.x.forEach((v, i) => {
    if (Math.abs(sx(v) - px) < Math.abs(sx(props.x[best]) - px)) best = i
  })
  active.value = best
}

function onFocus() {
  active.value = props.x.length - 1
}

function onKey(e: KeyboardEvent) {
  if (e.key === 'ArrowLeft') active.value = Math.max(0, active.value - 1)
  else if (e.key === 'ArrowRight') active.value = Math.min(props.x.length - 1, active.value + 1)
  else if (e.key === 'Escape') active.value = -1
  else return
  e.preventDefault()
}
</script>

<style scoped>
.line-chart {
  --line-chart-series-1: #2a78d6;
  --line-chart-series-2: #eb6834;
  --line-chart-series-3: #1baf7a;
  --line-chart-series-4: #eda100;
  margin: 1.5rem 0;
  font-family: var(--vp-font-family-base);
  text-align: left;
}

[data-theme="dark"] .line-chart {
  --line-chart-series-1: #3987e5;
  --line-chart-series-2: #d95926;
  --line-chart-series-3: #199e70;
  --line-chart-series-4: #c98500;
}

.line-chart__title {
  margin-bottom: 0.25rem;
  color: var(--vp-c-text-1);
  font-weight: 600;
  font-size: 0.95rem;
}

.line-chart__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem 1.25rem;
  margin: 0 0 0.25rem;
  padding: 0;
  list-style: none;
  color: var(--vp-c-text-2);
  font-size: 0.85rem;
}

.line-chart__legend-item {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  margin: 0;
}

.line-chart__key {
  stroke-width: 2;
  stroke-linecap: round;
}

.line-chart__plot {
  position: relative;
}

.line-chart__plot > svg {
  display: block;
  overflow: visible;
  outline: none;
}

.line-chart__plot > svg:focus-visible {
  outline: 2px solid var(--vp-c-brand-1);
  outline-offset: 4px;
  border-radius: 4px;
}

.line-chart__grid line {
  stroke: var(--vp-c-divider);
  stroke-width: 1;
}

.line-chart__axis {
  stroke: var(--vp-c-border);
  stroke-width: 1;
}

.line-chart__ticks text {
  fill: var(--vp-c-text-3);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}

.line-chart__ticks .line-chart__axis-label {
  fill: var(--vp-c-text-2);
}

.line-chart__line {
  fill: none;
  stroke-width: 2;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.line-chart__dot {
  stroke: var(--vp-c-bg);
  stroke-width: 2;
}

.line-chart__end {
  fill: var(--vp-c-text-1);
  font-size: 12px;
  font-weight: 600;
}

.line-chart__crosshair {
  stroke: var(--vp-c-text-3);
  stroke-width: 1;
}

.line-chart__hit {
  fill: transparent;
  cursor: crosshair;
}

.line-chart__tooltip {
  position: absolute;
  z-index: 1;
  padding: 0.4rem 0.6rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 6px;
  background: var(--vp-c-bg-elv);
  box-shadow: var(--vp-shadow-2);
  color: var(--vp-c-text-2);
  font-size: 0.8rem;
  line-height: 1.5;
  white-space: nowrap;
  pointer-events: none;
}

.line-chart__tooltip-title {
  color: var(--vp-c-text-2);
  font-weight: 600;
}

.line-chart__tooltip-row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}

.line-chart__tooltip-row strong {
  color: var(--vp-c-text-1);
  font-variant-numeric: tabular-nums;
}
</style>
