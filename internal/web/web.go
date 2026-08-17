package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html style.css app.js manifest.json
var embeddedFiles embed.FS

// GetFS returns the embedded file system for the web GUI
func GetFS() fs.FS {
	return embeddedFiles
}
