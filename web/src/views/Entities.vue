<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useEntities } from '../entities'
import { api, errorText, query } from '../api'
import { entityTypes, isExpired, time } from '../domain'
import type { HistorySample, Inventory } from '../types'
import SnapshotDetails from '../components/SnapshotDetails.vue'
import { LatestRequest } from '../requests'
defineProps<{ overview: boolean }>()
const { inventory, state, connection, error, loading, now, ready, load, connect } = useEntities()
const filter = ref(''),
  type = ref(''),
  selected = ref<Inventory>(),
  drawer = ref(false),
  samples = ref<HistorySample[]>([]),
  historyError = ref(''),
  historyBusy = ref(false),
  next = ref('')
const range = ref<[string, string]>([
  new Date(Date.now() - 3600000).toISOString(),
  new Date().toISOString(),
])
const rows = computed(() =>
  inventory.value.filter(
    (v) => (!type.value || v.entityType === type.value) && v.entityId.includes(filter.value),
  ),
)
const fresh = computed(
  () =>
    inventory.value.filter(
      (v) => !isExpired(state.value.entities[v.entityId]?.expiresAt, now.value),
    ).length,
)
const capable = computed(
  () => inventory.value.filter((v) => v.supportedTasks?.includes('inspect')).length,
)
const current = computed(() =>
  selected.value ? state.value.entities[selected.value.entityId] : undefined,
)
onMounted(load)
const historyRequests = new LatestRequest()
function resetHistory() {
  historyRequests.invalidate()
  historyBusy.value = false
  samples.value = []
  next.value = ''
  historyError.value = ''
}
watch(range, resetHistory)
onUnmounted(() => historyRequests.invalidate())
function detail(item: Inventory) {
  resetHistory()
  selected.value = item
  drawer.value = true
  range.value = [new Date(now.value - 3600000).toISOString(), new Date(now.value).toISOString()]
}
async function history(more = false) {
  if (!selected.value) return
  historyBusy.value = true
  historyError.value = ''
  const path =
    '/api/v1/entities/' +
    encodeURIComponent(selected.value.entityId) +
    '/history' +
    query({
      start_time: range.value[0],
      end_time: range.value[1],
      page_size: 50,
      page_token: more ? next.value : '',
    })
  const previous = more ? [...samples.value] : []
  await historyRequests.run(
    () => api<{ samples?: HistorySample[]; nextPageToken?: string }>(path),
    (data) => {
      samples.value = [...previous, ...(data.samples ?? [])]
      next.value = data.nextPageToken ?? ''
    },
    (e) => {
      historyError.value = errorText(e)
    },
    () => {
      historyBusy.value = false
    },
  )
}
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">LIVE ENTITY NETWORK</span>
      <h1>{{ overview ? '实体总览' : '实体资源' }}</h1>
      <p>让异构实体在同一视图中，保留各自的状态与能力。</p>
    </div>
    <div class="actions">
      <el-tag :type="ready ? 'success' : 'warning'">
        {{ connection }} · {{ state.ready ? '同步完成' : '等待快照结束' }}
      </el-tag>
      <el-button @click="connect">重新同步</el-button>
    </div>
  </section>
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <section v-if="overview" class="kpis">
    <article>
      <span>授权实体</span>
      <strong>{{ loading ? '—' : inventory.length }}</strong>
      <small>可信注册清单</small>
    </article>
    <article>
      <span>新鲜快照</span>
      <strong>{{ state.ready ? fresh : '—' }}</strong>
      <small>以服务端过期时间判断</small>
    </article>
    <article>
      <span>待关注状态</span>
      <strong>{{ state.ready ? inventory.length - fresh : '—' }}</strong>
      <small>过期或尚无快照</small>
    </article>
    <article>
      <span>巡检执行实体</span>
      <strong>{{ loading ? '—' : capable }}</strong>
      <small>以注册能力为准</small>
    </article>
  </section>
  <section v-if="overview" class="entity-strip">
    <button v-for="(label, key) in entityTypes" :key="key" @click="type = type === key ? '' : key">
      <span>{{ label }}</span>
      <b>{{ inventory.filter((v) => v.entityType === key).length }}</b>
      <small>{{ ['sensor', 'facility'].includes(key) ? '状态感知' : '可授权执行' }}</small>
    </button>
  </section>
  <section class="panel">
    <div class="panel-heading">
      <div>
        <h2>实体清单</h2>
        <p>连接中断会保留最后快照，不代表实体离线。最多订阅当前授权清单前 100 项。</p>
      </div>
    </div>
    <div class="filters">
      <el-input v-model="filter" placeholder="搜索实体 ID" aria-label="搜索实体 ID" clearable />
      <el-select v-model="type" placeholder="全部类型" aria-label="实体类型" clearable>
        <el-option v-for="(label, key) in entityTypes" :key="key" :label="label" :value="key" />
      </el-select>
      <el-button :loading="loading" @click="load">刷新清单</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" empty-text="没有符合条件的实体">
      <el-table-column label="实体" min-width="190">
        <template #default="{ row }">
          <button class="text-link" @click="detail(row)">{{ row.entityId }}</button>
          <div class="muted small">
            {{ entityTypes[row.entityType] ?? row.entityType }}
          </div>
        </template>
      </el-table-column>
      <el-table-column label="业务状态" min-width="140">
        <template #default="{ row }">
          {{ state.entities[row.entityId]?.snapshot?.status || '未知' }}
        </template>
      </el-table-column>
      <el-table-column label="数据新鲜度" width="150">
        <template #default="{ row }">
          <el-tag
            :type="isExpired(state.entities[row.entityId]?.expiresAt, now) ? 'warning' : 'success'"
          >
            {{
              !state.entities[row.entityId]
                ? '暂无快照'
                : isExpired(state.entities[row.entityId]?.expiresAt, now)
                  ? '已过期'
                  : '新鲜'
            }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="最后更新" min-width="190">
        <template #default="{ row }">{{ time(state.entities[row.entityId]?.updatedAt) }}</template>
      </el-table-column>
      <el-table-column label="能力" min-width="130">
        <template #default="{ row }">{{ row.supportedTasks?.join('、') || '仅状态' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button link type="primary" @click="detail(row)">查看详情</el-button>
        </template>
      </el-table-column>
    </el-table>
  </section>
  <el-drawer v-model="drawer" :title="selected?.entityId ?? '实体详情'" size="min(760px, 100%)">
    <el-alert
      v-if="!ready"
      title="订阅尚未同步，以下可能是旧快照"
      type="warning"
      :closable="false"
    />
    <SnapshotDetails :snapshot="current?.snapshot" />
    <h3>来源与版本</h3>
    <dl class="detail-grid">
      <dt>来源</dt>
      <dd>{{ current?.sourceId || '未知' }}</dd>
      <dt>来源代次 / 版本</dt>
      <dd>{{ current?.sourceGeneration ?? '未知' }} / {{ current?.version ?? '未知' }}</dd>
      <dt>过期时间</dt>
      <dd>{{ time(current?.expiresAt) }}</dd>
      <dt>执行器绑定</dt>
      <dd>{{ selected?.executorId || '无' }}</dd>
      <dt>视图代次</dt>
      <dd>{{ current?.viewGeneration || '未知' }}</dd>
    </dl>
    <h3>历史采样</h3>
    <p class="muted">有界采样记录，不是完整轨迹。选择时间范围后查询。</p>
    <el-date-picker
      v-model="range"
      type="datetimerange"
      value-format="YYYY-MM-DDTHH:mm:ss.SSSZ"
      start-placeholder="开始时间"
      end-placeholder="结束时间"
      :clearable="false"
    />
    <div class="actions spaced">
      <el-button :loading="historyBusy" @click="history()">查询历史</el-button>
      <el-button :disabled="!next" :loading="historyBusy" @click="history(true)">
        加载下一页
      </el-button>
    </div>
    <el-alert v-if="historyError" :title="historyError" type="error" :closable="false" />
    <el-empty v-if="!samples.length && !historyBusy" description="尚无历史结果" />
    <el-collapse>
      <el-collapse-item
        v-for="sample in samples"
        :key="sample.sampleId"
        :title="time(sample.sampledAt) + ' · ' + (sample.sampleReason || '样本')"
      >
        <p>发生于 {{ time(sample.occurredAt) }}</p>
        <SnapshotDetails :snapshot="sample.snapshot" />
      </el-collapse-item>
    </el-collapse>
  </el-drawer>
</template>
