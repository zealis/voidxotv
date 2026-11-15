// Package buildinfo 提供构建版本信息，供健康检查、启动日志与排障使用。
//
// 版本号的事实来源是仓库根目录的 VERSION 文件；改版本号统一走
// `node scripts/bump-version.mjs <版本|patch|minor|major>`，它会同时更新
// 这里、VERSION 与两个 package.json，避免多处不一致。
package buildinfo

// Version 是本次构建的版本号，与 VERSION 文件保持一致。
// 发布流水线可用
// -ldflags "-X github.com/yejie/voidxotv/internal/platform/buildinfo.Version=x.y.z" 覆盖。
var Version = "0.27.3"

// Commit 是构建对应的提交；本地开发保持 dev，发布构建由 ldflags 注入。
var Commit = "dev"
