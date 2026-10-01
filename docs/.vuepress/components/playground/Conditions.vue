<template>
  <div class="pg-conditions pg-mono">
    <div v-for="(c, i) in conditions" :key="i" :class="{ 'pg-conditions__and': i > 0 }">
      <span class="pg-conditions__word">{{ i === 0 ? 'when' : 'and' }}</span> {{ c }}
    </div>
  </div>
</template>

<script setup lang="ts">
// The `when` conditions a rule sits under, outermost first, the way the
// CLI prints them: nesting reads as "and".
defineProps<{ conditions: string[] }>()
</script>
