<script setup lang="ts">
// 举报对话框：把已有后端（POST /api/v1/reports，24 小时并单，§9.5）接到前台。
// 理由枚举与 admin repository 的 validReasons 对齐；处置结果在「我 → 我的举报」可见。
import { ref, watch } from 'vue'
import { ApiError, errorMessage } from '~/composables/useApi'

const props = defineProps<{
  open: boolean
  contentId: number
  contentTitle: string
  /** 举报对象：默认作品；评论区传 comment，object_id 即评论 id。 */
  objectType?: 'content' | 'comment'
}>()
const emit = defineEmits<{ close: []; reported: [] }>()

const REASONS = [
  { value: 'vulgar', label: '低俗色情' },
  { value: 'copyright', label: '侵权盗版' },
  { value: 'harassment', label: '骚扰辱骂' },
  { value: 'fraud', label: '欺诈诈骗' },
  { value: 'other', label: '其他问题' },
]

const { api, getMe } = useApi()

const { open: openAuthDialog } = useAuthDialog()

// 未登录时引导去登录：先收起举报对话框，否则两个 VpDialog 会叠在一起。
function goLogin() {
  emit('close')
  openAuthDialog('login')
}
const reason = ref('')
const detail = ref('')
const busy = ref(false)
const notice = ref('')
const needLogin = ref(false)
// 打开时先确认登录态再渲染表单，避免匿名用户看到一闪而过的表单。
const checking = ref(true)

watch(() => props.open, async (open) => {
  if (!open) return
  reason.value = ''
  detail.value = ''
  notice.value = ''
  checking.value = true
  needLogin.value = !(await getMe())
  checking.value = false
})

async function submit() {
  if (!reason.value || busy.value) return
  busy.value = true
  notice.value = ''
  try {
    await api('/api/v1/reports', {
      method: 'POST',
      body: {
        object_type: props.objectType || 'content',
        object_id: props.contentId,
        reason: reason.value,
        detail: detail.value.trim() || undefined,
      },
    })
    emit('reported')
    emit('close')
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
    } else {
      notice.value = errorMessage(error)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <VpDialog
    :open="props.open" :title="props.objectType === 'comment' ? '举报评论' : '举报作品'"
    :busy="busy" @close="emit('close')"
  >
    <p class="dialog-text">
      举报「{{ props.contentTitle || (props.objectType === 'comment' ? '该评论' : '该作品') }}」：
      {{ props.objectType === 'comment' ? '同一条评论' : '同一条内容' }} 24 小时内的举报会合并处理，
      处置结果将在「我 → 我的举报」反馈。
    </p>
    <p v-if="needLogin" class="dialog-text">
      请先<button type="button" class="login" @click="goLogin">登录</button>后再举报。
    </p>
    <p v-else-if="checking" class="dialog-text" role="status">正在检查登录状态…</p>
    <template v-else>
      <fieldset class="reasons">
        <legend class="sr-only">举报理由</legend>
        <label v-for="item in REASONS" :key="item.value" class="reason">
          <input v-model="reason" type="radio" name="report-reason" :value="item.value" />
          <span>{{ item.label }}</span>
        </label>
      </fieldset>
      <textarea
        v-model="detail" rows="3" maxlength="500"
        placeholder="补充说明（可选，500 字以内）" aria-label="举报补充说明"
      />
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    </template>
    <div class="dialog-actions">
      <button type="button" class="btn" @click="emit('close')">取消</button>
      <button
        type="button" class="btn primary" :disabled="!reason || busy || checking"
        @click="submit"
      >{{ busy ? '提交中…' : '提交举报' }}</button>
    </div>
  </VpDialog>
</template>

<style scoped>
.dialog-text { margin: 0 0 14px; font-size: 13px; line-height: 1.7; }
.login { border: 0; background: none; padding: 0; cursor: pointer; font-family: inherit; font-size: inherit; color: var(--vp-tool-accent, #ff2e7e); text-decoration: underline; }
.reasons { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; margin: 0 0 12px; padding: 0; border: 0; }
.reason {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 12px; border-radius: 10px; cursor: pointer; font-size: 13px;
  border: 1px solid var(--vp-tool-border, rgba(255, 255, 255, 0.16));
}
.reason input { accent-color: var(--vp-tool-accent, #ff2e7e); }
textarea {
  width: 100%; box-sizing: border-box; resize: vertical;
  padding: 10px 12px; border-radius: 10px; font-size: 13px; font-family: inherit;
  border: 1px solid var(--vp-tool-border, rgba(255, 255, 255, 0.16));
  background: transparent; color: inherit;
}
.notice { margin: 0 0 10px; font-size: 12px; color: #ff6b5e; }
.dialog-actions { display: flex; gap: 10px; justify-content: flex-end; }
.btn {
  padding: 9px 18px; border-radius: 10px; cursor: pointer; font-size: 13px;
  border: 1px solid var(--vp-tool-border, rgba(255, 255, 255, 0.16));
  background: transparent; color: inherit;
}
.btn.primary { background: var(--vp-tool-accent, #ff2e7e); border-color: transparent; color: #fff; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.sr-only {
  position: absolute; width: 1px; height: 1px; margin: -1px;
  clip: rect(0 0 0 0); overflow: hidden; white-space: nowrap;
}
</style>
