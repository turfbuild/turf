package main

import (
	"os"
	"reflect"
	"testing"
)

// TestEnvPathList checks the os.PathListSeparator parsing used as the --allow-path
// env fallback: unset → nil, single entry, multiple entries, and empty segments
// (leading/trailing/doubled separators) dropped.
func TestEnvPathList(t *testing.T) {
	sep := string(os.PathListSeparator)
	const key = "TURF_TEST_ALLOW_PATH"

	cases := []struct {
		name string
		set  bool
		val  string
		want []string
	}{
		{name: "unset", set: false, want: nil},
		{name: "empty", set: true, val: "", want: nil},
		{name: "single", set: true, val: "/a", want: []string{"/a"}},
		{name: "multiple", set: true, val: "/a" + sep + "/b", want: []string{"/a", "/b"}},
		{name: "drops empty segments", set: true, val: sep + "/a" + sep + sep + "/b" + sep, want: []string{"/a", "/b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.val)
			} else {
				os.Unsetenv(key)
			}
			if got := envPathList(key); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("envPathList(%q=%q) = %#v, want %#v", key, tc.val, got, tc.want)
			}
		})
	}
}
