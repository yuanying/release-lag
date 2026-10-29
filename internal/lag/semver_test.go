package lag

import "testing"

func TestLatestRelease(t *testing.T) {
	cases := []struct {
		name string
		tags []string
		want string
	}{
		{"none", nil, ""},
		{"not semver", []string{"latest", "release-1", "v1.2", "1.2.3.4"}, ""},
		{"numeric, not lexical", []string{"v1.9.0", "v1.10.0", "v1.2.0"}, "v1.10.0"},
		{"without v", []string{"1.0.0", "0.9.9"}, "1.0.0"},
		{"mixed v and no v", []string{"v1.0.0", "2.0.0", "v1.5.0"}, "2.0.0"},
		{"prerelease skipped", []string{"v1.0.0", "v2.0.0-rc.1", "v1.1.0-beta"}, "v1.0.0"},
		{"only prereleases", []string{"v1.0.0-rc.1"}, ""},
		{"build metadata counts", []string{"v1.0.0", "v1.1.0+build.5"}, "v1.1.0+build.5"},
		{"leading zeros are not semver", []string{"v1.0.0", "v01.2.0"}, "v1.0.0"},
		{"same version, v preferred", []string{"1.2.3", "v1.2.3"}, "v1.2.3"},
		{"patch and minor", []string{"v0.1.10", "v0.1.9", "v0.2.0"}, "v0.2.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := latestRelease(tc.tags); got != tc.want {
				t.Errorf("latestRelease(%v) = %q, want %q", tc.tags, got, tc.want)
			}
		})
	}
}
