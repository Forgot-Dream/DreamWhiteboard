package filestore

import (
	"path/filepath"
	"testing"
)

func TestStoragePathsRemainWithinRoot(t *testing.T) {
	root := t.TempDir()
	path, err := AssetPath(root, "project-1", "asset.png")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "project-1", "asset.png")
	if path != want {
		t.Fatalf("asset path = %q, want %q", path, want)
	}
	for _, invalid := range []string{"", ".", "..", "../outside", "nested/file", `nested\file`, filepath.Join(root, "absolute")} {
		if _, err := SafeJoin(root, invalid); err == nil {
			t.Fatalf("unsafe segment %q was accepted", invalid)
		}
	}
	if _, err := EnsureWithin(root, filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("path outside storage root was accepted")
	}
}
