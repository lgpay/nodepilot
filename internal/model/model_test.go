package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInboundJSONDoesNotExposeRealityPrivateKey(t *testing.T) {
	b, err := json.Marshal(Inbound{RealityPrivateKey: "secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-key") || strings.Contains(string(b), "reality_private_key") {
		t.Fatalf("REALITY private key leaked in JSON: %s", b)
	}
}
