package epel

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// RepomdXML represents the structure of repomd.xml
type RepomdXML struct {
	XMLName xml.Name     `xml:"repomd"`
	Data    []RepomdData `xml:"data"`
}

// RepomdData represents a single data element in repomd.xml
type RepomdData struct {
	Type     string         `xml:"type,attr"`
	Location RepomdLocation `xml:"location"`
	Checksum RepomdChecksum `xml:"checksum"`
	Size     int64          `xml:"size"`
}

// RepomdLocation represents the location element
type RepomdLocation struct {
	Href string `xml:"href,attr"`
}

// RepomdChecksum represents the checksum element
type RepomdChecksum struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// ParseRepomd parses repomd.xml data
func ParseRepomd(data []byte) (*RepomdXML, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Entity = map[string]string{}
	decoder.Strict = false

	var repomd RepomdXML
	if err := decoder.Decode(&repomd); err != nil {
		return nil, fmt.Errorf("parsing repomd.xml: %w", err)
	}
	return &repomd, nil
}

// FindPrimaryLocation finds the location of primary.xml.gz in the repomd data
func (r *RepomdXML) FindPrimaryLocation() (string, error) {
	for _, data := range r.Data {
		if data.Type == "primary" {
			if data.Location.Href == "" {
				return "", fmt.Errorf("primary data has empty location href")
			}
			return data.Location.Href, nil
		}
	}
	return "", fmt.Errorf("primary data not found in repomd.xml")
}

// FindPrimaryChecksum finds the checksum of primary.xml.gz in the repomd data
func (r *RepomdXML) FindPrimaryChecksum() (string, error) {
	for _, data := range r.Data {
		if data.Type == "primary" {
			if data.Checksum.Value == "" {
				return "", fmt.Errorf("primary data has empty checksum")
			}
			return data.Checksum.Value, nil
		}
	}
	return "", fmt.Errorf("primary data not found in repomd.xml")
}
