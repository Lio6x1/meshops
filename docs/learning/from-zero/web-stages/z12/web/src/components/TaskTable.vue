<script setup lang="ts">
import type { Task } from '../types'
import { taskLabel, time } from '../domain'
defineProps<{ tasks: Task[]; busy?: boolean }>()
</script>
<template>
  <el-table :data="tasks" v-loading="busy" empty-text="没有符合条件的任务">
    <el-table-column label="任务 ID" min-width="230">
      <template #default="{ row }">
        <RouterLink class="text-link" :to="'/tasks/' + encodeURIComponent(row.taskId)">
          {{ row.taskId }}
        </RouterLink>
        <div class="small muted">{{ row.taskType || 'inspect' }}</div>
      </template>
    </el-table-column>
    <el-table-column prop="targetEntityId" label="目标实体" min-width="150" />
    <el-table-column label="状态" min-width="190">
      <template #default="{ row }">
        <span class="status-pill" :class="row.status">{{ taskLabel(row) }}</span>
      </template>
    </el-table-column>
    <el-table-column label="创建时间" min-width="180">
      <template #default="{ row }">{{ time(row.createdAt) }}</template>
    </el-table-column>
    <el-table-column label="更新时间" min-width="180">
      <template #default="{ row }">{{ time(row.updatedAt) }}</template>
    </el-table-column>
  </el-table>
</template>
