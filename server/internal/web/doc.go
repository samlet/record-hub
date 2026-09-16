// Package web contains the Record Hub browser-facing authentication boundary.
//
// The package is deliberately independent from the API resource handlers. It
// provides a small BFF adapter for Dex/OIDC: the browser receives only a
// signed, short-lived session cookie, while token exchange and ID-token
// verification stay on the server.
package web
