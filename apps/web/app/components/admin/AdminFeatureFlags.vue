<script setup lang="ts">
// 功能开关（§4.7）：目前只有「GIF 表情包」一项。
// 图源密钥只存服务端：评论区的 GIF 搜索走本站代理，浏览器永远拿不到 id/key。
import { ElButton, ElInput, ElMessage, ElOption, ElSelect, ElSwitch } from 'element-plus'

interface GifConfig {
  provider: string
  apihz_line: string
  apihz_id: string
  apihz_key: string
  apihz_limit: number
  cache_days: number
  klipy_key: string
  klipy_filter: string
  klipy_locale: string
  klipy_limit: number
  klipy_cache_days: number
}

const { request, can } = useAdminApi()
const writable = computed(() => can('settings:write'))

const loading = ref(true)
const saving = ref(false)
const error = ref('')
const ready = ref(false)
const config = reactive<GifConfig>({
  provider: 'off', apihz_line: 'cn', apihz_id: '', apihz_key: '',
  apihz_limit: 20, cache_days: 7,
  klipy_key: '', klipy_filter: 'off', klipy_locale: 'cn',
  klipy_limit: 24, klipy_cache_days: 1,
})

// 档位标签用中文，上游枚举值留在 value 里：后台不该让人看 API 参数名。
const klipyFilters = [
  { value: 'off', label: '不限' },
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await request<{ config: GifConfig; ready: boolean }>('/api/v1/admin/settings/gif')
    Object.assign(config, data.config)
    ready.value = data.ready
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '加载失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  if (saving.value) return
  saving.value = true
  error.value = ''
  try {
    const data = await request<{ config: GifConfig; ready: boolean }>('/api/v1/admin/settings/gif', {
      method: 'PUT', csrf: true, body: { ...config },
    })
    Object.assign(config, data.config)
    ready.value = data.ready
    ElMessage.success(data.ready ? '已保存，表情包现在可用' : '已保存')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '保存失败'
  } finally {
    saving.value = false
  }
}

interface SpamConfig {
  enabled: boolean
  words: string
  lexicon_name: string
  lexicon_words: number
  lexicon_at: string
  installed: boolean
}

interface SpamTestResult {
  enabled: boolean
  blocked: boolean
  hits: string[]
}

const spam = reactive<SpamConfig>({
  enabled: false, words: '', lexicon_name: '', lexicon_words: 0,
  lexicon_at: '', installed: false,
})
const spamError = ref('')
const spamSaving = ref(false)
const downloading = ref(false)
const testText = ref('')
const testBusy = ref(false)
const testResult = ref<SpamTestResult | null>(null)

const adminReason = (cause: unknown) =>
  cause instanceof Error ? cause.message : '操作失败，请稍后再试'

async function loadSpam() {
  try {
    const data = await request<{ config: SpamConfig }>('/api/v1/admin/settings/spam')
    Object.assign(spam, data.config)
    spamError.value = ''
  } catch (cause) {
    spamError.value = adminReason(cause)
  }
}

async function saveSpam() {
  if (spamSaving.value) return
  spamSaving.value = true
  spamError.value = ''
  try {
    const data = await request<{ config: SpamConfig }>('/api/v1/admin/settings/spam', {
      method: 'PUT', csrf: true, body: { enabled: spam.enabled, words: spam.words },
    })
    Object.assign(spam, data.config)
    ElMessage.success(spam.enabled ? '已保存，过滤已生效' : '已保存')
  } catch (cause) {
    spamError.value = adminReason(cause)
    ElMessage.error(spamError.value)
  } finally {
    spamSaving.value = false
  }
}

async function downloadLexicon() {
  if (downloading.value) return
  downloading.value = true
  spamError.value = ''
  try {
    const data = await request<{ config: SpamConfig }>('/api/v1/admin/settings/spam/lexicon', {
      method: 'POST', body: {}, csrf: true,
    })
    Object.assign(spam, data.config)
    ElMessage.success('词库已下载')
  } catch (cause) {
    spamError.value = adminReason(cause)
    ElMessage.error(spamError.value)
  } finally {
    downloading.value = false
  }
}

async function runSpamTest() {
  if (testBusy.value || !testText.value.trim()) return
  testBusy.value = true
  spamError.value = ''
  try {
    testResult.value = await request<SpamTestResult>('/api/v1/admin/settings/spam/test', {
      method: 'POST', body: { text: testText.value }, csrf: true,
    })
  } catch (cause) {
    spamError.value = adminReason(cause)
  } finally {
    testBusy.value = false
  }
}

onMounted(() => { void load(); void loadSpam() })
</script>

<template>
  <section class="flags">
    <p v-if="error" class="error">{{ error }}</p>

    <div v-loading="loading" class="panel">
      <div class="panel-head">
        <strong>GIF 表情包</strong>
        <span class="state" :class="{ on: ready }">{{ ready ? '已启用' : '未启用' }}</span>
      </div>

      <div class="form">
        <label>图源</label>
        <ElSelect v-model="config.provider" class="full" :disabled="!writable">
          <ElOption value="off" label="关闭" />
          <ElOption value="apihz" label="API 盒子" />
          <ElOption value="klipy" label="KLIPY" />
        </ElSelect>

        <template v-if="config.provider === 'apihz'">
          <label>线路</label>
          <ElSelect v-model="config.apihz_line" class="full" :disabled="!writable">
            <ElOption value="cn" label="普通集群" />
            <ElOption value="vip" label="VIP 集群" />
          </ElSelect>

          <label>开发者 ID</label>
          <ElInput v-model="config.apihz_id" placeholder="例：10000000" autocomplete="off" :disabled="!writable" />

          <label>开发者 KEY</label>
          <ElInput
            v-model="config.apihz_key" type="password" show-password
            placeholder="通讯秘钥" autocomplete="new-password" :disabled="!writable"
          />

          <div class="two">
            <div>
              <label>每次返回</label>
              <ElInput v-model.number="config.apihz_limit" type="number" :min="1" :max="20" :disabled="!writable" />
            </div>
            <div>
              <label>结果缓存天数</label>
              <ElInput v-model.number="config.cache_days" type="number" :min="0" :max="90" :disabled="!writable" />
            </div>
          </div>
        </template>

        <template v-else-if="config.provider === 'klipy'">
          <label>App Key</label>
          <ElInput
            v-model="config.klipy_key" type="password" show-password
            placeholder="App Key" autocomplete="new-password" :disabled="!writable"
          />

          <label>内容分级</label>
          <ElSelect v-model="config.klipy_filter" class="full" :disabled="!writable">
            <ElOption v-for="item in klipyFilters" :key="item.value" :value="item.value" :label="item.label" />
          </ElSelect>

          <div class="two">
            <div>
              <label>国家 / 地区</label>
              <ElInput v-model="config.klipy_locale" placeholder="cn" maxlength="2" :disabled="!writable" />
            </div>
            <div>
              <label>每页条数</label>
              <ElInput v-model.number="config.klipy_limit" type="number" :min="8" :max="50" :disabled="!writable" />
            </div>
          </div>

          <label>浏览器缓存天数</label>
          <ElInput v-model.number="config.klipy_cache_days" type="number" :min="0" :max="90" :disabled="!writable" />
        </template>
      </div>

      <div v-if="writable" class="actions">
        <ElButton type="primary" :loading="saving" @click="save">保存</ElButton>
        <ElButton :disabled="saving" @click="load">重新读取</ElButton>
      </div>
      <p v-else class="hint">当前角色只有查看权限。</p>
    </div>

    <div class="panel">
      <div class="panel-head">
        <strong>垃圾信息控制</strong>
        <span class="state" :class="{ on: spam.enabled }">{{ spam.enabled ? '已启用' : '未启用' }}</span>
      </div>

      <p v-if="spamError" class="error">{{ spamError }}</p>

      <div class="form">
        <div class="switch-row">
          <span>启用过滤</span>
          <ElSwitch v-model="spam.enabled" :disabled="!writable" />
        </div>

        <label>自定义敏感词</label>
        <ElInput
          v-model="spam.words" type="textarea" :rows="5" maxlength="4000"
          placeholder="一行一个" :disabled="!writable"
        />

        <label>词库</label>
        <div class="lexicon">
          <span v-if="spam.installed">
            {{ spam.lexicon_name }} · {{ spam.lexicon_words.toLocaleString() }} 条词
            <template v-if="spam.lexicon_at">
              · {{ new Date(spam.lexicon_at).toLocaleDateString('zh-CN') }}
            </template>
          </span>
          <span v-else>尚未下载</span>
        </div>
        <div class="actions">
          <ElButton :loading="downloading" :disabled="!writable || downloading" @click="downloadLexicon">
            下载词库
          </ElButton>
        </div>

        <label>测试</label>
        <ElInput v-model="testText" placeholder="输入一段文字" maxlength="500" />
        <div class="actions">
          <ElButton :loading="testBusy" :disabled="!testText.trim() || testBusy" @click="runSpamTest">
            测试
          </ElButton>
        </div>
        <p v-if="testResult" class="test-result" :class="{ blocked: testResult.blocked }">
          {{ testResult.blocked
            ? `将拒绝提交（命中：${testResult.hits.join('、')}）`
            : (testResult.enabled ? '可以提交' : '过滤未启用，可以提交') }}
        </p>
      </div>

      <div v-if="writable" class="actions">
        <ElButton type="primary" :loading="spamSaving" @click="saveSpam">保存</ElButton>
        <ElButton :disabled="spamSaving" @click="loadSpam">重新读取</ElButton>
      </div>
      <p v-else class="hint">当前角色只有查看权限。</p>
    </div>
  </section>
</template>

<style scoped>
.flags { display: grid; gap: 14px; max-width: var(--vp-layout-content-max); }
.state {
  flex: none;
  padding: 3px 10px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  font-size: 12px;
  color: #a1a1aa;
}
.state.on { border-color: rgba(255, 46, 126, 0.5); color: #ff2e7e; }
.panel {
  display: grid;
  gap: 16px;
  padding: 16px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.02);
}
.panel-head { display: flex; align-items: center; gap: 10px; }
.panel-head strong { font-size: 14px; }
.form { display: grid; gap: 8px; max-width: 560px; }
.form label { font-size: 12px; color: #a1a1aa; }
.form .full { width: 100%; }
.hint { margin: 0; font-size: 12px; line-height: 1.7; color: #8b8b93; }
.two { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.two > div { display: grid; gap: 6px; }
.actions { display: flex; gap: 8px; }
.switch-row {
  display: flex; align-items: center; justify-content: space-between;
  max-width: 560px; font-size: 13px; color: #f4f4f5;
}
.lexicon { font-size: 12px; color: #d4d4d8; }
.test-result { margin: 0; font-size: 12px; color: #4ade80; }
.test-result.blocked { color: #ff6961; }
.error { margin: 0; font-size: 12px; color: #ff6961; }
</style>
