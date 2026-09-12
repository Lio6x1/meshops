<script setup lang="ts">
import { computed, ref } from 'vue'
import type { EntityRecord, Inventory } from '../types'
import { mapMarkers } from '../map'
import { entityTypes } from '../domain'
const props = defineProps<{ inventory: Inventory[]; records: Record<string, EntityRecord>; now: number; ready: boolean; selected?: string }>()
defineEmits<{ select: [entity: Inventory] }>()
const zoom = ref(1)
const markers = computed(() => mapMarkers(props.inventory, props.records, props.now))
const glyphs: Record<string, string> = { person: '人', drone: '✣', vehicle: '车', robot: '机', sensor: '感', facility: '充' }
</script>
<template>
  <section class="panel map-panel">
    <div class="panel-heading">
      <div><h2>实体地图 <el-tag size="small" effect="plain">模拟园区</el-tag></h2><p>实时坐标 · {{ markers.length }} 个点位 · 灰色为过期位置，悬停或点击查看 ID</p></div>
      <div class="actions"><el-button size="small" :disabled="zoom <= 1" @click="zoom = Math.max(1, zoom - .5)">缩小</el-button><el-button size="small" :disabled="zoom >= 2" @click="zoom = Math.min(2, zoom + .5)">放大</el-button><el-button size="small" @click="zoom = 1">复位</el-button></div>
    </div>
    <p v-if="!ready" class="map-warning" role="status">正在同步或连接中断，以下可能是旧位置。</p>
    <div class="map-scroll" tabindex="0" aria-label="二维实体地图，放大后可滚动查看">
      <div class="map-surface" :class="{ dense: markers.length > 12 }" :style="{ width: `${zoom * 100}%` }">
        <svg viewBox="0 0 1000 640" aria-hidden="true" class="map-drawing">
          <defs><pattern id="campus-grid" width="40" height="40" patternUnits="userSpaceOnUse"><path d="M 40 0 L 0 0 0 40" fill="none" stroke="#c7dce1" stroke-width=".7"/></pattern></defs>
          <rect width="1000" height="640" fill="#edf5f6"/><rect width="1000" height="640" fill="url(#campus-grid)"/>
          <path d="M0 340 H1000 M450 0 V640" stroke="#d1e1e5" stroke-width="58"/><path d="M0 340 H1000 M450 0 V640" stroke="#fff" stroke-width="2" stroke-dasharray="12 10"/>
          <g fill="#d9e9ee" stroke="#adc7d0" stroke-width="2"><rect x="70" y="70" width="220" height="155" rx="12"/><rect x="650" y="75" width="265" height="170" rx="12"/><rect x="70" y="450" width="220" height="120" rx="12"/></g>
          <rect x="665" y="445" width="240" height="125" rx="20" fill="#d9eadf" stroke="#afcdbb"/>
          <g fill="#557680" font-size="20" text-anchor="middle"><text x="180" y="150">综合楼</text><text x="780" y="160">实验区</text><text x="180" y="515">运维站</text><text x="785" y="515">设施区</text></g>
          <g fill="#456570" font-size="18"><text x="946" y="45">N ↑</text><text x="30" y="615">100 m</text></g><path d="M30 590 V600 H130 V590" fill="none" stroke="#456570" stroke-width="3"/>
        </svg>
        <button v-for="marker in markers" :key="marker.entityId" class="map-marker" :class="[marker.entityType, { stale: marker.stale, selected: selected === marker.entityId }]" :style="{ left: `${marker.x / 10}%`, top: `${marker.y / 6.4}%` }" :aria-label="`${entityTypes[marker.entityType] ?? marker.entityType} ${marker.entityId}，${marker.stale ? '位置已过期' : '新鲜位置'}`" @click="$emit('select', marker)">
          <span class="map-pin">{{ glyphs[marker.entityType] ?? '●' }}</span><span class="map-label">{{ marker.entityId }}</span>
        </button>
      </div>
    </div>
    <div class="map-legend"><span v-for="(label, kind) in entityTypes" :key="kind"><b :class="kind">{{ glyphs[kind] }}</b>{{ label }}</span></div>
    <p class="muted small">底图为园区示意，不代表真实建筑或导航路线。{{ inventory.length - markers.length }} 个实体暂无有效区域内坐标；详情仍可在清单中查看。</p>
  </section>
</template>
<style scoped>
.dense .map-label{visibility:hidden}.dense .map-marker:hover .map-label,.dense .map-marker:focus-visible .map-label,.dense .map-marker.selected .map-label{visibility:visible}

.map-panel{margin-bottom:24px}.map-panel .panel-heading{flex-wrap:wrap;gap:12px}.map-warning{background:#fff3d8;padding:10px 14px;color:#7a5411;border-radius:8px}.map-scroll{overflow:auto;border:1px solid #d7e4e8;border-radius:12px;max-height:620px}.map-surface{position:relative;aspect-ratio:1000/640;min-width:540px}.map-drawing{display:block;width:100%;height:100%}.map-marker{position:absolute;transform:translate(-50%,-50%);border:0;background:none;display:flex;align-items:center;flex-direction:column;cursor:pointer;z-index:1;padding:2px;transition:left .45s linear,top .45s linear;color:#087f8c}.map-marker:hover,.map-marker:focus-visible,.map-marker.selected{z-index:3}.map-pin{display:grid;place-items:center;border:3px solid white;box-shadow:0 2px 8px #12394430;background:currentColor;border-radius:50%;width:34px;height:34px;font-size:0}.map-pin::first-letter{font-size:18px}.map-pin{font-size:18px;background:#fff;border-color:currentColor;font-weight:700}.map-label{margin-top:3px;white-space:nowrap;background:#fffffff0;padding:3px 6px;border:1px solid #d1e1e5;border-radius:5px;font-size:12px;color:#243e49}.drone{color:#3070cb}.vehicle{color:#b57413}.robot{color:#8059b1}.sensor{color:#147a56}.facility{color:#617582}.map-marker.stale{color:#7d8791}.map-marker.stale .map-pin{border-style:dashed;background:#e6eaec}.map-marker.selected .map-pin{outline:4px solid #17a8b64a}.map-legend{display:flex;flex-wrap:wrap;gap:16px;margin-top:14px;font-size:14px}.map-legend span{display:flex;gap:6px;align-items:center}.map-legend b{font-size:16px}.map-marker:focus-visible{outline:3px solid #005f72;border-radius:6px}@media(prefers-reduced-motion:reduce){.map-marker{transition:none}}
</style>
