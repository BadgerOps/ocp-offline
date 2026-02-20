package epel

import (
	"encoding/xml"
	"fmt"
)

// PrimaryXML represents the root metadata element of primary.xml
type PrimaryXML struct {
	XMLName  xml.Name   `xml:"metadata"`
	Packages int        `xml:"packages,attr"`
	Package  []Package  `xml:"package"`
}

// Package represents a single rpm package in primary.xml
type Package struct {
	Type     string       `xml:"type,attr"`
	Name     string       `xml:"name"`
	Arch     string       `xml:"arch"`
	Version  Version      `xml:"version"`
	Checksum Checksum     `xml:"checksum"`
	Size     SizeInfo     `xml:"size"`
	Location Location     `xml:"location"`
}

// Version represents the version element
type Version struct {
	Epoch string `xml:"epoch,attr"`
	Ver   string `xml:"ver,attr"`
	Rel   string `xml:"rel,attr"`
}

// Checksum represents the checksum element
type Checksum struct {
	Type  string `xml:"type,attr"`
	Pkgid string `xml:"pkgid,attr"`
	Value string `xml:",chardata"`
}

// SizeInfo represents the size element with package and installed size
type SizeInfo struct {
	Package   int64 `xml:"package,attr"`
	Installed int64 `xml:"installed,attr"`
}

// Location represents the location element
type Location struct {
	Href string `xml:"href,attr"`
}

// PackageInfo is a simplified representation of package metadata for syncing
type PackageInfo struct {
	Name     string
	Arch     string
	Version  string
	Release  string
	Checksum string
	Size     int64
	Location string
}

// ParsePrimary parses primary.xml data and returns package information
func ParsePrimary(data []byte) (*PrimaryXML, error) {
	var metadata PrimaryXML
	err := xml.Unmarshal(data, &metadata)
	if err != nil {
		return nil, fmt.Errorf("parsing primary.xml: %w", err)
	}
	return &metadata, nil
}

// ExtractPackages converts parsed Package structs to PackageInfo structs
func (p *PrimaryXML) ExtractPackages() []PackageInfo {
	packages := make([]PackageInfo, 0, len(p.Package))
	for _, pkg := range p.Package {
		packages = append(packages, PackageInfo{
			Name:     pkg.Name,
			Arch:     pkg.Arch,
			Version:  pkg.Version.Ver,
			Release:  pkg.Version.Rel,
			Checksum: pkg.Checksum.Value,
			Size:     pkg.Size.Package,
			Location: pkg.Location.Href,
		})
	}
	return packages
}
