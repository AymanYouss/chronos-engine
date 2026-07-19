// Package migrations embeds the ordered SQL schema files applied at startup.
package migrations

import "embed"

// FS holds every migration, applied in lexical filename order.
//
//go:embed *.sql
var FS embed.FS
