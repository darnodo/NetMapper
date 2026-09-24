// Package migrations holds the SQL schema, applied by `netmapper migrate`.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
