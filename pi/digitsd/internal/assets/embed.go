package assets

import (
	"embed"
	"io/fs"
)

//go:embed embed/*
var embeddedFS embed.FS

// SubFS returns the embedded filesystem with the "embed/" prefix stripped,
// so paths match what Extractor.Extract expects (e.g., "rootfs/...", "data/...").
// The error from fs.Sub is safe to ignore: "embed" is a compile-time constant
// baked into the binary by the go:embed directive above.
func SubFS() fs.FS {
	sub, _ := fs.Sub(embeddedFS, "embed")
	return sub
}
