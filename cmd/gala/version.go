package main

import "runtime/debug"

// galaVersion is set at build time with
//
//	-ldflags "-X main.galaVersion=v1.2.3"
var galaVersion string

// version reports the gala version. Order: the ldflags value, then the module
// version baked into the binary (so `go install ...@vX` reports X), then
// "dev".
func version() string {
	if galaVersion != "" {
		return galaVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
