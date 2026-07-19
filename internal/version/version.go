// Package version exposes build metadata injected at link time.
package version

// Version is set via -ldflags at build time; defaults to "dev" for local builds.
var Version = "dev"
