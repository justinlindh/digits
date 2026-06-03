package main

import "net/http"

// captivePortalProbePaths are the URLs Android, iOS/macOS, and Windows fetch to
// decide whether a network is open or behind a captive portal. With AP-mode DNS
// wildcarded to the device, these requests land on our web server; a 302 to the
// portal root is what makes the OS pop its "sign in to network" page. Anything
// else (a 204, or a 404 from the static file server) reads as "no portal", so
// the page never auto-launches.
var captivePortalProbePaths = []string{
	"/generate_204",              // Android, ChromeOS
	"/hotspot-detect.html",       // iOS, macOS
	"/connecttest.txt",           // Windows
	"/library/test/success.html", // older iOS
}

// mountCaptivePortalRedirects registers a 302 redirect to target for each OS
// captive-portal probe path. Both setup (AP) mode and recovery mode mount this
// so the sign-in page auto-launches identically in either.
func mountCaptivePortalRedirects(mux *http.ServeMux, target string) {
	for _, path := range captivePortalProbePaths {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusFound)
		})
	}
}
