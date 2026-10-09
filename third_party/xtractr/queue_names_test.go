package xtractr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetTempFolderFinalNameUsesNumberedSibling(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, "A.zip_unpackerred")
	if err := os.Mkdir(temporary, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "A(1)"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	x := NewQueue(&Config{Suffix: "_unpackerred", TryNames: true})
	got := x.getTempFolderFinalName(&Response{
		Output: temporary,
		X:      &Xtract{Name: filepath.Join(root, "A.zip")},
	})
	want := filepath.Join(root, "A(2)")
	if got != want {
		t.Fatalf("unexpected final folder: got %q want %q", got, want)
	}
}
