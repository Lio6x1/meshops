<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { api, errorText, query, session } from '../api'
import { statuses, time } from '../domain'
import type { Dispatch } from '../types'
const route = useRoute(),
  taskId = ref(String(route.query.task ?? '')),
  dispatchId = ref(''),
  record = ref<Dispatch>(),
  busy = ref(false),
  error = ref(''),
  reason = ref(''),
  show = ref(false),
  notice = ref('')
async function load() {
  if (!taskId.value.trim()) return
  busy.value = true
  error.value = ''
  record.value = undefined
  try {
    record.value = await api<Dispatch>(
      '/api/v1/tasks/' +
        encodeURIComponent(taskId.value.trim()) +
        '/dispatch' +
        query({ dispatch_id: dispatchId.value.trim() }),
    )
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function retry() {
  busy.value = true
  error.value = ''
  try {
    if (!reason.value.trim() || new TextEncoder().encode(reason.value).length > 1024)
      throw new Error('请填写 1–1024 UTF-8 字节的重试原因')
    const response = await api<{ accepted?: boolean; message?: string }>(
      '/api/v1/tasks/' + encodeURIComponent(record.value?.taskId ?? '') + '/retry',
      { method: 'POST', body: JSON.stringify({ reason: reason.value }) },
    )
    notice.value = response.accepted
      ? '新的分发轮次已持久化，不代表设备已执行成功。'
      : response.message || '未接受重试'
    show.value = false
    await load()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
onMounted(() => {
  if (taskId.value) void load()
})
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">RELIABLE DELIVERY</span>
      <h1>可靠分发</h1>
      <p>业务执行与传输尝试分别追踪，重试保持同一个执行键。</p>
    </div>
  </section>
  <section class="panel">
    <el-form label-position="top" @submit.prevent="load">
      <div class="filters">
        <el-form-item label="任务 ID">
          <el-input v-model="taskId" placeholder="输入已知任务 ID" />
        </el-form-item>
        <el-form-item label="分发 ID（选填）">
          <el-input v-model="dispatchId" placeholder="留空查询最新一次" />
        </el-form-item>
        <el-button native-type="submit" type="primary" :loading="busy" :disabled="!taskId.trim()">
          查询记录
        </el-button>
      </div>
    </el-form>
    <p class="muted small">当前接口返回最新或指定的单条尝试，不提供全部尝试或死信列表。</p>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <el-alert v-if="notice" :title="notice" type="info" @close="notice = ''" />
    <el-empty v-if="!record && !busy" description="查询一条真实分发记录" />
    <template v-if="record">
      <div class="panel-heading">
        <h2>第 {{ record.attempt ?? 0 }} 次尝试</h2>
        <el-tag>{{ record.deliveryStatus || '未知传输状态' }}</el-tag>
      </div>
      <dl class="detail-grid">
        <dt>任务</dt>
        <dd>
          <RouterLink :to="'/tasks/' + encodeURIComponent(record.taskId ?? '')">
            {{ record.taskId }}
          </RouterLink>
        </dd>
        <dt>业务状态</dt>
        <dd>{{ statuses[record.status ?? ''] || record.status }}</dd>
        <dt>分发 ID</dt>
        <dd class="mono">{{ record.dispatchId }}</dd>
        <dt>命令 ID / 类型</dt>
        <dd>{{ record.commandId }} / {{ record.commandKind }}</dd>
        <dt>执行键</dt>
        <dd class="mono">{{ record.executionKey }}</dd>
        <dt>执行器</dt>
        <dd>{{ record.executorId }}</dd>
        <dt>分发时间</dt>
        <dd>{{ time(record.dispatchedAt) }}</dd>
        <dt>确认时间</dt>
        <dd>{{ time(record.ackedAt) }}</dd>
        <dt>完成时间</dt>
        <dd>{{ time(record.completedAt) }}</dd>
      </dl>
      <el-button
        v-if="session?.role === 'admin'"
        type="warning"
        :disabled="record.deliveryStatus !== 'dlq'"
        @click="show = true"
      >
        人工重试最新死信
      </el-button>
      <p class="small muted">
        管理员重试仍由服务端校验最新尝试、业务进度和截止时间。accepted 仅表示接受新的分发轮次。
      </p>
    </template>
  </section>
  <el-dialog v-model="show" title="人工重试死信" width="min(520px, 94vw)">
    <p>确认问题已处理后提交。任务和执行键保持不变。</p>
    <el-form label-position="top">
      <el-form-item label="重试原因">
        <el-input v-model="reason" type="textarea" :rows="3" />
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="show = false">返回</el-button>
      <el-button type="warning" :loading="busy" @click="retry">确认重试</el-button>
    </template>
  </el-dialog>
</template>
