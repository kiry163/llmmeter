package api

import "net/url"

// validUpstreamURL mirrors the configuration rule: an absolute http(s) URL
// with a host.
func validUpstreamURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}
