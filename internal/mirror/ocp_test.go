package mirror

import (
	"testing"
)

const ocpTestHTML = `<html><body>
<a href="4.14.41/">4.14.41/</a>
<a href="4.17.48/">4.17.48/</a>
<a href="4.18.3/">4.18.3/</a>
<a href="stable-4.17/">stable-4.17/</a>
<a href="fast-4.17/">fast-4.17/</a>
<a href="candidate-4.18/">candidate-4.18/</a>
<a href="latest-4.17/">latest-4.17/</a>
<a href="latest/">latest/</a>
<a href="stable/">stable/</a>
<a href="4.18.0-rc.2/">4.18.0-rc.2/</a>
<a href="4.12.0-ec.1/">4.12.0-ec.1/</a>
</body></html>`

const rhcosMinorTestHTML = `<html><body>
<a href="../">../</a>
<a href="4.14/">4.14/</a>
<a href="4.17/">4.17/</a>
<a href="4.18/">4.18/</a>
<a href="latest/">latest/</a>
<a href="pre-release/">pre-release/</a>
</body></html>`

const rhcosBuildsTestHTML = `<html><body>
<a href="../">../</a>
<a href="4.17.0/">4.17.0/</a>
<a href="4.17.1/">4.17.1/</a>
<a href="4.17.42/">4.17.42/</a>
<a href="latest/">latest/</a>
</body></html>`

func TestParseOCPVersions(t *testing.T) {
	versions := parseOCPDirectoryListing([]byte(ocpTestHTML))

	var channels, releases []OCPVersion
	for _, v := range versions {
		if v.Channel == "release" {
			releases = append(releases, v)
		} else {
			channels = append(channels, v)
		}
	}

	// Expect 3 channels: stable-4.17, fast-4.17, candidate-4.18
	// latest-4.17 should be skipped
	if len(channels) != 3 {
		t.Errorf("expected 3 channels, got %d: %+v", len(channels), channels)
	}

	// Check that we have exactly 1 stable, 1 fast, 1 candidate
	channelTypes := map[string]int{}
	for _, c := range channels {
		channelTypes[c.Channel]++
	}
	for _, ct := range []string{"stable", "fast", "candidate"} {
		if channelTypes[ct] != 1 {
			t.Errorf("expected 1 %s channel, got %d", ct, channelTypes[ct])
		}
	}

	// Expect at least 3 releases: 4.14.41, 4.17.48, 4.18.3
	// RC and EC builds should be excluded
	if len(releases) < 3 {
		t.Errorf("expected at least 3 releases, got %d: %+v", len(releases), releases)
	}

	// Verify RC/EC are excluded
	for _, r := range releases {
		if r.Version == "4.18.0-rc.2" || r.Version == "4.12.0-ec.1" {
			t.Errorf("RC/EC build should be excluded: %s", r.Version)
		}
	}

	// Verify descending sort for releases
	if len(releases) >= 2 {
		for i := 0; i < len(releases)-1; i++ {
			if releases[i].Version < releases[i+1].Version {
				t.Errorf("releases not sorted descending: %s < %s", releases[i].Version, releases[i+1].Version)
			}
		}
	}
}

func TestParseRHCOSMinorVersions(t *testing.T) {
	versions := parseRHCOSMinorVersions([]byte(rhcosMinorTestHTML))

	if len(versions) != 3 {
		t.Fatalf("expected 3 minor versions, got %d: %v", len(versions), versions)
	}

	expected := []string{"4.14", "4.17", "4.18"}
	for i, v := range versions {
		if v != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, v)
		}
	}
}

func TestParseRHCOSBuilds(t *testing.T) {
	builds := parseRHCOSBuilds([]byte(rhcosBuildsTestHTML))

	if len(builds) != 3 {
		t.Fatalf("expected 3 builds, got %d: %v", len(builds), builds)
	}

	expected := []string{"4.17.0", "4.17.1", "4.17.42"}
	for i, v := range builds {
		if v != expected[i] {
			t.Errorf("expected %s at index %d, got %s", expected[i], i, v)
		}
	}
}
