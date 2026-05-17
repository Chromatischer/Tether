package tether

// Version is the current application release identifier used for user-facing
// changelog ranges. Release builds may override it with -ldflags.
var Version = "v0.5"

func CurrentVersion() string {
	return NormalizeVersion(Version)
}
