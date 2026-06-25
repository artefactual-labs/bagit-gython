package runner

import "embed"

//go:embed files.json main.py
var Source embed.FS
