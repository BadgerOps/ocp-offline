package mirror

// MirrorInfo represents a single mirror endpoint discovered from a metalink or mirror list.
type MirrorInfo struct {
	URL        string `json:"url"`
	Country    string `json:"country"`
	Protocol   string `json:"protocol"`
	Preference int    `json:"preference"`
}

// SpeedResult holds the outcome of a mirror speed test.
type SpeedResult struct {
	URL            string  `json:"url"`
	LatencyMs      int     `json:"latency_ms"`
	ThroughputKBps float64 `json:"throughput_kbps"`
	Error          string  `json:"error,omitempty"`
}

// OCPVersion identifies an OpenShift Container Platform release.
type OCPVersion struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// RHCOSVersion identifies a Red Hat CoreOS version and its available builds.
type RHCOSVersion struct {
	Minor  string   `json:"minor"`
	Builds []string `json:"builds"`
}

// EPELVersionInfo describes an EPEL repository version and its supported architectures.
type EPELVersionInfo struct {
	Version       int      `json:"version"`
	Architectures []string `json:"architectures"`
}
