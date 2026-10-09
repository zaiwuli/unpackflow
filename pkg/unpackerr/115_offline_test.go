package unpackerr

import "testing"

func TestParseOfflineLinksMixedTextAndDeduplicates(t *testing.T) {
	text := "说明 ed2k://|file|A.zip|123|ABCDEF|/\n" +
		"ed2k://|file|A-copy.zip|123|abcdef|/ magnet:?xt=urn:btih:112233&dn=B.zip"
	links := parseOfflineLinks(text)
	if len(links) != 2 {
		t.Fatalf("expected two unique links, got %d: %#v", len(links), links)
	}
	if offlineLinkName(links[0]) != "A.zip" {
		t.Fatalf("unexpected ed2k name: %q", offlineLinkName(links[0]))
	}
}
