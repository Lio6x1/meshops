<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { createAccount, errorText, listAccounts, resetAccountPassword, session, setAccountEnabled } from '../api'
import { canManageAccount, canUseBusiness, passwordError, usernameError } from '../accounts'
import { time } from '../domain'
import { LatestRequest } from '../requests'
import type { Account } from '../types'

const allowed = computed(() => canUseBusiness(session.value) && session.value?.role === 'admin')
const accounts = ref<Account[]>([]), busy = ref(false), error = ref(''), notice = ref('')
const next = ref(''), cursor = ref(''), page = ref(1), loaded = ref(false)
const requests = new LatestRequest()
const dialog = ref(false), saving = ref(false), formError = ref('')
const mode = ref<'create' | 'reset' | 'toggle'>('create'), target = ref<Account | null>(null)
const form = reactive({ username: '', displayName: '', password: '', confirmation: '' })
const dialogTitle = computed(() => mode.value === 'create' ? '创建操作员' : mode.value === 'reset' ? '重置操作员密码' : target.value?.enabled ? '禁用操作员' : '启用操作员')

onMounted(() => load())
onUnmounted(() => {
  requests.invalidate()
  clearForm()
})
async function load(after = '', requestedPage = 1) {
  if (!allowed.value) return
  busy.value = true
  error.value = ''
  await requests.run(
    () => listAccounts(after),
    result => {
      accounts.value = result.accounts ?? []
      next.value = result.nextCursor ?? ''
      cursor.value = after
      page.value = requestedPage
      loaded.value = true
    },
    e => { error.value = errorText(e) },
    () => { busy.value = false },
  )
}
function clearForm() {
  form.username = ''
  form.displayName = ''
  form.password = ''
  form.confirmation = ''
  formError.value = ''
  target.value = null
}
function open(kind: 'create' | 'reset' | 'toggle', account?: Account) {
  if (!allowed.value || busy.value || saving.value) return
  if (account && !canManageAccount(session.value, account)) return
  clearForm()
  mode.value = kind
  target.value = account ? { ...account } : null
  dialog.value = true
}
function close(done: () => void) {
  if (!saving.value) done()
}
async function save() {
  if (saving.value || !allowed.value) return
  const account = target.value
  if (mode.value !== 'create' && (!account || !canManageAccount(session.value, account))) return
  formError.value = ''
  if (mode.value === 'create') {
    formError.value = usernameError(form.username)
    if (!formError.value && !form.displayName.trim()) formError.value = '请输入显示名称'
  }
  if (!formError.value && mode.value !== 'toggle') {
    formError.value = passwordError(form.password)
    if (!formError.value && form.password !== form.confirmation) formError.value = '两次输入的密码不一致'
  }
  if (formError.value) return
  saving.value = true
  notice.value = ''
  try {
    if (mode.value === 'create') {
      await createAccount({ username: form.username, displayName: form.displayName.trim(), password: form.password })
      notice.value = `操作员 ${form.username} 已创建，首次登录须修改密码。`
    } else if (mode.value === 'reset' && account) {
      await resetAccountPassword(account.id, form.password)
      notice.value = `${account.username} 的密码已重置，已有会话失效，下次登录须修改密码。`
    } else if (account) {
      await setAccountEnabled(account.id, !account.enabled)
      notice.value = `${account.username} 已${account.enabled ? '禁用，已有会话失效' : '启用'}。`
    }
    const created = mode.value === 'create'
    dialog.value = false
    await load(created ? '' : cursor.value, created ? 1 : page.value)
  } catch (e) {
    formError.value = errorText(e)
  } finally {
    // 密码仅存在于当前输入和请求中，提交结束即清除。
    form.password = ''
    form.confirmation = ''
    saving.value = false
  }
}
</script>
<template>
  <template v-if="allowed">
    <section class="page-heading">
      <div>
        <span class="eyebrow">ACCOUNT MANAGEMENT</span>
        <h1>账号管理</h1>
        <p>创建操作员并管理访问权限；初始管理员通过部署命令配置。</p>
      </div>
      <el-button type="primary" :disabled="busy || saving" @click="open('create')">创建操作员</el-button>
    </section>
    <section class="panel">
      <div class="panel-heading">
        <div><h2>个人账号</h2><p>账号按稳定 ID 分页，初始管理员不提供禁用或重置操作。</p></div>
        <el-button :loading="busy" :disabled="saving" @click="load(cursor, page)">刷新</el-button>
      </div>
      <el-alert v-if="notice" :title="notice" type="success" @close="notice = ''" role="status" />
      <el-alert v-if="error" :title="error" type="error" :closable="false" role="alert" />
      <el-button v-if="error" class="spaced" :disabled="busy || saving" @click="load(cursor, page)">重试</el-button>
      <el-table v-loading="busy" :data="accounts" row-key="id" :empty-text="busy ? '正在加载账号…' : error ? '账号加载失败，请重试' : '暂无账号'" aria-label="账号列表">
        <el-table-column prop="username" label="用户名" min-width="150" />
        <el-table-column prop="displayName" label="显示名称" min-width="130" />
        <el-table-column label="身份" width="100"><template #default="{ row }">{{ row.role === 'admin' ? '管理员' : '操作员' }}</template></el-table-column>
        <el-table-column label="账号状态" width="105"><template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'" effect="plain">{{ row.enabled ? '已启用' : '已禁用' }}</el-tag></template></el-table-column>
        <el-table-column label="密码状态" width="120"><template #default="{ row }"><el-tag v-if="row.mustChangePassword" type="warning" effect="plain">待修改密码</el-tag><span v-else class="muted">已设置</span></template></el-table-column>
        <el-table-column label="创建时间" min-width="175"><template #default="{ row }">{{ time(row.createdAt) }}</template></el-table-column>
        <el-table-column label="操作" min-width="185" fixed="right">
          <template #default="{ row }">
            <div v-if="canManageAccount(session, row)" class="actions">
              <el-button text type="primary" :disabled="busy || saving" @click="open('toggle', row)">{{ row.enabled ? '禁用' : '启用' }}</el-button>
              <el-button text type="primary" :disabled="busy || saving" @click="open('reset', row)">重置密码</el-button>
            </div>
            <span v-else class="small muted">初始管理员</span>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="loaded" class="pagination">
        <span>第 {{ page }} 页 · 本页 {{ accounts.length }} 个账号</span>
        <div><el-button :disabled="page === 1 || busy || saving" @click="load()">回到第一页</el-button><el-button :disabled="!next || busy || saving" @click="load(next, page + 1)">下一页</el-button></div>
      </div>
    </section>
    <el-dialog v-model="dialog" :title="dialogTitle" width="min(520px, 94vw)" :before-close="close" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" destroy-on-close @closed="clearForm">
      <el-alert v-if="formError" :title="formError" type="error" :closable="false" role="alert" />
      <p v-if="target">目标账号：<b>{{ target.displayName }}</b>（{{ target.username }}）</p>
      <el-form label-position="top" @submit.prevent="save">
        <template v-if="mode === 'create'">
          <el-form-item label="用户名" label-for="account-username"><el-input id="account-username" v-model="form.username" autocomplete="off" :disabled="saving" placeholder="3–32 位小写字母、数字、_ 或 -" /></el-form-item>
          <el-form-item label="显示名称" label-for="account-display-name"><el-input id="account-display-name" v-model="form.displayName" :disabled="saving" maxlength="128" placeholder="例如：园区值班员" /></el-form-item>
        </template>
        <template v-if="mode !== 'toggle'">
          <p class="muted small">请为操作员设置初始密码，并通过可信渠道告知本人。操作员登录后必须修改密码。{{ mode === 'reset' ? '重置会使该账号的已有会话失效。' : '' }}</p>
          <el-form-item label="初始密码" label-for="account-initial-password"><el-input id="account-initial-password" v-model="form.password" type="password" show-password autocomplete="off" :disabled="saving" placeholder="8–128 个字符" /></el-form-item>
          <el-form-item label="确认初始密码" label-for="account-confirm-password"><el-input id="account-confirm-password" v-model="form.confirmation" type="password" show-password autocomplete="off" :disabled="saving" /></el-form-item>
        </template>
        <p v-else>{{ target?.enabled ? '禁用后，该操作员将立即退出且无法再次登录；可随时重新启用。' : '启用后，该操作员可以使用现有密码登录；仍需完成待办的首次改密。' }}</p>
        <div class="account-dialog-actions"><el-button :disabled="saving" @click="dialog = false">取消</el-button><el-button native-type="submit" type="primary" :loading="saving">{{ mode === 'create' ? '创建操作员' : mode === 'reset' ? '确认重置' : '确认' }}</el-button></div>
      </el-form>
    </el-dialog>
  </template>
  <el-result v-else icon="warning" title="仅管理员可管理账号" sub-title="请联系管理员处理账号权限。" />
</template>
