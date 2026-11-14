<script setup lang="ts">
// 用户行操作按钮：桌面表格与窄屏卡片共用，避免两处逻辑漂移。
// 认证与取消认证成对出现——已认证的创作者也要有下行路径。
import { ElButton } from 'element-plus'
import type { AdminUserRow } from '~/composables/useAdminApi'

type Action = 'warn' | 'mute' | 'ban' | 'unban' | 'verify_creator' | 'unverify_creator'

const props = defineProps<{ row: AdminUserRow }>()
const emit = defineEmits<{
  sanction: [row: AdminUserRow, action: Action]
  avatar: [row: AdminUserRow]
}>()

const { can } = useAdminApi()
const writable = computed(() => can('users:sanction'))
</script>

<template>
  <div v-if="writable" class="row-actions">
    <ElButton size="small" plain @click="emit('sanction', props.row, 'warn')">警告</ElButton>
    <ElButton size="small" plain @click="emit('sanction', props.row, 'mute')">禁言</ElButton>
    <ElButton size="small" type="danger" plain @click="emit('sanction', props.row, 'ban')">封禁</ElButton>
    <ElButton
      v-if="props.row.status !== 'active'" size="small" plain
      @click="emit('sanction', props.row, 'unban')"
    >解封</ElButton>
    <ElButton
      v-if="props.row.role !== 'creator'" size="small" plain type="primary"
      @click="emit('sanction', props.row, 'verify_creator')"
    >认证</ElButton>
    <ElButton
      v-else size="small" plain
      @click="emit('sanction', props.row, 'unverify_creator')"
    >取消认证</ElButton>
    <ElButton size="small" plain @click="emit('avatar', props.row)">头像</ElButton>
  </div>
  <span v-else class="sub">只读角色</span>
</template>

<style scoped>
.row-actions { display: flex; flex-wrap: wrap; gap: 6px; }
/* Element Plus 给相邻按钮加 margin-left:12px，它和 gap 叠加后换行不会归零，
   窄屏下第二行开头会比第一行右移 12px。间距统一交给 gap。 */
.row-actions :deep(.el-button + .el-button) { margin-left: 0; }
.row-actions :deep(.el-button) { margin: 0; padding: 5px 9px; }
.sub { font-size: 11px; color: #8b8b93; }
</style>
