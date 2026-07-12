package store

import "testing"

func TestHashBoardUpdateProtocolV4(t *testing.T) {
	update := []byte{1, 2, 3}
	base := int64(42)
	manifest := []string{"ast_a", "ast_b"}

	legacyV3 := hashBoardUpdate(update, &base, manifest, nil)
	if want := "b8bfc6a9928ef2ed536ad34323213b442743ae148e5ceb1be84b9460144f54e7"; legacyV3 != want {
		t.Fatalf("protocol v3 hash = %q, want %q", legacyV3, want)
	}

	v4 := hashBoardUpdate(update, &base, manifest, []string{"ast_b"})
	if want := "1a752bfda47fca37b9cbc2eca862d4f21a4e9f1bd7806991600e90bafe9727d3"; v4 != want {
		t.Fatalf("protocol v4 hash = %q, want %q", v4, want)
	}
	if v4 == legacyV3 {
		t.Fatal("protocol v4 claims did not domain-separate the legacy hash")
	}
	if withoutIntroductions := hashBoardUpdate(update, &base, manifest, []string{}); withoutIntroductions == v4 || withoutIntroductions == legacyV3 {
		t.Fatalf("empty protocol v4 claims were not represented distinctly: %q", withoutIntroductions)
	}
	if differentIntroductions := hashBoardUpdate(update, &base, manifest, []string{"ast_a"}); differentIntroductions == v4 {
		t.Fatal("different introduced asset claims produced the same hash")
	}
	if withoutManifest := hashBoardUpdate(update, nil, nil, []string{}); withoutManifest == hashBoardUpdate(update, &base, []string{}, []string{}) {
		t.Fatal("absent and present-empty manifests produced the same protocol v4 hash")
	}
	if legacyV2 := hashBoardUpdate(update, nil, nil, nil); legacyV2 != hashUpdate(update) {
		t.Fatalf("protocol v2 receipt hash changed: got %q want %q", legacyV2, hashUpdate(update))
	}
}
