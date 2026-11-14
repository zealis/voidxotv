<script setup lang="ts">
// 功能开关（§4.7）：目前只有「GIF 表情包」一项。
// 图源密钥只存服务端：评论区的 GIF 搜索走本站代理，浏览器永远拿不到 id/key。
import { ElButton, ElInput, ElMessage, ElOption, ElSelect } from 'element-plus'

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

const klipyFilters = [
  { value: 'off', label: 'off（不按分级过滤，原样返回）' },
  { value: 'low', label: 'low（只拦最高一档）' },
  { value: 'medium', label: 'medium（再收紧一级）' },
  { value: 'high', label: 'high（最严：只出适合全年龄的结果）' },
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

onMounted(load)
</script>

<template>
  <section class="flags">
    <header class="head">
      <div>
        <h3>功能开关</h3>
        <p>默认关闭、需要外部凭据的能力集中在这里；密钥只存服务端，浏览器拿不到。</p>
      </div>
      <span class="state" :class="{ on: ready }">{{ ready ? 'GIF 已启用' : 'GIF 未启用' }}</span>
    </header>

    <p v-if="error" class="error">{{ error }}</p>

    <div v-loading="loading" class="panel">
      <div class="panel-head">
        <strong>GIF 表情包</strong>
        <p>
          评论区表情面板的 GIF 页：搜索与热门都走本站代理，按人限流、结果按天缓存以省上游配额。
          选中的 GIF 以图片形式发进评论（正文只存图片地址标记）。
        </p>
      </div>

      <div class="form">
        <label>图源</label>
        <ElSelect v-model="config.provider" class="full" :disabled="!writable">
          <ElOption value="off" label="关闭（评论区隐藏 GIF 页）" />
          <ElOption value="apihz" label="API 盒子 · 表情包（国内可达，本站代理）" />
          <ElOption value="klipy" label="KLIPY（浏览器直连，海外可用）" />
        </ElSelect>
        <p class="hint">
          <template v-if="config.provider === 'apihz'">
            图源方在 <strong>API 盒子用户中心 → 个人资料</strong> 查看开发者 ID 与 KEY；
            本站按「普通集群负载」线路调用，密钥只在服务端拼接。
          </template>
          <template v-else-if="config.provider === 'klipy'">
            App Key 在 <strong>KLIPY Partner Panel</strong>（partner.klipy.com/api-keys）创建。
            KLIPY 的条款要求请求由访客浏览器直接发起、不得经本站转发或缓存，所以这个 key
            会出现在前端请求里（它本身就是客户端密钥）；结果缓存也只能落在访客自己的浏览器里。
          </template>
          <template v-else>两个图源二选一，内容不混排。</template>
        </p>

        <template v-if="config.provider === 'apihz'">
          <label>线路</label>
          <ElSelect v-model="config.apihz_line" class="full" :disabled="!writable">
            <ElOption value="cn" label="线路二 · 普通集群负载（域名接口）" />
            <ElOption value="vip" label="线路三 · VIP 集群负载" />
          </ElSelect>

          <label>开发者 ID</label>
          <ElInput v-model="config.apihz_id" placeholder="例：10000000" autocomplete="off" :disabled="!writable" />

          <label>开发者 KEY</label>
          <ElInput
            v-model="config.apihz_key" type="password" show-password
            placeholder="通讯秘钥，只存服务端" autocomplete="new-password" :disabled="!writable"
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
          <p class="hint">
            每次返回上限 20 条、关键词最长 10 个汉字（上游限制）。缓存按「线路 + 关键词 + 页码」存
            <code>gif_search_cache</code>，同一查询在缓存期内不再请求上游；填 0 表示每次都真调。
          </p>
        </template>

        <template v-else-if="config.provider === 'klipy'">
          <label>App Key</label>
          <ElInput
            v-model="config.klipy_key" type="password" show-password
            placeholder="KLIPY 发放的 app_key" autocomplete="new-password" :disabled="!writable"
          />

          <label>内容分级（content_filter）</label>
          <ElSelect v-model="config.klipy_filter" class="full" :disabled="!writable">
            <ElOption v-for="item in klipyFilters" :key="item.value" :value="item.value" :label="item.label" />
          </ElSelect>

          <div class="two">
            <div>
              <label>国家 / 地区</label>
              <ElInput v-model="config.klipy_locale" placeholder="cn / us / jp" maxlength="2" :disabled="!writable" />
            </div>
            <div>
              <label>每页条数</label>
              <ElInput v-model.number="config.klipy_limit" type="number" :min="8" :max="50" :disabled="!writable" />
            </div>
          </div>

          <label>浏览器缓存天数</label>
          <ElInput v-model.number="config.klipy_cache_days" type="number" :min="0" :max="90" :disabled="!writable" />
          <p class="hint">
            KLIPY 不允许服务端缓存，这里控制的是<strong>访客浏览器</strong>的 localStorage：
            同一关键词 + 同一页在天数内不重复请求。国家码是 ISO 3166-1 两位（cn / us / jp），留空则不带这个参数；
            每页 8–50 条是他们的允许范围。填 0 = 不缓存。
          </p>
        </template>

        <p v-else class="hint">
          关闭后评论输入框的表情面板只保留「表情」页，GIF 页签不再出现；已发出的 GIF 评论不受影响。
        </p>
      </div>

      <div v-if="writable" class="actions">
        <ElButton type="primary" :loading="saving" @click="save">保存</ElButton>
        <ElButton :disabled="saving" @click="load">重新读取</ElButton>
      </div>
      <p v-else class="hint">当前角色只有查看权限，修改需要「系统设置」写权限。</p>
    </div>
  </section>
</template>

<style scoped>
.flags { display: grid; gap: 14px; max-width: var(--vp-layout-content-max); }
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.head h3 { margin: 0 0 4px; font-size: 15px; }
.head p { margin: 0; font-size: 12px; line-height: 1.6; color: #8b8b93; }
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
  gap: 14px;
  padding: 16px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.02);
}
.panel-head strong { font-size: 14px; }
.panel-head p { margin: 6px 0 0; font-size: 12px; line-height: 1.7; color: #8b8b93; }
.form { display: grid; gap: 8px; max-width: 560px; }
.form label { font-size: 12px; color: #a1a1aa; }
.form .full { width: 100%; }
.hint { margin: 0; font-size: 12px; line-height: 1.7; color: #8b8b93; }
.hint code { padding: 1px 5px; border-radius: 4px; background: rgba(255, 255, 255, 0.08); }
.two { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
.two > div { display: grid; gap: 6px; }
.actions { display: flex; gap: 8px; }
.error { margin: 0; font-size: 12px; color: #ff6961; }
</style>
