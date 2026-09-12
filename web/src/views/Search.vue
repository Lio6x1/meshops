<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { api, errorText, query } from '../api'
import { statuses, searchExpired } from '../domain'
import type { Task } from '../types'
import TaskTable from '../components/TaskTable.vue'
import { LatestRequest } from '../requests'
const keyword = ref(''),
  status = ref(''),
  entity = ref(''),
  from = ref(''),
  before = ref(''),
  tasks = ref<Task[]>([]),
  busy = ref(false),
  error = ref(''),
  expired = ref(false),
  searched = ref(false),
  next = ref(''),
  page = ref(1)
const requests = new LatestRequest()
watch([keyword, status, entity, from, before], () => {
  requests.invalidate()
  busy.value = false
  next.value = ''
  page.value = 1
  searched.value = false
  tasks.value = []
  expired.value = false
  error.value = ''
})
onUnmounted(() => requests.invalidate())
async function search(more = false) {
  busy.value = true
  error.value = ''
  expired.value = false
  // Freeze both filters and the opaque cursor together before sending.
  const path =
    '/api/v1/search/tasks' +
    query({
      keyword: keyword.value,
      status: status.value,
      target_entity_id: entity.value,
      created_from: from.value,
      created_before: before.value,
      page_size: 20,
      page_token: more ? next.value : '',
    })
  const requestedPage = more ? page.value + 1 : 1
  await requests.run(
    () => api<{ tasks?: Task[]; nextPageToken?: string }>(path),
    (result) => {
      tasks.value = result.tasks ?? []
      next.value = result.nextPageToken ?? ''
      page.value = requestedPage
      searched.value = true
    },
    (e) => {
      expired.value = searchExpired(e)
      error.value = errorText(e)
      if (expired.value) next.value = ''
    },
    () => {
      busy.value = false
    },
  )
}
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">TASK SEARCH</span>
      <h1>任务检索</h1>
      <p>从备注、取消和失败原因中寻找线索，再打开权威详情进行操作。</p>
    </div>
    <el-tag effect="plain" type="info">最终一致搜索投影</el-tag>
  </section>
  <section class="panel">
    <el-form label-position="top" @submit.prevent="search()">
      <div class="search-grid">
        <el-form-item label="关键词">
          <el-input v-model="keyword" placeholder="例如：巡检、设备异常" clearable />
        </el-form-item>
        <el-form-item label="任务状态">
          <el-select v-model="status" clearable placeholder="全部状态">
            <el-option v-for="(label, key) in statuses" :key="key" :label="label" :value="key" />
          </el-select>
        </el-form-item>
        <el-form-item label="目标实体">
          <el-input v-model="entity" placeholder="实体 ID" clearable />
        </el-form-item>
        <el-form-item label="创建时间起（含）">
          <el-date-picker v-model="from" type="datetime" value-format="YYYY-MM-DDTHH:mm:ss.SSSZ" />
        </el-form-item>
        <el-form-item label="创建时间止（不含）">
          <el-date-picker
            v-model="before"
            type="datetime"
            value-format="YYYY-MM-DDTHH:mm:ss.SSSZ"
          />
        </el-form-item>
      </div>
      <el-button native-type="submit" type="primary" :loading="busy">搜索任务</el-button>
    </el-form>
    <p class="muted small">
      新任务需要经过 MySQL → Canal → Kafka → Elasticsearch
      后才可检索；搜索结果暂时落后时，请以任务详情为准。
    </p>
  </section>
  <section class="panel">
    <el-alert
      v-if="error"
      :title="expired ? '搜索快照已过期，请保留当前条件重新搜索。' : error"
      type="warning"
      :closable="false"
    />
    <el-button v-if="expired" class="spaced" @click="search()">从第一页重新查询</el-button>
    <el-empty v-if="!searched && !busy" description="输入条件并搜索；留空可浏览可访问的任务" />
    <TaskTable v-else :tasks="tasks" :busy="busy" />
    <div v-if="searched" class="pagination">
      <span>第 {{ page }} 页 · 本页 {{ tasks.length }} 条 · 不提供总命中数</span>
      <div>
        <el-button :disabled="page === 1 || busy" @click="search()">回到第一页</el-button>
        <el-button :disabled="!next || busy" @click="search(true)">下一页</el-button>
      </div>
    </div>
    <ul
      v-if="tasks.some((t) => t.note || t.failureReason || t.cancelledReason)"
      class="search-clues"
    >
      <li
        v-for="t in tasks.filter((t) => t.note || t.failureReason || t.cancelledReason)"
        :key="t.taskId"
      >
        <RouterLink :to="'/tasks/' + encodeURIComponent(t.taskId ?? '')">{{ t.taskId }}</RouterLink>
        — {{ t.note || t.failureReason || t.cancelledReason }}
      </li>
    </ul>
  </section>
</template>
