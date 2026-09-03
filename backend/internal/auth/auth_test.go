package auth

import (
	"strings"
	"testing"
)

func TestIssueMintsUniqueIdentities(t *testing.T) {
	d := NewDevTokens()
	t1, i1, err := d.Issue("Ann")
	if err != nil {
		t.Fatal(err)
	}
	t2, i2, err := d.Issue("Ann")
	if err != nil {
		t.Fatal(err)
	}
	if t1 == t2 {
		t.Fatal("two sessions must not share a token")
	}
	// Same display name, different players: the id is chosen by the server.
	if i1.PlayerID == i2.PlayerID {
		t.Fatal("two sessions must not share a player id")
	}
	if i1.Name != "Ann" || i2.Name != "Ann" {
		t.Fatal("the display name should be preserved")
	}
	if len(t1) < 16 {
		t.Fatalf("token %q is too short to be unguessable", t1)
	}
}

func TestAuthenticateRejectsAnythingNotIssued(t *testing.T) {
	d := NewDevTokens()
	tok, want, _ := d.Issue("Bo")

	got, err := d.Authenticate(tok)
	if err != nil || got != want {
		t.Fatalf("a valid token resolved to %+v, %v", got, err)
	}
	for _, bad := range []string{"", "   ", "not-a-token", strings.ToUpper(tok)} {
		if _, err := d.Authenticate(bad); err == nil {
			t.Fatalf("token %q should have been refused", bad)
		}
	}
}

func TestNameValidation(t *testing.T) {
	d := NewDevTokens()
	if _, id, err := d.Issue("   "); err != nil || id.Name != "Player" {
		t.Fatalf("a blank name should fall back, got %q %v", id.Name, err)
	}
	if _, _, err := d.Issue(strings.Repeat("x", 25)); err == nil {
		t.Fatal("an over-long name should be refused")
	}
	if _, _, err := d.Issue("bad\x00name"); err == nil {
		t.Fatal("control characters should be refused")
	}
}

func TestIssueFixedIsStableForReconnects(t *testing.T) {
	d := NewDevTokens()
	want := d.IssueFixed("tok-1", "p1", "Ann")
	for i := 0; i < 3; i++ {
		got, err := d.Authenticate("tok-1")
		if err != nil || got != want {
			t.Fatalf("a fixed token must resolve the same way every time: %+v %v", got, err)
		}
	}
}
