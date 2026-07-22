package services

import "testing"

func TestNewWebAuthnWithOriginsUsesExactConfiguration(t *testing.T) {
	origin := "https://constellation.tailnet.example"
	wa, err := NewWebAuthnWithOrigins("constellation.tailnet.example", []string{origin})
	if err != nil {
		t.Fatalf("NewWebAuthnWithOrigins() error = %v", err)
	}
	if wa.Config.RPID != "constellation.tailnet.example" {
		t.Fatalf("RPID = %q", wa.Config.RPID)
	}
	if len(wa.Config.RPOrigins) != 1 || wa.Config.RPOrigins[0] != origin {
		t.Fatalf("RPOrigins = %#v", wa.Config.RPOrigins)
	}
}
