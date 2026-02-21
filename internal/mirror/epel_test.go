package mirror

import (
	"testing"
)

const sampleMetalinkXML = `<?xml version="1.0" encoding="utf-8"?>
<metalink version="3.0" xmlns="http://www.metalinker.org/" type="dynamic" pubdate="Wed, 18 Feb 2026 12:00:00 GMT" generator="mirrormanager" xmlns:mm0="http://fedorahosted.org/mirrormanager">
  <files>
    <file name="repomd.xml">
      <mm0:timestamp>1708257600</mm0:timestamp>
      <size>4096</size>
      <verification>
        <hash type="sha256">abc123</hash>
      </verification>
      <resources maxconnections="1">
        <url protocol="https" type="https" location="US" preference="100">https://mirror1.example.com/pub/epel/9/Everything/x86_64/repodata/repomd.xml</url>
        <url protocol="https" type="https" location="DE" preference="90">https://mirror2.example.com/pub/epel/9/Everything/x86_64/repodata/repomd.xml</url>
        <url protocol="http" type="http" location="JP" preference="80">http://mirror3.example.com/pub/epel/9/Everything/x86_64/repodata/repomd.xml</url>
      </resources>
    </file>
  </files>
</metalink>`

func TestParseMetalink(t *testing.T) {
	mirrors, err := parseMetalink([]byte(sampleMetalinkXML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mirrors) != 3 {
		t.Fatalf("expected 3 mirrors, got %d", len(mirrors))
	}

	// Should be sorted by preference descending (100, 90, 80)
	if mirrors[0].Preference != 100 {
		t.Errorf("expected first mirror preference 100, got %d", mirrors[0].Preference)
	}
	if mirrors[1].Preference != 90 {
		t.Errorf("expected second mirror preference 90, got %d", mirrors[1].Preference)
	}
	if mirrors[2].Preference != 80 {
		t.Errorf("expected third mirror preference 80, got %d", mirrors[2].Preference)
	}

	// URL should have /repodata/repomd.xml stripped
	expectedURL := "https://mirror1.example.com/pub/epel/9/Everything/x86_64"
	if mirrors[0].URL != expectedURL {
		t.Errorf("expected URL %q, got %q", expectedURL, mirrors[0].URL)
	}

	// Check country
	if mirrors[0].Country != "US" {
		t.Errorf("expected country US, got %s", mirrors[0].Country)
	}
	if mirrors[1].Country != "DE" {
		t.Errorf("expected country DE, got %s", mirrors[1].Country)
	}
	if mirrors[2].Country != "JP" {
		t.Errorf("expected country JP, got %s", mirrors[2].Country)
	}

	// Check protocol
	if mirrors[0].Protocol != "https" {
		t.Errorf("expected protocol https, got %s", mirrors[0].Protocol)
	}
	if mirrors[2].Protocol != "http" {
		t.Errorf("expected protocol http, got %s", mirrors[2].Protocol)
	}
}

func TestParseMetalinkEmpty(t *testing.T) {
	emptyXML := `<?xml version="1.0" encoding="utf-8"?>
<metalink version="3.0" xmlns="http://www.metalinker.org/" type="dynamic">
  <files>
    <file name="repomd.xml">
      <resources maxconnections="1">
      </resources>
    </file>
  </files>
</metalink>`

	mirrors, err := parseMetalink([]byte(emptyXML))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mirrors) != 0 {
		t.Errorf("expected 0 mirrors, got %d", len(mirrors))
	}
}

func TestParseMetalinkInvalid(t *testing.T) {
	_, err := parseMetalink([]byte("this is not valid xml"))
	if err == nil {
		t.Error("expected error for invalid XML, got nil")
	}
}
