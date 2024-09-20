// Package version provides shared version information.
package version

var version string

// Version is set by the build system.
func Version() string {
	return version
}
