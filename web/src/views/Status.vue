<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { api, errorText, session } from '../api'
import { time } from '../domain'
const status = ref<{
    consumerLag?: string
    activeTasks?: number
    dlqCount?: string
    asOf?: string
  }>(),
  busy = ref(false),
  error = ref('')
// 读取失败保留上次成功采样并提示过期，不把错误显示成“积压为零”。
async function load() {
  if (session.value?.role !== 'admin') return
  busy.value = true
  error.value = ''
  try {
    status.value = await api('/api/v1/dispatcher/status')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
onMounted(load)
const timer = window.setInterval(() => {
  if (!busy.value) void load()
}, 5000)
onUnmounted(() => clearInterval(timer))
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">RUNTIME OBSERVATION</span>
      <h1>运行状态</h1>
      <p>分发器的当前采样值，帮助定位等待与分发积压。</p>
    </div>
    <el-button v-if="session?.role === 'admin'" :loading="busy" @click="load">刷新状态</el-button>
  </section>
  <el-result
    v-if="session?.role !== 'admin'"
    icon="info"
    title="此页面需要管理员身份"
    sub-title="操作员可查看实体、任务和单条分发记录。运行状态需要使用管理员账号登录。"
  />
  <template v-else>
    <el-alert
      v-if="error"
      :title="error + '；保留上次成功采样，数据可能已过期。'"
      type="warning"
      :closable="false"
    />
    <section class="kpis three">
      <article>
        <span>消费积压</span>
        <strong>{{ status ? (status.consumerLag ?? '0') : '—' }}</strong>
        <small>consumer_lag</small>
      </article>
      <article>
        <span>活跃任务</span>
        <strong>{{ status ? (status.activeTasks ?? 0) : '—' }}</strong>
        <small>active_tasks</small>
      </article>
      <article>
        <span>死信记录</span>
        <strong>{{ status ? (status.dlqCount ?? '0') : '—' }}</strong>
        <small>dlq_count</small>
      </article>
    </section>
    <section class="panel">
      <h2>采样说明</h2>
      <p>
        后端采样时间：
        <b>{{ time(status?.asOf) }}</b>
        · 页面每 5 秒查询
      </p>
      <p class="muted">
        这里只展示 GetStatus
        已提供的三项指标，不推导整体系统健康，也不生成历史趋势。消费积压和死信数量的 64
        位值保持原始精度。
      </p>
      <RouterLink to="/dispatch"><el-button>查询具体任务的分发记录 →</el-button></RouterLink>
    </section>
  </template>
</template>
