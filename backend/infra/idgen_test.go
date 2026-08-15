package infra

import (
	"strings"
	"testing"
)

func TestUUIDGen(t *testing.T) {
	g := UUIDGen{}
	id := g.New()
	if len(id) != 36 { // 8-4-4-4-12
		t.Fatalf("uuid length %d: %q", len(id), id)
	}
	if id == g.New() {
		t.Fatal("ids must be unique")
	}
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("not 5 groups: %q", id)
	}
	if parts[2][0] != '4' { // version 4
		t.Fatalf("not a v4 uuid: %q", id)
	}
	if c := parts[3][0]; c != '8' && c != '9' && c != 'a' && c != 'b' { // variant 10xx
		t.Fatalf("wrong uuid variant: %q", id)
	}
}
