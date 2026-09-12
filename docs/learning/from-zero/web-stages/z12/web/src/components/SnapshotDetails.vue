<script setup lang="ts">
import type { Snapshot } from '../types'
import { display, time } from '../domain'
defineProps<{ snapshot?: Snapshot }>()
</script>
<template>
  <el-empty v-if="!snapshot" description="暂无实体快照" />
  <dl v-else class="detail-grid">
    <dt>业务状态</dt>
    <dd>{{ snapshot.status || '未知' }}</dd>
    <template v-if="snapshot.location">
      <dt>经纬度</dt>
      <dd>{{ snapshot.location.longitude ?? 0 }}, {{ snapshot.location.latitude ?? 0 }}</dd>
      <dt>海拔 / 精度</dt>
      <dd>
        {{ display(snapshot.location.altitude) }} m / {{ display(snapshot.location.accuracy) }} m
      </dd>
    </template>
    <template v-if="snapshot.velocity">
      <dt>速度</dt>
      <dd>{{ snapshot.velocity.speed ?? 0 }} m/s</dd>
      <dt>航向 / 垂直速度</dt>
      <dd>
        {{ display(snapshot.velocity.heading) }} ° /
        {{ display(snapshot.velocity.verticalSpeed) }} m/s
      </dd>
    </template>
    <template v-if="snapshot.power">
      <dt>电量</dt>
      <dd>{{ display(snapshot.power.batteryPercent) }} %</dd>
    </template>
    <template v-if="snapshot.person">
      <dt>在岗 / 可用性</dt>
      <dd>
        {{ display(snapshot.person.onDuty ?? false) }} /
        {{ snapshot.person.availability || '未知' }}
      </dd>
      <dt>技能</dt>
      <dd>{{ snapshot.person.skills?.join('、') || '无' }}</dd>
    </template>
    <template v-if="snapshot.vehicle">
      <dt>载重 / 可用性</dt>
      <dd>
        {{ display(snapshot.vehicle.loadKg) }} kg / {{ snapshot.vehicle.availability || '未知' }}
      </dd>
    </template>
    <template v-if="snapshot.robot">
      <dt>故障码 / 可用性</dt>
      <dd>
        {{ snapshot.robot.faultCodes?.join('、') || '无' }} /
        {{ snapshot.robot.availability || '未知' }}
      </dd>
    </template>
    <template v-if="snapshot.sensor">
      <dt>传感读数</dt>
      <dd>{{ display(snapshot.sensor.reading) }} {{ snapshot.sensor.unit }}</dd>
      <dt>健康 / 测量时间</dt>
      <dd>{{ snapshot.sensor.health || '未知' }} / {{ time(snapshot.sensor.measuredAt) }}</dd>
    </template>
    <template v-if="snapshot.facility">
      <dt>设施类型 / 开放</dt>
      <dd>
        {{ snapshot.facility.facilityType || '未知' }} /
        {{ display(snapshot.facility.isOpen ?? false) }}
      </dd>
      <dt>占用 / 总容量</dt>
      <dd>{{ snapshot.facility.occupiedSlots ?? 0 }} / {{ snapshot.facility.totalSlots ?? 0 }}</dd>
    </template>
  </dl>
</template>
