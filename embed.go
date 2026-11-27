package knooo

import "embed"

// Assets embeds the built web UI and release changelog for the desktop binary.
//
//go:embed ui/dist CHANGELOG.md
var Assets embed.FS

// Version embeds the application release version string.
//
//go:embed VERSION
var Version string
