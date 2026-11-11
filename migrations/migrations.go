// Package migrations embeds versioned SQL so a single binary can run them.
package migrations

import "embed"

// FS contains all *.up.sql / *.down.sql files in this directory.
//
//go:embed *.sql
var FS embed.FS
