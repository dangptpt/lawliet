// Package version holds build version information.
package version

// Version is the Lawliet server version. Overridable at build time:
//
//	go build -ldflags "-X github.com/tranbaohieu/lawliet/internal/version.Version=0.1.0"
var Version = "0.0.1-dev"
