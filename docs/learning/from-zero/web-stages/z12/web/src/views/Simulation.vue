<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { api, errorText } from '../api'
import { entityTypes, time } from '../domain'
import { validSceneCount, sceneIDs } from '../scene'
type Mode = 'running' | 'paused' | 'offline'
interface Source { sourceId: string; entityIds: string[]; entityType: string; desired: Mode; connected: boolean; count: number; observation?: { applied: Mode; activeCount: number; observedAt: string; generated: string; sent: string; pending: string } }
const sources = ref<Source[]>([]), loading = ref(false), busy = ref(false), error = ref(''), feedback = ref('')
const counts = ref<Record<string, number | undefined>>({})
const total = computed(() => sources.value.reduce((sum, s) => sum + s.count, 0))
const actualTotal = computed(() => sources.value.length && sources.value.every((s) => s.connected && Number.isInteger(s.observation?.activeCount)) ? sources.value.reduce((sum, s) => sum + s.observation!.activeCount, 0) : '未知')
const modes: { mode: Mode; label: string; detail: string }[] = [
  { mode: 'running', label: '正常上报', detail: '持续采集并上传，自动补传缓存' },
  { mode: 'paused', label: '暂停采集', detail: '停止采集与上传，保留已有缓存' },
  { mode: 'offline', label: '断网缓存', detail: '继续采集到本地队列，断开上传连接' },
]
const label = (mode?: Mode) => modes.find((v) => v.mode === mode)?.label ?? '未知'
let disposed = false, timer: ReturnType<typeof setTimeout> | undefined
let revision = 0
const controller = new AbortController()
async function refresh() {
  if (disposed) return
  const current = ++revision
  loading.value = true
  try {
    const data = await api<{ sources: Source[] }>('/api/v1/simulation', { signal: controller.signal })
    if (!disposed && current === revision) { sources.value = data.sources; error.value = ''; for (const s of data.sources) if (!(s.sourceId in counts.value)) counts.value[s.sourceId] = s.count }
  } catch (e) { if (!disposed && current === revision) { error.value = errorText(e); sources.value = sources.value.map((s) => ({ ...s, connected: false })) } }
  finally { if (current === revision) loading.value = false }
}
async function poll() {
  if (!busy.value) await refresh()
  if (!disposed) timer = setTimeout(poll, 1500)
}
async function change(selected: Source[], mode: Mode) {
  if (busy.value) return
  busy.value = true; feedback.value = ''
  ++revision
  let submitted = 0
  try {
    // Each source is independent. Do not claim atomic all-source changes or
    // switch applied-state indicators optimistically after an HTTP 202.
    for (const source of selected) {
      await api('/api/v1/simulation/' + encodeURIComponent(source.sourceId), { method: 'PUT', body: JSON.stringify({ mode }), signal: controller.signal })
      submitted++
    }
    feedback.value = `已提交 ${submitted} 个数据源：${label(mode)}。请以各卡片的实际状态为准。`
  } catch (e) { feedback.value = `已提交 ${submitted}/${selected.length} 个数据源；其余未确认：${errorText(e)}` }
  finally { await refresh(); busy.value = false }
}
async function saveCount(source: Source) {
  const count = counts.value[source.sourceId]
  if (busy.value || !validSceneCount(count)) return
  busy.value = true; ++revision; feedback.value = ''
  try {
    await api('/api/v1/simulation/' + encodeURIComponent(source.sourceId) + '/count', { method: 'PUT', body: JSON.stringify({ count }), signal: controller.signal })
    delete counts.value[source.sourceId]
    feedback.value = `已保存${entityTypes[source.entityType]}数量 ${count}。等待模拟器应用；减少数量会保留历史与任务。`
  } catch (e) { feedback.value = `数量保存未确认：${errorText(e)}。刷新后核对目标数量，可用同一值重试。` }
  finally { await refresh(); busy.value = false }
}
onMounted(poll)
onUnmounted(() => { disposed = true; clearTimeout(timer); controller.abort() })
</script>
<template>
  <section class="page-heading"><div><span class="eyebrow">SIMULATION CONTROL</span><h1>模拟演示</h1><p>控制真实模拟数据源，观察地图更新、数据过期与断线补传。移动实体沿加速演示路线运行，固定传感器和设施保持原位。</p></div><RouterLink to="/"><el-button>查看实体地图</el-button></RouterLink></section>
  <el-alert title="这里配置六类模拟实体的数量和上报模式。任务执行器独立运行，不会随这些开关停止。" type="info" :closable="false" />
  <section class="panel scene-summary"><div><h2>混合实体场景</h2><p>每类 0—5 个，共最多 30 个。数量设为 0 会停用该类的新数据生成，历史与已有任务保留。</p></div><div class="scene-total"><strong>{{ total }} / 30</strong><span>目标实体 · 实际启用 {{ actualTotal }}</span></div></section>
  <div class="simulation-toolbar"><el-button type="primary" :disabled="!sources.length || !!error || busy" @click="change(sources, 'running')">全部开始上报</el-button><el-button :disabled="!sources.length || !!error || busy" @click="change(sources, 'paused')">全部暂停采集</el-button><span class="muted small">状态每 1.5 秒刷新；批量操作逐个生效</span></div>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-alert v-if="feedback" :title="feedback" type="info" :closable="false" />
  <el-empty v-if="!sources.length && !loading" description="暂无可控制的模拟器" />
  <p v-if="!sources.length && loading" role="status">正在读取模拟器状态…</p>
  <section class="simulation-grid" aria-label="模拟数据源">
    <article v-for="source in sources" :key="source.sourceId" class="panel simulation-card">
      <div class="panel-heading"><div><h2>{{ entityTypes[source.entityType] ?? source.entityType }}</h2><p>{{ sceneIDs(source.entityIds, source.count).join('、') || '当前类型未启用' }}</p></div><el-tag :type="source.connected ? 'success' : 'warning'">{{ source.connected ? '进程有心跳' : '未收到进程心跳' }}</el-tag></div>
      <div class="scene-count"><label :for="`count-${source.sourceId}`">启用实体数量</label><el-input-number :id="`count-${source.sourceId}`" v-model="counts[source.sourceId]" :min="0" :max="5" :step="1" :precision="0" :disabled="busy || !!error" :aria-label="`${entityTypes[source.entityType]}实体数量`" controls-position="right" /><el-button :disabled="busy || !!error || !validSceneCount(counts[source.sourceId]) || counts[source.sourceId] === source.count" @click="saveCount(source)">应用数量</el-button><small>目标 {{ source.count }} · 实际启用 {{ source.connected ? source.observation?.activeCount ?? '未知' : '未知' }}<span v-if="counts[source.sourceId] !== source.count"> · 修改未应用</span></small></div>
      <div class="simulation-state"><span>实际状态</span><strong>{{ source.connected ? label(source.observation?.applied) : '未知' }}</strong><small>期望状态：{{ label(source.desired) }}{{ source.connected && source.observation?.applied === source.desired ? ' · 已应用' : ' · 等待应用' }}</small></div>
      <div class="simulation-modes"><el-button v-for="option in modes" :key="option.mode" :type="source.desired === option.mode ? 'primary' : 'default'" :plain="source.desired === option.mode" :disabled="busy || !!error" :title="option.detail" @click="change([source], option.mode)">{{ option.label }}</el-button></div>
      <dl class="simulation-stats"><div><dt>本地待补传</dt><dd>{{ source.connected ? source.observation?.pending ?? '—' : '—' }}</dd></div><div><dt>本次进程已采集</dt><dd>{{ source.connected ? source.observation?.generated ?? '—' : '—' }}</dd></div><div><dt>发送条数（含重试）</dt><dd>{{ source.connected ? source.observation?.sent ?? '—' : '—' }}</dd></div></dl>
      <p class="small muted">{{ source.sourceId }} · 心跳 {{ time(source.observation?.observedAt) }}</p>
    </article>
  </section>
  <section class="panel simulation-guide"><h2>试一次断线恢复</h2><ol><li>选择无人机“断网缓存”，观察待补传数量增长。</li><li>打开实体地图：位置停止更新。超过当前配置的 30 秒新鲜度期限后，标记变为灰色。</li><li>切回“正常上报”，等待缓存补传、收到新鲜快照，地图继续更新。</li></ol><p class="muted">每类来源总计约每秒生成 2 条观测，由启用实体轮流上报；不是每个实体每秒 2 条。暂停不会删除缓存，缩减前已缓存的数据仍可补传。数量和期望模式会保留到下次启动；需要恢复演示时点击“全部开始上报”。控制状态不代表 gRPC 上传链路健康，请同时观察待补传数量和实体新鲜度。</p></section>
</template>
<style scoped>
.scene-summary{display:flex;justify-content:space-between;gap:20px;margin-top:20px;flex-wrap:wrap}.scene-summary p{max-width:560px;color:#637780;line-height:1.7}.scene-total{display:flex;flex-direction:column;gap:8px;justify-content:center}.scene-total strong{font-size:30px;color:#13596b}.scene-total span{font-size:14px;color:#637780}.scene-count{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:16px 0;border-bottom:1px solid #e1e9ed}.scene-count label{font-size:14px}.scene-count small{flex-basis:100%;font-size:14px;color:#637780}.scene-count .el-input-number{width:110px}

.simulation-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:10px;margin:20px 0}.simulation-toolbar .el-button+.el-button{margin-left:0}.simulation-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px;margin:20px 0}.simulation-card .panel-heading{align-items:start;gap:10px;flex-wrap:wrap}.simulation-state{display:grid;grid-template-columns:1fr auto;gap:8px;margin:20px 0}.simulation-state span{color:#607780;font-size:14px}.simulation-state strong{font-size:20px;color:#13596b}.simulation-state small{grid-column:1/-1;color:#637780;font-size:14px}.simulation-modes{display:flex;flex-wrap:wrap;gap:8px}.simulation-modes .el-button+.el-button{margin:0}.simulation-stats{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;border-top:1px solid #e1e9ed;margin-top:24px;padding-top:18px}.simulation-stats dt{font-size:13px;color:#637780}.simulation-stats dd{margin:8px 0 0;font-size:24px;font-variant-numeric:tabular-nums}.simulation-guide li{margin-bottom:12px;line-height:1.7}@media(max-width:1000px){.simulation-grid{grid-template-columns:1fr}}
</style>
