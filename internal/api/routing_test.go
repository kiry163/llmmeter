package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kiry163/llmmeter/internal/api"
)

func TestRootRouting(t *testing.T) {
	var got string
	apiH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = "api" })
	spaH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = "spa" })
	gwH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = "gateway" })
	root := api.RootHandler(apiH, spaH, gwH)

	for _, c := range []struct{ path, want string }{
		{"/api/overview", "api"},
		{"/api", "api"},
		{"/assets/index.js", "spa"},
		{"/", "spa"},
		{"/logs", "spa"},
		{"/analysis", "spa"},
		{"/requests", "spa"},
		{"/pricing", "spa"},
		{"/settings", "spa"},
		{"/setup", "spa"},
		{"/login", "spa"},
		{"/favicon.svg", "spa"},
		{"/v1/chat/completions", "gateway"},
		{"/unknown", "gateway"},
	} {
		got = ""
		root.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", c.path, nil))
		if got != c.want {
			t.Fatalf("%s routed to %q want %q", c.path, got, c.want)
		}
	}
}
