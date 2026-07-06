package tools

import "strings"

// buildURL appends urlPath to baseURL, inserting a single slash between
// them when neither side already provides one and avoiding a double slash when
// both do.
func buildURL(baseURL, urlPath string) string {
	fullURL := baseURL
	if !strings.HasSuffix(fullURL, "/") && !strings.HasPrefix(urlPath, "/") {
		fullURL += "/"
	} else if strings.HasSuffix(fullURL, "/") && strings.HasPrefix(urlPath, "/") {
		urlPath = strings.TrimPrefix(urlPath, "/")
	}
	return fullURL + urlPath
}
