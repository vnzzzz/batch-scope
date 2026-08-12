package identity

import "testing"

func TestEncodeDecode(t *testing.T) {
	tests := []struct {
		namespace string
		localID   string
	}{
		{namespace: "main", localID: "JOB-A"},
		{namespace: "dr:west", localID: "JOB:A/B"},
		{namespace: "本番", localID: "ジョブ:A"},
	}
	for _, test := range tests {
		encoded := Encode(test.namespace, test.localID)
		namespace, localID, ok := Decode(encoded)
		if !ok || namespace != test.namespace || localID != test.localID {
			t.Fatalf("Decode(%q) = (%q, %q, %v), want (%q, %q, true)", encoded, namespace, localID, ok, test.namespace, test.localID)
		}
	}
}

func TestDecodeRejectsNonCanonicalIdentity(t *testing.T) {
	for _, value := range []string{
		"JOB-A",
		"bsid1:04:main:JOB-A",
		"bsid1:0::JOB-A",
		"bsid1:4:mai:JOB-A",
		"bsid1:4:main:",
	} {
		if _, _, ok := Decode(value); ok {
			t.Fatalf("Decode(%q) unexpectedly succeeded", value)
		}
	}
}

func TestPublicFieldsFallsBackToDefaultNamespace(t *testing.T) {
	namespace, localID := PublicFields("legacy:JOB-A")
	if namespace != DefaultNamespace || localID != "legacy:JOB-A" {
		t.Fatalf("PublicFields legacy = (%q, %q)", namespace, localID)
	}
}
