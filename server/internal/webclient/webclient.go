// Package webclient serves the browser viewer (built from web/ by
// web/build.sh) at /web/.
package webclient

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 'self' covers wss:// to the same host in current browsers; it is
		// spelled out for the older ones. blob: is not needed: frames are drawn
		// straight from WebCodecs.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"connect-src 'self' wss://"+r.Host+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// Handler serves /web/ (the page) and /web/<file>.
func Handler() http.Handler {
	sub, _ := fs.Sub(files, "static")
	return secure(http.StripPrefix("/web/", http.FileServer(http.FS(sub))))
}
