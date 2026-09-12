<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, errorText } from '../api'
import { canCancel, taskLabel, time, statuses } from '../domain'
import type { Task, TaskChange } from '../types'
const route = useRoute(),
  task = ref<Task>(),
  history = ref<TaskChange[]>([]),
  error = ref(''),
  busy = ref(false),
  cancelOpen = ref(false),
  reason = ref(''),
  writing = ref(false),
  notice = ref('')
const base = '/api/v1/tasks/' + encodeURIComponent(String(route.params.id))
// 状态版本防止较晚返回的旧响应令页面“倒退”；历史来自持久化审计接口。
async function load() {
  busy.value = true
  error.value = ''
  try {
    const result = await api<{ task: Task }>(base)
    if (!task.value || (result.task.statusVersion ?? 0) >= (task.value.statusVersion ?? 0))
      task.value = result.task
    const resultHistory = await api<{ history?: TaskChange[] }>(base + '/history')
    history.value = resultHistory.history ?? []
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function cancel() {
  writing.value = true
  error.value = ''
  try {
    const bytes = new TextEncoder().encode(reason.value).length
    if (!reason.value.trim() || bytes > 1024) throw new Error('取消原因须为 1–1024 UTF-8 字节')
    const response = await api<{ cancelRequested?: boolean; message?: string }>(base + '/cancel', {
      method: 'POST',
      body: JSON.stringify({ reason: reason.value }),
    })
    notice.value = response.cancelRequested
      ? '取消意图已接受，等待执行方确认。'
      : '已返回当前任务状态，请核对。'
    cancelOpen.value = false
    await load()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    writing.value = false
  }
}
onMounted(load)
const interval = window.setInterval(() => {
  if (!busy.value && !writing.value) void load()
}, 2000)
onUnmounted(() => clearInterval(interval))
</script>
<template>
  <section class="page-heading">
    <div>
      <RouterLink class="text-link" to="/tasks">← 返回任务中心</RouterLink>
      <h1>任务详情</h1>
      <p class="mono break">{{ $route.params.id }}</p>
    </div>
    <div class="actions">
      <el-button @click="load" :loading="busy">刷新</el-button>
      <el-button
        v-if="task && canCancel(task) && !task.cancelRequested"
        type="warning"
        @click="cancelOpen = true"
      >
        请求取消
      </el-button>
      <RouterLink :to="{ path: '/dispatch', query: { task: String($route.params.id) } }">
        <el-button>查看分发</el-button>
      </RouterLink>
    </div>
  </section>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-alert v-if="notice" :title="notice" type="info" @close="notice = ''" />
  <el-skeleton v-if="!task && busy" :rows="6" animated />
  <template v-if="task">
    <div class="two-columns">
      <section class="panel">
        <h2>权威业务状态</h2>
        <p class="state-head">{{ taskLabel(task) }}</p>
        <p class="muted">每 2 秒查询任务服务。取消请求不代表执行已停止。</p>
        <dl class="detail-grid">
          <dt>执行实体</dt>
          <dd>{{ task.targetEntityId }}</dd>
          <dt>执行器</dt>
          <dd>{{ task.executorId || '尚未分配' }}</dd>
          <dt>任务类型 / 版本</dt>
          <dd>{{ task.taskType }} / {{ task.statusVersion ?? 0 }}</dd>
          <dt>执行幂等键</dt>
          <dd class="mono">{{ task.executionKey || '尚未生成' }}</dd>
          <dt>创建时间</dt>
          <dd>{{ time(task.createdAt) }}</dd>
          <dt>截止时间</dt>
          <dd>{{ time(task.deadline) }}</dd>
          <dt>更新时间</dt>
          <dd>{{ time(task.updatedAt) }}</dd>
          <dt>取消原因</dt>
          <dd>{{ task.cancelledReason || '无' }}</dd>
          <dt>失败原因</dt>
          <dd>{{ task.failureReason || '无' }}</dd>
        </dl>
        <h3>请求参数</h3>
        <pre>{{ task.payload?.payloadJson || '无' }}</pre>
        <h3>执行结果</h3>
        <pre>{{ task.resultJson || '尚无结果' }}</pre>
      </section>
      <section class="panel">
        <h2>持久化状态记录</h2>
        <el-empty v-if="!history.length" description="暂无状态记录" />
        <el-timeline>
          <el-timeline-item
            v-for="(item, index) in history"
            :key="item.eventId || index"
            :timestamp="time(item.changedAt)"
            placement="top"
          >
            <strong>
              {{ statuses[item.fromStatus ?? ''] || '初始' }} →
              {{ statuses[item.toStatus ?? ''] || item.toStatus }}
            </strong>
            <p>{{ item.reason || '无附加原因' }}</p>
            <small class="muted">{{ item.changedBy }} · 版本 {{ item.statusVersion ?? 0 }}</small>
          </el-timeline-item>
        </el-timeline>
      </section>
    </div>
  </template>
  <el-dialog v-model="cancelOpen" title="提交取消意图" width="min(520px, 94vw)">
    <p>系统会尝试通知执行方停止。最终结果以任务状态为准。</p>
    <el-form label-position="top">
      <el-form-item label="取消原因">
        <el-input v-model="reason" type="textarea" :rows="3" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="cancelOpen = false">返回</el-button>
      <el-button type="warning" :loading="writing" @click="cancel">确认提交</el-button>
    </template>
  </el-dialog>
</template>
