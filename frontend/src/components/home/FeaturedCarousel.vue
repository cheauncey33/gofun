<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps({
  slides: { type: Array, default: () => [] },
})

const emit = defineEmits(['select'])

const index = ref(0)
const hovering = ref(false)
const reduceMotion = ref(false)
let timer = 0

const safeIndex = computed(() => {
  if (!props.slides.length) return 0
  return ((index.value % props.slides.length) + props.slides.length) % props.slides.length
})

watch(() => props.slides.length, () => {
  index.value = 0
  restart()
})

onMounted(() => {
  reduceMotion.value = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  restart()
})

onBeforeUnmount(() => window.clearInterval(timer))

function restart() {
  window.clearInterval(timer)
  if (reduceMotion.value || hovering.value || props.slides.length < 2) return
  timer = window.setInterval(() => {
    index.value += 1
  }, 4500)
}

function go(next) {
  if (!props.slides.length) return
  index.value = next
  restart()
}

function prev() {
  go(safeIndex.value - 1)
}

function next() {
  go(safeIndex.value + 1)
}

function onEnter() {
  hovering.value = true
  window.clearInterval(timer)
}

function onLeave() {
  hovering.value = false
  restart()
}

function money(cents) {
  if (cents == null) return '待公布'
  return `¥${(cents / 100).toFixed(cents % 100 ? 2 : 0)}`
}
</script>

<template>
  <section
    v-if="slides.length"
    class="featured"
    aria-label="主推场次"
    @mouseenter="onEnter"
    @mouseleave="onLeave"
  >
    <div class="viewport">
      <div class="track" :style="{ transform: `translate3d(${-safeIndex * 100}%, 0, 0)` }">
        <article
          v-for="(slide, i) in slides"
          :key="slide.id"
          class="slide"
          :class="[`tone-${i % 5}`, { rush: slide.kind === 'rush' }]"
          @click="emit('select', slide)"
        >
          <img v-if="slide.cover" :src="slide.cover" :alt="slide.title" />
          <div class="veil" />
          <div class="copy">
            <small>{{ slide.badge }}</small>
            <h2>{{ slide.title }}</h2>
            <p>{{ slide.subtitle }}</p>
            <div class="meta">
              <strong>{{ money(slide.price) }}<em v-if="slide.kind !== 'rush'"> 起</em></strong>
              <span>{{ slide.time }}</span>
            </div>
          </div>
        </article>
      </div>
    </div>
    <button v-if="slides.length > 1" class="nav prev" type="button" aria-label="上一张" @click.stop="prev">‹</button>
    <button v-if="slides.length > 1" class="nav next" type="button" aria-label="下一张" @click.stop="next">›</button>
    <div v-if="slides.length > 1" class="dots">
      <button
        v-for="(slide, i) in slides"
        :key="slide.id"
        type="button"
        :class="{ active: i === safeIndex }"
        :aria-label="`第 ${i + 1} 张`"
        @click.stop="go(i)"
      />
    </div>
  </section>
</template>

<style scoped>
.featured {
  position: relative;
  border-radius: var(--radius-xl);
  overflow: hidden;
  isolation: isolate;
}
.viewport { overflow: hidden; }
.track {
  display: flex;
  transition: transform .55s cubic-bezier(.22, .8, .2, 1);
}
.slide {
  position: relative;
  flex: 0 0 100%;
  min-height: min(52vh, 460px);
  color: #f6eee4;
  cursor: pointer;
  overflow: hidden;
}
.slide img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.veil {
  position: absolute;
  inset: 0;
  background:
    linear-gradient(90deg, rgba(12, 8, 6, .78) 0%, rgba(12, 8, 6, .18) 58%, rgba(12, 8, 6, .45) 100%),
    linear-gradient(180deg, rgba(12, 8, 6, .12), transparent 36%, rgba(12, 8, 6, .7) 100%);
}
.tone-0 { background: linear-gradient(145deg, #1b1b1b 10%, #75402f 58%, #d63f2e); }
.tone-1 { background: linear-gradient(145deg, #18324a, #287b86 64%, #b7d4c5); }
.tone-2 { background: linear-gradient(145deg, #27223b, #315ba5 55%, #e5ad5f); }
.tone-3 { background: linear-gradient(145deg, #1b1512, #674f34 55%, #a92e24); }
.tone-4 { background: linear-gradient(145deg, #294b3f, #67a86c 62%, #f3ca61); }
.copy {
  position: relative;
  z-index: 1;
  max-width: 640px;
  min-height: min(52vh, 460px);
  padding: 42px 56px 56px;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
}
.copy small {
  align-self: flex-start;
  padding: 4px 10px;
  border-radius: var(--radius-pill);
  background: var(--red);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: .12em;
}
.copy h2 {
  margin: 14px 0 8px;
  font: 760 clamp(28px, 4vw, 48px)/1.12 var(--font-display);
}
.copy p { margin: 0; color: rgba(246, 238, 228, .78); }
.meta {
  margin-top: 18px;
  display: flex;
  align-items: baseline;
  gap: 16px;
}
.meta strong { color: #fff; font-size: 28px; }
.meta em { font-style: normal; font-size: 14px; color: rgba(246, 238, 228, .7); }
.meta span { color: rgba(246, 238, 228, .7); font-size: 13px; }
.nav {
  position: absolute;
  top: 50%;
  z-index: 2;
  width: 40px;
  height: 40px;
  transform: translateY(-50%);
  border: 0;
  border-radius: 50%;
  background: rgba(12, 8, 6, .42);
  color: #fff;
  font-size: 22px;
  cursor: pointer;
}
.nav.prev { left: 14px; }
.nav.next { right: 14px; }
.dots {
  position: absolute;
  left: 56px;
  bottom: 18px;
  z-index: 2;
  display: flex;
  gap: 8px;
}
.dots button {
  width: 22px;
  height: 4px;
  border: 0;
  border-radius: var(--radius-pill);
  background: rgba(246, 238, 228, .35);
  cursor: pointer;
}
.dots button.active { background: #fff; width: 32px; }
@media (max-width: 720px) {
  .slide, .copy { min-height: 320px; }
  .copy { padding: 24px 20px 40px; }
  .nav { display: none; }
  .dots { left: 20px; }
}
@media (prefers-reduced-motion: reduce) {
  .track { transition: none; }
}
</style>
