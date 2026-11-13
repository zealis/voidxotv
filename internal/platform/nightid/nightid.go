// Package nightid 统一夜街号的形态：纯数字，补零到 6 位（000001），
// 超过 6 位（1000000 起）按真实位数展示，不再补零。
//
// 夜街号就是 users.id 的字符串形式。展示与解析集中在这里，
// 避免「后端返回 34、用户输入 34、数据库存 000034」三处各写一套补零规则。
package nightid

import (
	"strconv"
	"strings"
)

// Width 是夜街号的补零位数。SQL 侧的 lpad(id::text, 6, '0')（注册插入、
// 迁移回填、列表按号查询）必须与这里保持一致。
const Width = 6

// Format 把用户 ID 格式化成夜街号：1 → "000001"，1000000 → "1000000"。
func Format(id int64) string {
	return pad(strconv.FormatInt(id, 10))
}

// Normalize 把用户输入的号补零到 6 位，用于兼容输入 "34" 的旧链接与手输入。
// 含非数字字符、或本身就超过 6 位时原样返回——绝不做截断：
// Postgres 的 lpad('1000000', 6, '0') 会截成 '100000'，不能拿它当匹配依据。
func Normalize(value string) string {
	return pad(strings.TrimSpace(value))
}

func pad(value string) string {
	if value == "" || len(value) >= Width {
		return value
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return value
		}
	}
	return strings.Repeat("0", Width-len(value)) + value
}
