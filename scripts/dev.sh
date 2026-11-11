#!/usr/bin/env bash
# 夜街 · 本地一键启动（Git Bash / WSL / Linux / macOS）
# 依赖：Go 1.27+、Node 22.22.3+、Docker。开发凭据仅用于本机，不得用于生产。
set -euo pipefail

cd "$(dirname "$0")/.."

export YJ_ENV=${YJ_ENV:-dev}
export YJ_HTTP_ADDR=${YJ_HTTP_ADDR:-127.0.0.1:18080}
export YJ_DATABASE_URL=${YJ_DATABASE_URL:-postgres://yejie:yejie_dev@127.0.0.1:5432/yejie?sslmode=disable}
export YJ_REDIS_ADDR=${YJ_REDIS_ADDR:-127.0.0.1:6380}
export YJ_S3_ENDPOINT=${YJ_S3_ENDPOINT:-http://127.0.0.1:9100}
export YJ_S3_PUBLIC_ENDPOINT=${YJ_S3_PUBLIC_ENDPOINT:-http://127.0.0.1:9100}
export YJ_S3_BUCKET=${YJ_S3_BUCKET:-yejie-dev}
export YJ_S3_ACCESS_KEY=${YJ_S3_ACCESS_KEY:-yejie_dev}
export YJ_S3_SECRET_KEY=${YJ_S3_SECRET_KEY:-yejie_dev_secret}
export YJ_FFMPEG_PATH=${YJ_FFMPEG_PATH:-$(pwd)/.tools/ffmpeg/ffmpeg.exe}
export YJ_FFPROBE_PATH=${YJ_FFPROBE_PATH:-$(pwd)/.tools/ffmpeg/ffprobe.exe}
export YJ_WORKER_TEMP_DIR=${YJ_WORKER_TEMP_DIR:-$(pwd)/data/transcode-tmp}
export YJ_API_ORIGIN=${YJ_API_ORIGIN:-http://127.0.0.1:18080}
export YJ_ALLOWED_ORIGINS=${YJ_ALLOWED_ORIGINS:-http://127.0.0.1:3000}
export API_ORIGIN=${API_ORIGIN:-http://127.0.0.1:18080}
export YJ_SITE_URL=${YJ_SITE_URL:-http://127.0.0.1:${PORT:-3000}}
# Nuxt 运行时配置：canonical / OG / sitemap 的公开 origin 唯一来源。
export NUXT_PUBLIC_SITE_URL=${NUXT_PUBLIC_SITE_URL:-$YJ_SITE_URL}
export YJ_DELIVERY_TOKEN=${YJ_DELIVERY_TOKEN:-dev-internal-token}
export YJ_DELIVERY_NUXT_ORIGIN=${YJ_DELIVERY_NUXT_ORIGIN:-http://127.0.0.1:${PORT:-3000}}
export YJ_DELIVERY_IDLE_INTERVAL=${YJ_DELIVERY_IDLE_INTERVAL:-2s}
export YJ_ADMIN_BOOTSTRAP_PASSWORD=${YJ_ADMIN_BOOTSTRAP_PASSWORD:-dev-admin-password}
export YJ_STATS_INTERVAL=${YJ_STATS_INTERVAL:-5m}
export YJ_IMPORTS_INTERVAL=${YJ_IMPORTS_INTERVAL:-5s}
export PORT=${PORT:-3000}
export HOST=${HOST:-127.0.0.1}

echo "[1/4] 启动 PostgreSQL / Redis / S3 测试桶"
docker compose up -d --wait

echo "[2/4] 执行数据库迁移（可重复执行）"
go run ./cmd/migrate -direction up

echo "[3/4] 启动 Go API / Worker / Nuxt"
go run ./cmd/api &
api_pid=$!
go run ./cmd/worker &
worker_pid=$!
npm --prefix apps/web run dev &
web_pid=$!

cleanup() {
  echo "正在停止开发进程…"
  kill "$api_pid" "$worker_pid" "$web_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "[4/4] 就绪：Web http://127.0.0.1:${PORT} · API http://127.0.0.1:18080/health/ready"
wait
