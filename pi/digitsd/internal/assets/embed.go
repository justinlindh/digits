package assets

import "embed"

//go:embed embed/*
var EmbeddedFS embed.FS
