// Package web embeds NABU's interface: templates, stylesheets, scripts,
// icons and fonts. Everything is compiled into the binary, so a NABU
// drive needs nothing from the internet to look right.
package web

import "embed"

// Assets are served under /nabu/static/.
//
//go:embed assets
var Assets embed.FS

// Templates are the HTML pages.
//
//go:embed templates/*.html
var Templates embed.FS
