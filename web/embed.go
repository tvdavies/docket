// Package webassets exposes the committed Docket web build to the Go service.
package webassets

import "embed"

// Dist contains the production Vite output. Keeping it committed preserves a
// self-contained go build/install with no frontend toolchain at runtime.
//
//go:embed dist
var Dist embed.FS
