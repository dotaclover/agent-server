package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/labstack/echo/v4"
)

//go:embed assets/*
var embedded embed.FS

func Register(e *echo.Echo) {
	sub, _ := fs.Sub(embedded, "assets")
	handler := http.FileServer(http.FS(sub))
	wrapped := echo.WrapHandler(noCacheStaticAssets(handler))
	e.GET("/*", wrapped)
	e.HEAD("/*", wrapped)
}

func noCacheStaticAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
	})
}
