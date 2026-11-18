// Package console serves the web console: static files embedded in the binary.
package console

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// Index serves the single page.
func Index() http.Handler {
	return secure(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := files.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "console missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}))
}

// Static serves /static/*.
func Static() http.Handler {
	sub, _ := fs.Sub(files, "static")
	return secure(http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
}
