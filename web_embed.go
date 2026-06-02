package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The frontend bundle. Populated at build time by the Dockerfile's
// web-builder stage (`npm run build` → web/dist). The `all:` prefix is
// required to include .gitkeep / other dotfiles so the embed has at least
// one entry when building outside the Dockerfile.
//
//go:embed all:web/dist
var embeddedWeb embed.FS

func webFS() fs.FS {
	sub, err := fs.Sub(embeddedWeb, "web/dist")
	if err != nil {
		return nil
	}
	return sub
}

// serveWeb is the catch-all SPA handler. Any path that doesn't match an
// /api route falls through here. If the requested path resolves to a real
// embedded file, serve it; otherwise fall back to index.html so client-side
// routing can take over.
func serveWeb(w http.ResponseWriter, r *http.Request) {
	root := webFS()
	if root == nil {
		http.NotFound(w, r)
		return
	}
	clean := strings.TrimPrefix(r.URL.Path, "/")
	if clean == "" {
		clean = "index.html"
	}
	if _, err := fs.Stat(root, clean); err == nil {
		http.FileServer(http.FS(root)).ServeHTTP(w, r)
		return
	}
	idx, err := fs.ReadFile(root, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(idx)
}
