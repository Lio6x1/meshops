<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { onBeforeRouteLeave, useRouter } from 'vue-router'
import { api, errorText, query, clockOffset } from '../api'
import { useEntities } from '../entities'
import { canInspect, makeDraft, statuses } from '../domain'
import type { Task } from '../types'
import TaskTable from '../components/TaskTable.vue'
const router = useRouter(),
  tasks = ref<Task[]>([]),
  busy = ref(false),
  error = ref(''),
  status = ref(''),
  entity = ref(''),
  next = ref(''),
  token = ref(''),
  total = ref<number>(),
  show = ref(false),
  submitting = ref(false)
const draftEntity = ref(''),
  duration = ref(5),
  note = ref(''),
  deadline = ref(new Date(Date.now() + 3600000).toISOString()),
  frozen = ref<ReturnType<typeof makeDraft>>(),
  createError = ref('')
const { inventory, state, ready, now, load: loadEntities } = useEntities()
const choices = computed(() =>
  inventory.value.filter((v) => v.executorId && v.supportedTasks?.includes('inspect')),
)
function available(id: string) {
  return canInspect(
    inventory.value.find((v) => v.entityId === id),
    state.value.entities[id],
    ready.value,
    now.value,
  )
}
async function load(reset = false) {
  if (reset) token.value = ''
  busy.value = true
  error.value = ''
  try {
    const data = await api<{ tasks?: Task[]; nextPageToken?: string; totalCount?: number }>(
      '/api/v1/tasks' +
        query({
          target_entity_id: entity.value,
          status: status.value,
          page_size: 20,
          page_token: token.value,
        }),
    )
    tasks.value = data.tasks ?? []
    next.value = data.nextPageToken ?? ''
    total.value = data.totalCount ?? 0
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
function nextPage() {
  token.value = next.value
  void load()
}
function open() {
  show.value = true
  if (!frozen.value) {
    createError.value = ''
    deadline.value = new Date(Date.now() + clockOffset.value + 3600000).toISOString()
  }
}
function discard() {
  if (
    frozen.value &&
    !window.confirm(
      '原请求可能已经创建任务。建议先在任务列表核对。确认放弃该请求的幂等重试信息吗？',
    )
  )
    return
  frozen.value = undefined
  createError.value = ''
  note.value = ''
  draftEntity.value = ''
}
async function create() {
  submitting.value = true
  createError.value = ''
  try {
    // 首次发送时冻结完整请求。超时、网络中断甚至收到错误，都不自动换业务幂等键。
    if (!frozen.value) {
      if (!available(draftEntity.value))
        throw new Error('实体状态未同步、已过期或非空闲，请等待新鲜状态')
      const delta = Date.parse(deadline.value) - (Date.now() + clockOffset.value)
      if (delta <= 0 || delta > 86400000) throw new Error('截止时间须在未来 24 小时内')
      frozen.value = makeDraft(
        {
          entityId: draftEntity.value,
          duration: duration.value,
          note: note.value,
          deadline: deadline.value,
        },
        crypto.randomUUID(),
      )
    }
    const data = await api<{ taskId: string }>('/api/v1/tasks', {
      method: 'POST',
      body: JSON.stringify(frozen.value),
    })
    frozen.value = undefined
    show.value = false
    await router.push('/tasks/' + encodeURIComponent(data.taskId))
  } catch (e) {
    createError.value = errorText(e)
  } finally {
    submitting.value = false
  }
}
function warnUnload(event: BeforeUnloadEvent) {
  if (frozen.value) {
    event.preventDefault()
    event.returnValue = ''
  }
}
onBeforeRouteLeave(
  () =>
    !frozen.value ||
    window.confirm(
      '原创建请求结果尚未确认，离开将丢失其幂等重试信息。请先确认任务是否已创建。仍要离开吗？',
    ),
)
onMounted(() => {
  void load()
  void loadEntities()
  window.addEventListener('beforeunload', warnUnload)
})
const interval = window.setInterval(() => {
  if (!busy.value && !show.value) void load()
}, 5000)
onUnmounted(() => {
  clearInterval(interval)
  window.removeEventListener('beforeunload', warnUnload)
})
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">TASK OPERATIONS</span>
      <h1>任务中心</h1>
      <p>一次业务任务，一个稳定执行键。从创建到结果持续跟踪。</p>
    </div>
    <el-button type="primary" size="large" @click="open">＋ 创建巡检任务</el-button>
  </section>
  <section class="panel">
    <div class="filters">
      <el-input v-model="entity" aria-label="目标实体 ID" placeholder="目标实体 ID" clearable />
      <el-select v-model="status" placeholder="全部状态" aria-label="任务状态" clearable>
        <el-option v-for="(label, key) in statuses" :key="key" :value="key" :label="label" />
      </el-select>
      <el-button type="primary" @click="load(true)">查询</el-button>
      <el-button @click="load()">刷新</el-button>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" />
    <TaskTable :tasks="tasks" :busy="busy" />
    <div class="pagination">
      <span>本次查询共 {{ total ?? '—' }} 条 · 每 5 秒刷新</span>
      <div>
        <el-button :disabled="!token" @click="load(true)">回到第一页</el-button>
        <el-button :disabled="!next || busy" @click="nextPage">下一页</el-button>
      </div>
    </div>
  </section>
  <el-dialog
    v-model="show"
    title="创建巡检任务"
    width="min(600px, 94vw)"
    :close-on-click-modal="false"
  >
    <p class="muted">系统按可信绑定选择执行器。优先级固定为 5，仅支持 inspect。</p>
    <el-alert v-if="createError" :title="createError" type="error" :closable="false" />
    <el-alert
      v-if="frozen"
      title="请求已固定：结果不确定时，请重试原请求。不要另建任务，以免重复业务操作。"
      type="warning"
      :closable="false"
    />
    <el-form label-position="top" :disabled="!!frozen || submitting">
      <el-form-item label="执行实体">
        <el-select
          v-model="draftEntity"
          placeholder="选择已同步、空闲且未过期的实体"
          class="full-width"
        >
          <el-option
            v-for="item in choices"
            :key="item.entityId"
            :value="item.entityId"
            :label="item.entityId + (available(item.entityId) ? ' · 可执行' : ' · 暂不可用')"
            :disabled="!available(item.entityId)"
          />
        </el-select>
      </el-form-item>
      <el-form-item label="模拟执行时长（1–60 秒）">
        <el-input-number v-model="duration" :min="1" :max="60" :precision="0" />
      </el-form-item>
      <el-form-item label="备注（最多 256 UTF-8 字节）">
        <el-input v-model="note" type="textarea" :rows="3" />
      </el-form-item>
      <el-form-item label="截止时间（未来 24 小时内）">
        <el-date-picker
          v-model="deadline"
          type="datetime"
          value-format="YYYY-MM-DDTHH:mm:ss.SSSZ"
          :clearable="false"
        />
      </el-form-item>
    </el-form>
    <p v-if="frozen" class="small break">请求幂等键：{{ frozen.idempotencyKey }}</p>
    <template #footer>
      <el-button v-if="frozen" :disabled="submitting" @click="discard">
        明确放弃此请求并重填
      </el-button>
      <el-button @click="show = false">暂时关闭</el-button>
      <el-button type="primary" :loading="submitting" @click="create">
        {{ frozen ? '重试原请求' : '创建任务' }}
      </el-button>
    </template>
  </el-dialog>
</template>
