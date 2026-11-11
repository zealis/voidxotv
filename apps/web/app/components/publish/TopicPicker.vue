<script setup lang="ts">
// 发布页 · 话题标签多选（UI 规范 §8.1 / 技术规范 §4.7）
// 候选只出 available；自建话题进入 pending，需审核通过后才能关联。
import { errorMessage } from '~/composables/useApi'

interface Topic { id: number; slug: string; name: string; status: string }

const props = defineProps<{
  modelValue: number[]
  disabled?: boolean
  max?: number
}>()
const emit = defineEmits<{ 'update:modelValue': [number[]] }>()

const { api } = useApi()
const limit = computed(() => props.max ?? 8)
const query = ref('')
const suggestions = ref<Topic[]>([])
const selected = ref<Topic[]>([])
const message = ref('')
const busy = ref(false)
let searchTimer: ReturnType<typeof setTimeout> | null = null
let hydrated = false

async function search() {
  try {
    const data = await api<{ items: Topic[] }>(
      `/api/v1/topics/suggest?q=${encodeURIComponent(query.value)}&limit=10`,
    )
    suggestions.value = data.items || []
  } catch {
    suggestions.value = []
  }
}

function scheduleSearch() {
  if (searchTimer) clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { void search() }, 220)
}

function add(topic: Topic) {
  if (props.disabled || selected.value.some((item) => item.id === topic.id)) return
  if (selected.value.length >= limit.value) {
    message.value = `最多选择 ${limit.value} 个话题`
    return
  }
  selected.value = [...selected.value, topic]
  query.value = ''
  suggestions.value = []
  message.value = ''
  emit('update:modelValue', selected.value.map((item) => item.id))
}

function remove(id: number) {
  if (props.disabled) return
  selected.value = selected.value.filter((item) => item.id !== id)
  emit('update:modelValue', selected.value.map((item) => item.id))
}

async function createTopic() {
  const name = query.value.trim()
  if (!name || busy.value || props.disabled) return
  busy.value = true
  message.value = ''
  try {
    const item = await api<Topic>('/api/v1/topics', { method: 'POST', body: { name } })
    if (item.status === 'pending') {
      message.value = `「${item.name}」已提交审核，通过后即可选用`
    } else {
      add(item)
    }
    query.value = ''
    suggestions.value = []
    await search()
  } catch (error) {
    message.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

// 草稿回填由父组件传入 GET /contents/{id}/topics 的完整列表：
// 候选接口只出 available，若用候选回填会丢掉已下线但仍关联的话题。
function hydrate(items: Topic[]) {
  if (hydrated) return
  hydrated = true
  selected.value = items.slice(0, limit.value)
  emit('update:modelValue', selected.value.map((item) => item.id))
}

defineExpose({ hydrate })
onMounted(() => { void search() })
onBeforeUnmount(() => { if (searchTimer) clearTimeout(searchTimer) })
</script>

<template>
  <div class="topic-picker">
    <div v-if="selected.length" class="chips" aria-label="已选话题">
      <span v-for="item in selected" :key="item.id" class="chip">
        #{{ item.name }}
        <button
          type="button" :disabled="props.disabled"
          :aria-label="`移除话题 ${item.name}`" @click="remove(item.id)"
        >×</button>
      </span>
    </div>
    <div class="search-row">
      <input
        v-model="query" type="text" class="topic-input"
        :disabled="props.disabled" :placeholder="`搜索或自建话题（${selected.length}/${limit}）`"
        aria-label="搜索或输入新话题" @input="scheduleSearch" @keydown.enter.prevent="createTopic"
      />
      <button
        type="button" class="topic-create" :disabled="props.disabled || busy || !query.trim()"
        @click="createTopic"
      >{{ busy ? '提交中…' : '新建' }}</button>
    </div>
    <ul v-if="suggestions.length" class="suggest" aria-label="候选话题">
      <li v-for="item in suggestions" :key="item.id">
        <button
          type="button" :disabled="props.disabled || selected.some((s) => s.id === item.id)"
          @click="add(item)"
        >
          <span>#{{ item.name }}</span>
          <small>{{ selected.some((s) => s.id === item.id) ? '已添加' : '添加' }}</small>
        </button>
      </li>
    </ul>
    <p v-if="message" class="hint" :class="{ warn: message.includes('审核') }" role="status">{{ message }}</p>
  </div>
</template>

<style scoped>
.topic-picker { display: grid; gap: 8px; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chip {
  display: inline-flex; align-items: center; gap: 5px;
  padding: 3px 6px 3px 9px; border-radius: 99px; font-size: 11px;
  border: 1px solid var(--vp-tool-border-strong);
  background: var(--vp-tool-accent-soft); color: var(--vp-tool-accent-ink);
}
.chip button {
  border: 0; background: transparent; color: inherit; cursor: pointer;
  font-size: 14px; line-height: 1; padding: 0 2px;
}
.search-row { display: flex; gap: 8px; }
.topic-input {
  flex: 1; min-width: 0; padding: 8px 10px; border-radius: 9px;
  border: 1px solid var(--vp-tool-input-border);
  background: var(--vp-tool-input); color: var(--vp-tool-text);
  font-size: 12px; font-family: inherit;
}
.topic-create {
  flex: none; padding: 8px 12px; border-radius: 9px; cursor: pointer; font-size: 12px;
  border: 1px solid var(--vp-tool-border-strong);
  background: transparent; color: var(--vp-tool-text); font-family: inherit;
}
.topic-create:disabled, .chip button:disabled { opacity: .55; cursor: not-allowed; }
.suggest {
  list-style: none; margin: 0; padding: 5px; max-height: 170px; overflow-y: auto;
  border-radius: 9px; border: 1px solid var(--vp-tool-border-strong);
  background: var(--vp-tool-panel);
}
.suggest button {
  width: 100%; display: flex; align-items: center; justify-content: space-between;
  padding: 7px 9px; border: 0; border-radius: 6px; cursor: pointer;
  background: transparent; color: var(--vp-tool-text); font-size: 12px; font-family: inherit;
}
.suggest button:hover:not(:disabled) { background: var(--vp-tool-item-active); }
.suggest button:disabled { opacity: .5; cursor: default; }
.suggest small { color: var(--vp-tool-text-2); font-size: 10px; }
.hint { margin: 0; font-size: 11px; color: var(--vp-tool-text-2); }
.hint.warn { color: var(--vp-tool-danger-ink); }
</style>
