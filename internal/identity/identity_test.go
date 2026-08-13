package identity

import "testing"

func TestCanonicalRoundTripAndDelimiterSafety(t *testing.T) {
	t.Parallel()

	namespace := "main:prod/東京"
	localID := "JOB:A/B.01"
	id := Canonical(namespace, localID)
	gotNamespace, gotLocalID, err := Decode(id)
	if err != nil {
		t.Fatal(err)
	}
	if gotNamespace != namespace || gotLocalID != localID {
		t.Fatalf("Decode(%q) = (%q, %q), want (%q, %q)", id, gotNamespace, gotLocalID, namespace, localID)
	}
	if id != Canonical(namespace, localID) {
		t.Fatal("canonical ID is not deterministic")
	}
	if id == Canonical("main", "prod:JOB:A/B.01") {
		t.Fatal("different namespace/local ID pairs collided")
	}
}

func TestDecodeRejectsNonCanonicalID(t *testing.T) {
	t.Parallel()
	if _, _, err := Decode("JOB-A"); err == nil {
		t.Fatal("Decode accepted a legacy ID")
	}
}

func TestPublicIdentityLegacyFallback(t *testing.T) {
	t.Parallel()
	namespace, localID := PublicIdentity("JOB-A", "", "")
	if namespace != "default" || localID != "JOB-A" {
		t.Fatalf("PublicIdentity legacy = (%q, %q)", namespace, localID)
	}
}
