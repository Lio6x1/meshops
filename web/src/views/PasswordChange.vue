<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { changePassword, errorText, session } from '../api'
import { passwordError } from '../accounts'

const emit = defineEmits<{ 'password-changed': [] }>()
const currentPassword = ref(''), newPassword = ref(''), confirmation = ref('')
const busy = ref(false), error = ref('')
function clearPasswords() {
  currentPassword.value = ''
  newPassword.value = ''
  confirmation.value = ''
}
onUnmounted(clearPasswords)
async function submit() {
  if (busy.value) return
  error.value = !currentPassword.value ? '请输入当前密码' : passwordError(newPassword.value)
  if (!error.value && newPassword.value !== confirmation.value) error.value = '两次输入的新密码不一致'
  if (!error.value && currentPassword.value === newPassword.value) error.value = '新密码须与当前密码不同'
  if (error.value) return
  busy.value = true
  try {
    await changePassword(currentPassword.value, newPassword.value)
    emit('password-changed')
  } catch (e) {
    error.value = errorText(e)
  } finally {
    clearPasswords()
    busy.value = false
  }
}
</script>
<template>
  <section class="page-heading">
    <div>
      <span class="eyebrow">ACCOUNT SECURITY</span>
      <h1>{{ session?.mustChangePassword ? '设置个人密码' : '修改密码' }}</h1>
      <p>为 {{ session?.displayName || session?.username }} 修改密码，保存后请重新登录。</p>
    </div>
  </section>
  <section class="panel password-panel">
    <el-alert v-if="session?.mustChangePassword" title="首次登录或密码已被重置，请先修改密码再进入工作空间。" type="warning" :closable="false" />
    <el-alert v-if="error" :title="error" type="error" :closable="false" role="alert" />
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item label="当前密码" label-for="current-password">
        <el-input id="current-password" v-model="currentPassword" type="password" show-password autocomplete="off" :disabled="busy" />
      </el-form-item>
      <el-form-item label="新密码" label-for="new-password">
        <el-input id="new-password" v-model="newPassword" type="password" show-password autocomplete="off" :disabled="busy" placeholder="12–128 个字符" />
      </el-form-item>
      <el-form-item label="确认新密码" label-for="confirm-password">
        <el-input id="confirm-password" v-model="confirmation" type="password" show-password autocomplete="off" :disabled="busy" />
      </el-form-item>
      <p class="muted small">修改后，当前账号的已有会话将失效。</p>
      <el-button type="primary" native-type="submit" :loading="busy" :disabled="!currentPassword || !newPassword || !confirmation">保存密码并重新登录</el-button>
    </el-form>
  </section>
</template>
