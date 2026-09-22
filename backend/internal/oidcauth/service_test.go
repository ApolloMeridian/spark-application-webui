package oidcauth

import "testing"

func TestClaimExtractionAndAdminGroupMapping(t *testing.T) {
	claims := map[string]any{
		"preferred_username": "oidc-admin",
		"realm_access":       map[string]any{"groups": []any{"developers", "/k8s-admins"}},
	}
	if got := stringClaim(claims, "preferred_username"); got != "oidc-admin" {
		t.Fatalf("unexpected username claim: %q", got)
	}
	groups := stringListClaim(claims, "realm_access.groups")
	if len(groups) != 2 || !hasMappedGroup(groups, []string{"k8s-admins"}) {
		t.Fatalf("expected nested k8s-admins group to map to admin, got %#v", groups)
	}
	if hasMappedGroup([]string{"users"}, []string{"k8s-admins"}) {
		t.Fatal("ordinary users must not map to admin")
	}
}

func TestStringListClaimAcceptsCommaSeparatedClaim(t *testing.T) {
	groups := stringListClaim(map[string]any{"groups": "users, k8s-admins"}, "groups")
	if len(groups) != 2 || groups[1] != "k8s-admins" {
		t.Fatalf("unexpected groups: %#v", groups)
	}
}
