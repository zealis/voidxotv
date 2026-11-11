#!/usr/bin/env node
// 本地开发服务编排（跨平台，Windows / macOS / Linux 通用）。
//
//   node scripts/dev.mjs up            起全套：容器 → 迁移 → API / Worker / Web（已在跑的就跳过），前台守护
//   node scripts/dev.mjs up --watch    守护的同时监听 Go 源码，改完自动重建 + 重启 API / Worker
//   node scripts/dev.mjs up --detach   只启动不守护（CI / 脚本里用）
//   node scripts/dev.mjs status        健康检查与版本一览
//   node scripts/dev.mjs restart api   重建并重启指定服务（api / worker / web / all）
//   node scripts/dev.mjs stop all      停止指定服务
//   node scripts/dev.mjs watch         只开源码监听（服务已在跑时用）
//
// 环境变量默认值与 scripts/dev.sh 保持一致；需要覆盖时在调用前 export。

import { spawn, spawnSync } from 'node:child_process'
import { appendFileSync, closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, watch, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const dataDir = join(root, 'data')
const pidDir = join(dataDir, 'pids')
const logDir = join(dataDir, 'logs')
mkdirSync(pidDir, { recursive: true })
mkdirSync(logDir, { recursive: true })

const isWin = process.platform === 'win32'
const exe = (name) => (isWin ? `${name}.exe` : name)

// 守护进程必须活到用户按 Ctrl+C：单个子任务出错不该带走整套服务。
process.on('unhandledRejection', (error) => {
  console.error('未处理的 Promise 异常（守护继续）：', error?.message ?? error)
})

// 开发环境默认值：与 scripts/dev.sh 同一套凭据，仅本机使用。
const env = {
  ...process.env,
  YJ_ENV: process.env.YJ_ENV || 'dev',
  YJ_HTTP_ADDR: process.env.YJ_HTTP_ADDR || '127.0.0.1:18080',
  YJ_DATABASE_URL:
    process.env.YJ_DATABASE_URL || 'postgres://yejie:yejie_dev@127.0.0.1:5432/yejie?sslmode=disable',
  YJ_REDIS_ADDR: process.env.YJ_REDIS_ADDR || '127.0.0.1:6380',
  YJ_COOKIE_SECURE: process.env.YJ_COOKIE_SECURE || 'false',
  YJ_S3_ENDPOINT: process.env.YJ_S3_ENDPOINT || 'http://127.0.0.1:9100',
  YJ_S3_PUBLIC_ENDPOINT: process.env.YJ_S3_PUBLIC_ENDPOINT || 'http://127.0.0.1:9100',
  YJ_S3_BUCKET: process.env.YJ_S3_BUCKET || 'yejie-dev',
  YJ_S3_REGION: process.env.YJ_S3_REGION || 'us-east-1',
  YJ_S3_ACCESS_KEY: process.env.YJ_S3_ACCESS_KEY || 'yejie_dev',
  YJ_S3_SECRET_KEY: process.env.YJ_S3_SECRET_KEY || 'yejie_dev_secret',
  YJ_FFMPEG_PATH: process.env.YJ_FFMPEG_PATH || join(root, '.tools/ffmpeg', exe('ffmpeg')),
  YJ_FFPROBE_PATH: process.env.YJ_FFPROBE_PATH || join(root, '.tools/ffmpeg', exe('ffprobe')),
  YJ_WORKER_TEMP_DIR: process.env.YJ_WORKER_TEMP_DIR || join(root, 'data/transcode-tmp'),
  YJ_WORKER_IDLE_INTERVAL: process.env.YJ_WORKER_IDLE_INTERVAL || '2s',
  YJ_API_ORIGIN: process.env.YJ_API_ORIGIN || 'http://127.0.0.1:18080',
  YJ_ALLOWED_ORIGINS: process.env.YJ_ALLOWED_ORIGINS || 'http://127.0.0.1:3000',
  API_ORIGIN: process.env.API_ORIGIN || 'http://127.0.0.1:18080',
  YJ_SITE_URL: process.env.YJ_SITE_URL || 'http://127.0.0.1:3000',
  NUXT_PUBLIC_SITE_URL: process.env.NUXT_PUBLIC_SITE_URL || 'http://127.0.0.1:3000',
  YJ_DELIVERY_TOKEN: process.env.YJ_DELIVERY_TOKEN || 'dev-internal-token',
  // 交付回源必须是 Nuxt 的 3000：旧代理 13000 已停，指错会让交付任务全部死信。
  YJ_DELIVERY_NUXT_ORIGIN: process.env.YJ_DELIVERY_NUXT_ORIGIN || 'http://127.0.0.1:3000',
  YJ_DELIVERY_IDLE_INTERVAL: process.env.YJ_DELIVERY_IDLE_INTERVAL || '2s',
  YJ_ADMIN_BOOTSTRAP_PASSWORD: process.env.YJ_ADMIN_BOOTSTRAP_PASSWORD || 'dev-admin-password',
  YJ_STATS_INTERVAL: process.env.YJ_STATS_INTERVAL || '30s',
  YJ_IMPORTS_INTERVAL: process.env.YJ_IMPORTS_INTERVAL || '5s',
  PORT: process.env.PORT || '3000',
  HOST: process.env.HOST || '127.0.0.1',
}

const services = {
  api: {
    kind: 'go',
    image: 'yejie-api',
    build: './cmd/api',
    bin: join(root, 'bin', exe('yejie-api')),
    health: 'http://127.0.0.1:18080/health/ready',
  },
  worker: {
    kind: 'go',
    image: 'yejie-worker',
    build: './cmd/worker',
    bin: join(root, 'bin', exe('yejie-worker')),
    health: null,
  },
  web: {
    kind: 'node',
    cwd: join(root, 'apps/web'),
    // 直接用 node 跑 nuxt CLI：绕开 npx / shell 的 PATH 与引号问题，跨平台一致。
    command: process.execPath,
    args: [join(root, 'apps/web/node_modules/nuxt/bin/nuxt.mjs'), 'dev'],
    health: 'http://127.0.0.1:3000/',
  },
}

const pidFile = (name) => join(pidDir, `${name}.pid`)
const logFile = (name) => join(logDir, `${name}.log`)

function sh(command, args) {
  const result = spawnSync(command, args, { cwd: root, env, stdio: 'inherit', shell: isWin })
  if (result.status !== 0) throw new Error(`命令失败（退出码 ${result.status}）：${command} ${args.join(' ')}`)
}

async function healthy(name) {
  const url = services[name]?.health
  if (!url) return false
  try {
    return (await fetch(url, { signal: AbortSignal.timeout(1500) })).ok
  } catch {
    return false
  }
}

function readPid(name) {
  if (!existsSync(pidFile(name))) return null
  const pid = Number(readFileSync(pidFile(name), 'utf8').trim())
  if (!pid) return null
  try {
    process.kill(pid, 0)
    return pid
  } catch {
    rmSync(pidFile(name), { force: true })
    return null
  }
}

// 别的会话（或手动终端）起的进程没有 pid 文件，只能按镜像名/可执行路径认。
function processRunning(name) {
  const spec = services[name]
  if (!spec || spec.kind !== 'go') return false
  if (isWin) {
    const out = spawnSync('tasklist', ['/FI', `IMAGENAME eq ${exe(spec.image)}`], { encoding: 'utf8' }).stdout || ''
    return out.includes(spec.image)
  }
  return spawnSync('pgrep', ['-f', spec.bin], { encoding: 'utf8' }).status === 0
}

function killPid(pid) {
  // Windows 上用 /T 连同子进程一起收（nuxt dev 会派生子 node）
  if (isWin) spawnSync('taskkill', ['/F', '/T', '/PID', String(pid)], { stdio: 'ignore' })
  else {
    try {
      process.kill(-pid, 'SIGTERM')
    } catch {
      try {
        process.kill(pid, 'SIGTERM')
      } catch {
        /* 已退出 */
      }
    }
  }
}

function killImage(name) {
  const spec = services[name]
  if (isWin) spawnSync('taskkill', ['/F', '/IM', exe(spec.image)], { stdio: 'ignore' })
  else spawnSync('pkill', ['-f', spec.bin], { stdio: 'ignore' })
}

async function running(name) {
  if (services[name]?.health) return healthy(name)
  return Boolean(readPid(name)) || processRunning(name)
}

async function stop(name) {
  const pid = readPid(name)
  if (pid) {
    killPid(pid)
    rmSync(pidFile(name), { force: true })
    console.log(`  已停止 ${name}（pid ${pid}）`)
  }
  if (services[name]?.kind === 'go' && processRunning(name)) {
    killImage(name)
    console.log(`  已停止 ${name}（按进程名兜底）`)
  }
}

// 编译失败不能把守护进程带崩：返回布尔值，调用方决定是沿用上一版二进制还是跳过。
function build(name) {
  console.log(`  编译 ${name} …`)
  const result = spawnSync('go', ['build', '-o', services[name].bin, services[name].build], {
    cwd: root,
    env,
    stdio: 'inherit',
    shell: isWin,
  })
  if (result.status === 0) return true
  console.error(`  ✗ ${name} 编译失败（退出码 ${result.status}），沿用上一版二进制`)
  return false
}

async function start(name, options = {}) {
  const spec = services[name]
  if (spec.kind === 'go' && !options.built) {
    const ok = build(name)
    if (!ok && !existsSync(spec.bin)) {
      console.error(`  ✗ ${name} 没有可用的二进制（且源码编译不过），跳过启动`)
      return false
    }
  }
  // spawn 的 stdio 只接受已打开的文件描述符，WriteStream 在 open 之前 fd 为 null。
  const fd = openSync(logFile(name), 'a')
  appendFileSync(logFile(name), `\n=== ${new Date().toISOString()} 启动 ${name} ===\n`)
  const child =
    spec.kind === 'go'
      ? spawn(spec.bin, [], { cwd: root, env, detached: true, stdio: ['ignore', fd, fd] })
      : spawn(spec.command, spec.args, { cwd: spec.cwd, env, detached: true, stdio: ['ignore', fd, fd] })
  closeSync(fd)
  child.unref()
  writeFileSync(pidFile(name), String(child.pid), 'utf8')
  console.log(`  已启动 ${name}（pid ${child.pid}，日志 data/logs/${name}.log）`)
  return true
}

async function waitHealthy(name, seconds) {
  if (!services[name]?.health) return true
  for (let i = 0; i < seconds * 2; i++) {
    if (await healthy(name)) return true
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  return false
}

async function status() {
  const rows = []
  for (const [name, spec] of Object.entries(services)) {
    const up = await running(name)
    let detail = up ? '运行中' : '未运行'
    if (name === 'api' && up) {
      try {
        const payload = await (await fetch('http://127.0.0.1:18080/health/live')).json()
        detail = `运行中 · v${payload?.data?.version ?? '?'}`
      } catch {
        detail = '运行中'
      }
    }
    rows.push([name, spec.health || '（进程存活）', detail])
  }
  const width = (text) => [...text].reduce((sum, char) => sum + (char.charCodeAt(0) > 0x2e80 ? 2 : 1), 0)
  const pad = (text, width_) => text + ' '.repeat(Math.max(0, width_ - width(text)))
  const nameWidth = Math.max(...rows.map((row) => width(row[0]))) + 2
  const urlWidth = Math.max(...rows.map((row) => width(row[1]))) + 2
  console.log('\n服务状态')
  for (const [name, url, detail] of rows) console.log(`  ${pad(name, nameWidth)}${pad(url, urlWidth)}${detail}`)
  console.log('')
}

async function up(detach, withWatch) {
  console.log('[1/4] 依赖容器')
  sh('docker', ['compose', 'up', '-d', '--wait'])
  console.log('[2/4] 数据库迁移')
  sh('go', ['run', './cmd/migrate', '-direction', 'up'])
  console.log('[3/4] 应用进程')
  const started = []
  for (const name of Object.keys(services)) {
    if (await running(name)) {
      console.log(`  ${name} 已在运行，跳过`)
      continue
    }
    const ok = await start(name)
    if (ok) started.push(name)
  }
  console.log('[4/4] 等待就绪')
  for (const name of Object.keys(services)) {
    if (!services[name].health) continue
    const ok = await waitHealthy(name, name === 'web' ? 120 : 20)
    console.log(`  ${name} ${ok ? '就绪' : `未就绪，看 data/logs/${name}.log`}`)
  }
  await status()
  if (withWatch) watchSources()
  if (detach) return
  if (started.length === 0 && !withWatch) return
  // 前台守护：Ctrl+C 时只收本次启动的进程，不影响原本就在跑的服务。
  console.log(`守护中（Ctrl+C 停止本次启动的 ${started.length ? started.join(' / ') : '进程'}）`)
  // top-level await 只有事件循环里有句柄时才挂得住，否则 Node 以 13 退出。
  const keepAlive = setInterval(() => {}, 1 << 30)
  await new Promise((resolve) => {
    const shutdown = () => {
      clearInterval(keepAlive)
      for (const name of started) {
        const pid = readPid(name)
        if (pid) killPid(pid)
        rmSync(pidFile(name), { force: true })
      }
      resolve()
    }
    process.once('SIGINT', shutdown)
    process.once('SIGTERM', shutdown)
  })
}

async function restart(names) {
  for (const name of names) {
    if (!services[name]) {
      console.error(`未知服务：${name}（可选 api / worker / web / all）`)
      process.exit(1)
    }
    // 先编译：编译不过就不动现有进程，避免「停掉了却起不来」。
    if (services[name].kind === 'go' && !build(name)) {
      if (!existsSync(services[name].bin)) {
        console.error(`  ✗ ${name} 无可用二进制，跳过`)
      } else {
        console.error(`  ↳ ${name} 沿用正在运行的旧进程`)
      }
      continue
    }
    await stop(name)
    await start(name, { built: true })
    const ok = await waitHealthy(name, name === 'web' ? 120 : 20)
    console.log(`  ${name} ${ok ? '已恢复' : `未通过健康检查，看 data/logs/${name}.log`}`)
  }
  await status()
}

function watchSources() {
  console.log('监听 Go 源码改动（Ctrl+C 结束）…')
  const pending = new Set()
  let timer = null
  const trigger = (name) => {
    pending.add(name)
    clearTimeout(timer)
    timer = setTimeout(async () => {
      const names = [...pending]
      pending.clear()
      console.log(`\n检测到改动：${names.join(' / ')}，重建重启`)
      try {
        for (const name of names) {
          // 先编译再停旧进程：源码编译不过时保持现有服务继续跑，不制造空窗。
          if (services[name].kind === 'go' && !build(name)) {
            console.error(`  ↳ ${name} 保持原进程不动；修好后保存会自动再试`)
            continue
          }
          await stop(name)
          await start(name, { built: true })
        }
        await status()
      } catch (error) {
        // 重启过程中的任何意外都不该结束监听。
        console.error(`重建重启出错，监听继续：${error.message}`)
      }
    }, 400)
  }
  for (const dir of ['internal', 'cmd']) {
    watch(join(root, dir), { recursive: true }, (_event, file) => {
      if (!file || !String(file).endsWith('.go')) return
      const target = String(file).replaceAll('\\', '/')
      if (target.includes('cmd/worker')) trigger('worker')
      else if (target.includes('cmd/api')) trigger('api')
      else {
        trigger('api')
        trigger('worker')
      }
    })
  }
}

const [command = 'status', ...rest] = process.argv.slice(2)
const detach = rest.includes('--detach')
const withWatch = rest.includes('--watch')
const named = rest.filter((arg) => !arg.startsWith('--'))
const targets = named.length && named[0] !== 'all' ? named : ['api', 'worker', 'web']

switch (command) {
  case 'up':
    await up(detach, withWatch)
    break
  case 'status':
    await status()
    break
  case 'restart':
    await restart(targets)
    break
  case 'stop':
    for (const name of targets) await stop(name)
    await status()
    break
  case 'watch':
    watchSources()
    break
  default:
    console.error('用法：node scripts/dev.mjs <up|status|restart|stop|watch> [api|worker|web|all]')
    process.exit(1)
}
