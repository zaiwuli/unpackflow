package unpackerr

import "testing"

func TestParse115MappingsAcceptsFallbackAndLegacyFormats(t *testing.T) {
	mappings := parse115Mappings([]string{
		"100 => 200 => /115open/fallback/movie",
		"300 => /115open/legacy",
		"not a mapping",
		" => 400 => /115open/invalid",
	})
	if len(mappings) != 2 {
		t.Fatalf("mapping count = %d, want 2", len(mappings))
	}
	if mappings[0].SourceCID != "100" || mappings[0].FallbackCID != "200" || mappings[0].CD2Path != "/115open/fallback/movie" {
		t.Fatalf("unexpected fallback mapping: %#v", mappings[0])
	}
	if !mappings[0].fallbackEnabled() {
		t.Fatal("three-part mapping must enable fallback")
	}
	if mappings[1].SourceCID != "300" || mappings[1].FallbackCID != "" || mappings[1].CD2Path != "/115open/legacy" {
		t.Fatalf("unexpected legacy mapping: %#v", mappings[1])
	}
	if mappings[1].fallbackEnabled() {
		t.Fatal("legacy mapping must not attempt a move without a fallback CID")
	}
}

func TestN115ExtractStatus(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"number", map[string]any{"data": map[string]any{"unzip_status": float64(1)}}, 1},
		{"string", map[string]any{"data": map[string]any{"unzip_status": "6"}}, 6},
		{"missing", map[string]any{"data": map[string]any{}}, -1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := n115ExtractStatus(test.body); got != test.want {
				t.Fatalf("status = %d, want %d", got, test.want)
			}
		})
	}
}

func TestN115FileKeyChangesForDifferentFiles(t *testing.T) {
	first := n115FileKey("100", n115File{FID: "1", Size: 10})
	second := n115FileKey("100", n115File{FID: "2", Size: 10})
	if first == second {
		t.Fatal("different 115 files must not share a running-task key")
	}
}

func TestN115SeparateFolderName(t *testing.T) {
	if got := n115ExtractFolderName("sample.tar.gz"); got != "sample" {
		t.Fatalf("tar.gz folder = %q", got)
	}
	if got := n115ExtractFolderName("movie.7z"); got != "movie" {
		t.Fatalf("archive folder = %q", got)
	}
}

func TestN115QueueHasSingleSlot(t *testing.T) {
	u := New()
	if cap(u.n115Queue) != 1 {
		t.Fatalf("115 queue capacity = %d, want 1", cap(u.n115Queue))
	}
	u.n115Queue <- struct{}{}
	select {
	case u.n115Queue <- struct{}{}:
		t.Fatal("a second 115 extraction must wait for the active extraction")
	default:
	}
	<-u.n115Queue
}
