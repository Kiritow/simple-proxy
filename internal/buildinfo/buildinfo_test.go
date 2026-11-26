package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestRead(t *testing.T) {
	defer func(c string) { Commit = c }(Commit)
	Commit = "0123456789abcdef0123456789abcdef01234567"
	if got := Read(); got != (Info{Commit: Commit}) {
		t.Errorf("with Commit set when linking, Read = %+v", got)
	}
}

func TestFromSettings(t *testing.T) {
	for _, tc := range []struct {
		settings []debug.BuildSetting
		want     Info
	}{
		{nil, Info{}},
		{[]debug.BuildSetting{{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "false"}}, Info{Commit: "abc"}},
		{[]debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}, Info{Commit: "abc", Modified: true}},
		// Without a commit, whether it was modified means nothing.
		{[]debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}, Info{}},
	} {
		if got := fromSettings(tc.settings); got != tc.want {
			t.Errorf("fromSettings(%v) = %+v, want %+v", tc.settings, got, tc.want)
		}
	}
}
