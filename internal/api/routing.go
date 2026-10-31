package api

import "net/http"

// RootHandler wires the API, the SPA and the gateway into one handler.
//
// API calls go to api (including the exact path /api), frontend routes and
// embedded assets go to spa, and everything else is proxied to the upstream by
// gw. SPA page routes are served the index document: the request path is
// rewritten to "/" before spa sees it.
func RootHandler(api, spa, gw http.Handler) http.Handler {
	mux := http.NewServeMux()

	page := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.URL.Path = "/"
		spa.ServeHTTP(w, r)
	})
	mux.Handle("GET /{$}", page)
	for _, path := range []string{"/analysis", "/logs", "/requests", "/pricing", "/settings", "/settings/pricing", "/settings/access", "/settings/data", "/login"} {
		mux.Handle("GET "+path, page)
	}
	mux.Handle("GET /assets/", spa)
	mux.Handle("GET /favicon.svg", spa)

	mux.Handle("/api", api)
	mux.Handle("/api/", api)
	mux.Handle("/", gw)

	return mux
}
