package admin

import "embed"

//go:embed ui/index.html ui/style.css ui/app.js
var uiFiles embed.FS
