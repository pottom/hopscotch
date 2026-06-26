package admin

import "embed"

//go:embed ui/index.html ui/style.css ui/app.js ui/docs ui/vendor
var uiFiles embed.FS
