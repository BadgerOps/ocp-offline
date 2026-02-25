package mirror

import (
	"encoding/xml"
	"sort"
	"strings"
)

// EPELVersions lists the EPEL major versions to discover mirrors for.
var EPELVersions = []int{7, 8, 9, 10}

// EPELArchitectures lists the CPU architectures supported by EPEL repositories.
var EPELArchitectures = []string{"x86_64", "aarch64", "ppc64le", "s390x"}

// metalinkXML structs model the Metalink 3.0 XML format.
type metalinkXML struct {
	XMLName xml.Name         `xml:"metalink"`
	Files   metalinkFilesXML `xml:"files"`
}

type metalinkFilesXML struct {
	File []metalinkFileXML `xml:"file"`
}

type metalinkFileXML struct {
	Name      string               `xml:"name,attr"`
	Resources metalinkResourcesXML `xml:"resources"`
}

type metalinkResourcesXML struct {
	URLs []metalinkURLXML `xml:"url"`
}

type metalinkURLXML struct {
	Protocol   string `xml:"protocol,attr"`
	Type       string `xml:"type,attr"`
	Location   string `xml:"location,attr"`
	Preference int    `xml:"preference,attr"`
	URL        string `xml:",chardata"`
}

// repomdSuffix is stripped from metalink URLs to obtain the base repository URL.
const repomdSuffix = "/repodata/repomd.xml"

// parseMetalink parses a Metalink 3.0 XML document and returns discovered mirrors
// sorted by preference in descending order.
func parseMetalink(data []byte) ([]MirrorInfo, error) {
	var ml metalinkXML
	if err := xml.Unmarshal(data, &ml); err != nil {
		return nil, err
	}

	var mirrors []MirrorInfo
	for _, file := range ml.Files.File {
		for _, u := range file.Resources.URLs {
			url := strings.TrimSpace(u.URL)
			url = strings.TrimSuffix(url, repomdSuffix)

			mirrors = append(mirrors, MirrorInfo{
				URL:        url,
				Country:    u.Location,
				Protocol:   u.Protocol,
				Preference: u.Preference,
			})
		}
	}

	sort.Slice(mirrors, func(i, j int) bool {
		return mirrors[i].Preference > mirrors[j].Preference
	})

	return mirrors, nil
}
