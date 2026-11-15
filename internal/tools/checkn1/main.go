// Command checkn1 扫描 Go 源码里的 N+1：循环体内直接调用 db / tx / pool 的
// Query、QueryRow、Exec。规范见 PERFORMANCE.md。
//
// 只查「直接调用」；循环里调用某个仓库方法、方法内部再查库的情况，需要在
// 评审时人工确认（静态分析拿不到跨函数的数据流）。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"internal"}
	}
	var findings []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" || entry.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			findings = append(findings, scanFile(path)...)
			return nil
		})
	}
	for _, finding := range findings {
		fmt.Fprintln(os.Stderr, finding)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\n共 %d 处循环内直接查库；改用 ANY($1) / unnest / JOIN 后重跑。\n", len(findings))
		os.Exit(1)
	}
	fmt.Println("checkn1: OK（没有循环内直接查库）")
}

func scanFile(path string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return []string{fmt.Sprintf("%s: 解析失败: %v", path, err)}
	}
	var out []string
	ast.Inspect(file, func(node ast.Node) bool {
		var body *ast.BlockStmt
		switch item := node.(type) {
		case *ast.ForStmt:
			body = item.Body
		case *ast.RangeStmt:
			body = item.Body
		}
		if body == nil {
			return true
		}
		ast.Inspect(body, func(inner ast.Node) bool {
			// 嵌套循环交给外层 ast.Inspect 单独处理，避免同一处报两次。
			switch inner.(type) {
			case *ast.ForStmt, *ast.RangeStmt:
				return false
			}
			call, ok := inner.(*ast.CallExpr)
			if !ok || !isDBCall(call) {
				return true
			}
			position := fset.Position(call.Pos())
			if allowed(fset, file, position.Line) {
				return true
			}
			out = append(out, fmt.Sprintf("%s:%d: 循环内直接查库（N+1），改用批量查询",
				path, position.Line))
			return true
		})
		return true
	})
	return out
}

func isDBCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch selector.Sel.Name {
	case "Query", "QueryRow", "Exec":
	default:
		return false
	}
	return receiverIsDB(selector.X)
}

func receiverIsDB(expr ast.Expr) bool {
	switch item := expr.(type) {
	case *ast.Ident:
		switch item.Name {
		case "db", "tx", "pool", "conn":
			return true
		}
	case *ast.SelectorExpr:
		// h.db / r.db / h.repo.db 都只看最后一段。
		switch item.Sel.Name {
		case "db", "tx", "pool":
			return true
		}
	}
	return false
}

// 允许在调用行或上一行写 // n+1-allow: 原因 说明为什么必须逐条执行。
func allowed(fset *token.FileSet, file *ast.File, line int) bool {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			pos := fset.Position(comment.Pos())
			if pos.Line != line && pos.Line != line-1 {
				continue
			}
			if strings.Contains(comment.Text, "n+1-allow") {
				return true
			}
		}
	}
	return false
}
