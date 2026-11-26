// Package buildinfo tells which commit the program was built from.
package buildinfo

import "runtime/debug"

// Commit is the hash of the commit the program was built from, if set when
// linking, as the image does, since its build context has no .git:
//
//	go build -ldflags "-X github.com/kiritow/simple-proxy/internal/buildinfo.Commit=$(git rev-parse HEAD)"
//
// Otherwise Read takes it from what go build records of the git checkout it
// runs in.
var Commit string

// Info is what is known of the build.
type Info struct {
	Commit   string `json:"commit,omitempty"`   // the full hash; empty if unknown
	Modified bool   `json:"modified,omitempty"` // built with uncommitted changes
}

// Read returns what is known of the build of the running program.
func Read() Info {
	if Commit != "" {
		return Info{Commit: Commit}
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{}
	}
	return fromSettings(bi.Settings)
}

func fromSettings(settings []debug.BuildSetting) Info {
	var info Info
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			info.Commit = s.Value
		case "vcs.modified":
			info.Modified = s.Value == "true"
		}
	}
	if info.Commit == "" {
		return Info{}
	}
	return info
}
