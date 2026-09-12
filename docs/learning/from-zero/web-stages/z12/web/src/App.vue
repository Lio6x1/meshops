<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { session, restoreSession, login, logout, errorText } from './api'
const initializing = ref(true),
  busy = ref(false),
  error = ref(''),
  role = ref('operator'),
  accessCode = ref('')
const nav = [
  ['/', '◈', '实体总览'],
  ['/entities', '▦', '实体资源'],
  ['/simulation', '▷', '模拟演示'],
  ['/tasks', '◎', '任务中心'],
  ['/search', '⌕', '任务检索'],
  ['/dispatch', '⇄', '可靠分发'],
  ['/status', '◷', '运行状态'],
]
onMounted(async () => {
  try {
    await restoreSession()
  } catch {
  } finally {
    initializing.value = false
  }
})
async function signIn() {
  busy.value = true
  error.value = ''
  try {
    await login(role.value, accessCode.value)
    accessCode.value = ''
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = false
  }
}
async function signOut() {
  try {
    await logout()
  } catch (e) {
    error.value = errorText(e)
  }
}
</script>
<template>
  <a class="skip-link" href="#content">跳到主要内容</a>
  <div v-if="initializing" class="loading-screen" role="status">正在检查会话…</div>
  <div v-else-if="!session" class="login-screen">
    <section class="login-story">
      <div class="brand">
        <b class="brand-mark">M</b>
        <strong>MeshOps</strong>
      </div>
      <span class="eyebrow">ENTITY OPERATIONS</span>
      <h1>
        每一个实体，
        <br />
        每一次可靠协同。
      </h1>
      <p>
        连接人员、无人机与园区设备，
        <br />
        从实时状态到任务结果，在一个地方看清。
      </p>
      <div class="login-types">人员 · 无人机 · 车辆 · 机器人 · 传感器 · 设施</div>
    </section>
    <section class="login-card">
      <span class="eyebrow">欢迎使用</span>
      <h2>进入协同控制台</h2>
      <p class="muted">本系统使用模拟数据源和执行方，业务结果来自真实后端。</p>
      <el-alert v-if="error" :title="error" type="error" :closable="false" />
      <el-form label-position="top" @submit.prevent="signIn">
        <el-form-item label="访问身份">
          <el-radio-group v-model="role">
            <el-radio-button value="operator">操作员</el-radio-button>
            <el-radio-button value="admin">管理员</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="演示访问码">
          <el-input
            v-model="accessCode"
            type="password"
            show-password
            autocomplete="off"
            placeholder="输入启动手册提供的随机访问码"
          />
        </el-form-item>
        <el-button
          native-type="submit"
          type="primary"
          size="large"
          :loading="busy"
          :disabled="!accessCode"
          class="full-width"
        >
          进入工作台
        </el-button>
      </el-form>
      <p class="small muted">访问码只用于本次登录，不保存在浏览器存储中。</p>
    </section>
  </div>
  <div v-else class="shell">
    <aside class="sidebar">
      <RouterLink to="/" class="brand">
        <b class="brand-mark">M</b>
        <strong>
          MeshOps
          <small>实体协同控制台</small>
        </strong>
      </RouterLink>
      <div class="nav-label">工作空间</div>
      <nav aria-label="主要导航">
        <RouterLink
          v-for="[path, icon, label] in nav"
          :key="path"
          :to="path"
          :class="{ active: path === '/' ? $route.path === '/' : $route.path.startsWith(path!) }"
        >
          <span class="nav-icon">{{ icon }}</span>
          <span>{{ label }}</span>
        </RouterLink>
      </nav>
      <div class="sidebar-foot">
        <span class="dot"></span>
        模拟场景 · 真实业务链路
        <p>单机演示环境</p>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <span>
          园区综合协同 /
          <b>{{ nav.find((n) => n[0] === $route.path)?.[2] ?? '任务详情' }}</b>
        </span>
        <div class="identity">
          <el-tag effect="plain">{{ session.role === 'admin' ? '管理员' : '操作员' }}</el-tag>
          <span>{{ session.actorId }}</span>
          <el-button text @click="signOut">退出</el-button>
        </div>
      </header>
      <main id="content" tabindex="-1">
        <el-alert v-if="error" :title="error" type="error" @close="error = ''" />
        <RouterView :key="$route.path" />
      </main>
      <footer>
        MeshOps · 通用实体与可靠任务协同
        <span>模拟接入与执行 · 以权威任务状态为准</span>
      </footer>
    </div>
  </div>
</template>
