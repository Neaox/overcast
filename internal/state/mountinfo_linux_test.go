//go:build linux && !nosqlite

package state

import (
	"strings"
	"testing"
)

// TestClassifyMounts pins the longest-prefix /proc/mounts matching and the
// fstype → mount-class buckets the slow-filesystem advisory copy keys on.
func TestClassifyMounts(t *testing.T) {
	const mounts = `overlay / overlay rw,relatime 0 0
proc /proc proc rw 0 0
tmpfs /dev tmpfs rw 0 0
/dev/sdc /data ext4 rw,relatime 0 0
grpcfuse /bind fuse.grpcfuse rw 0 0
/dev/sdc /data/nested/deeper xfs rw 0 0
weirdfs /weird somefs rw 0 0
malformed-line
`
	cases := []struct {
		dir       string
		wantType  string
		wantClass string
	}{
		{"/data", "ext4", "native"},
		{"/data/sub", "ext4", "native"},              // inherits /data, not /
		{"/data/nested/deeper/x", "xfs", "native"},   // longest prefix wins over /data
		{"/data/nested", "ext4", "native"},           // sibling of deeper mount, still /data
		{"/bind", "fuse.grpcfuse", "shared"},         // Docker Desktop file sharing
		{"/bind/sub/dir", "fuse.grpcfuse", "shared"}, //
		{"/datadir", "overlay", "native"},            // NOT /data — prefix must end at a path boundary
		{"/elsewhere", "overlay", "native"},          // falls to root mount
		{"/weird", "somefs", "unknown"},              // unrecognised fstype
		{"/weird/", "somefs", "unknown"},             // trailing slash normalised
	}
	for _, tc := range cases {
		fsType, class := classifyMounts(strings.NewReader(mounts), tc.dir)
		if fsType != tc.wantType || class != tc.wantClass {
			t.Errorf("classifyMounts(%q) = (%q, %q), want (%q, %q)", tc.dir, fsType, class, tc.wantType, tc.wantClass)
		}
	}
}

// TestClassifyMounts_noMatch: an empty or unparseable table yields "unknown"
// rather than a confident wrong answer.
func TestClassifyMounts_noMatch(t *testing.T) {
	fsType, class := classifyMounts(strings.NewReader(""), "/data")
	if fsType != "" || class != "unknown" {
		t.Errorf("classifyMounts(empty) = (%q, %q), want (\"\", \"unknown\")", fsType, class)
	}
}
