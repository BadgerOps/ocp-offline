package mirror

import (
	"regexp"
	"sort"
	"strings"
)

var (
	versionRegex    = regexp.MustCompile(`^(\d+\.\d+\.\d+)/?$`)
	channelRegex    = regexp.MustCompile(`^(stable|fast|candidate|latest)-(\d+\.\d+)/?$`)
	rhcosMinorRegex = regexp.MustCompile(`^(\d+\.\d+)/?$`)
	hrefRegex       = regexp.MustCompile(`href="([^"]+)"`)
)

const (
	DefaultOCPBaseURL   = "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp"
	DefaultRHCOSBaseURL = "https://mirror.openshift.com/pub/openshift-v4/x86_64/dependencies/rhcos"
)

// extractHrefs extracts href values from HTML anchor tags.
// It skips "../" and non-directory links (those not ending with "/"),
// and strips trailing "/".
func extractHrefs(data []byte) []string {
	matches := hrefRegex.FindAllSubmatch(data, -1)
	var result []string
	for _, m := range matches {
		href := string(m[1])
		// Skip parent directory link
		if href == "../" {
			continue
		}
		// Skip non-directory links (must end with "/")
		if !strings.HasSuffix(href, "/") {
			continue
		}
		// Strip trailing "/"
		href = strings.TrimSuffix(href, "/")
		result = append(result, href)
	}
	return result
}

// parseOCPDirectoryListing categorizes hrefs from an OCP mirror directory listing
// into channels (e.g. stable-4.17 -> channel="stable") and releases
// (e.g. 4.17.48 -> channel="release"). It skips "latest-X.Y" entries (redundant)
// and RC/EC builds. Results are sorted descending by version string.
func parseOCPDirectoryListing(data []byte) []OCPVersion {
	hrefs := extractHrefs(data)
	var versions []OCPVersion

	for _, href := range hrefs {
		// Check if it's a channel entry
		if cm := channelRegex.FindStringSubmatch(href); cm != nil {
			channelType := cm[1]
			channelVersion := cm[2]
			// Skip "latest-X.Y" as redundant
			if channelType == "latest" {
				continue
			}
			versions = append(versions, OCPVersion{
				Version: channelVersion,
				Channel: channelType,
			})
			continue
		}

		// Check if it's a release version
		if vm := versionRegex.FindStringSubmatch(href); vm != nil {
			versions = append(versions, OCPVersion{
				Version: vm[1],
				Channel: "release",
			})
			continue
		}

		// Skip everything else (RC/EC builds, bare "latest", "stable", etc.)
	}

	// Sort: channels first by channel name, then releases descending by version
	sort.SliceStable(versions, func(i, j int) bool {
		iIsRelease := versions[i].Channel == "release"
		jIsRelease := versions[j].Channel == "release"

		// Channels before releases
		if !iIsRelease && jIsRelease {
			return true
		}
		if iIsRelease && !jIsRelease {
			return false
		}

		// Within releases, sort descending by version string
		if iIsRelease && jIsRelease {
			return versions[i].Version > versions[j].Version
		}

		// Within channels, sort by channel name
		return versions[i].Channel < versions[j].Channel
	})

	return versions
}

// parseRHCOSMinorVersions extracts minor versions like "4.17" from a RHCOS
// directory listing. Non-version directories like "latest" are skipped.
// Results are sorted ascending.
func parseRHCOSMinorVersions(data []byte) []string {
	hrefs := extractHrefs(data)
	var versions []string

	for _, href := range hrefs {
		if rhcosMinorRegex.MatchString(href) {
			versions = append(versions, href)
		}
	}

	sort.Strings(versions)
	return versions
}

// parseRHCOSBuilds extracts build versions like "4.17.0" from a RHCOS
// directory listing. Non-version directories like "latest" are skipped.
// Results are sorted ascending.
func parseRHCOSBuilds(data []byte) []string {
	hrefs := extractHrefs(data)
	var builds []string

	for _, href := range hrefs {
		if versionRegex.MatchString(href) {
			builds = append(builds, href)
		}
	}

	sort.Strings(builds)
	return builds
}
