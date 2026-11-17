package web

import "embed"

// Assets embeds all HTML templates and static CSS/JS files into the binary.
//
//go:embed templates/* static/*
var Assets embed.FS
