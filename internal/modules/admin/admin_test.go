package admin

import "testing"

func TestValidateRemoteURLRejectsInternalRanges(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/file.mp4",
		"http://localhost/admin",
		"http://10.0.0.8/x",
		"http://192.168.1.10/x",
		"http://172.16.5.4/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.100.100.200/latest/meta-data/",
		"http://[::1]/x",
		"ftp://example.com/x",
		"http://user:pass@example.com/x",
	}
	for _, raw := range blocked {
		if err := ValidateRemoteURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected by the SSRF boundary", raw)
		}
	}
}

func TestValidateRemoteURLAllowsPublicAddress(t *testing.T) {
	for _, raw := range []string{"https://1.1.1.1/video.mp4", "http://8.8.8.8/x"} {
		if err := ValidateRemoteURL(raw); err != nil {
			t.Fatalf("expected %q to be allowed, got %v", raw, err)
		}
	}
	if err := ValidateRemoteURL(""); err != nil {
		t.Fatalf("empty base URL must be allowed for API connectors, got %v", err)
	}
}

func TestRolePermissionMatrix(t *testing.T) {
	if !hasPermission("super_admin", PermSettingsWrite) {
		t.Fatal("super_admin must hold every permission")
	}
	if hasPermission("readonly", PermReviewDecide) ||
		hasPermission("readonly", PermConnectorWrite) {
		t.Fatal("readonly must not hold write permissions")
	}
	if !hasPermission("reviewer", PermReviewDecide) {
		t.Fatal("reviewer must be able to decide reviews")
	}
	if hasPermission("reviewer", PermConnectorWrite) {
		t.Fatal("reviewer must not configure connectors")
	}
	if !hasPermission("operator", PermTopicWrite) ||
		hasPermission("operator", PermReviewDecide) {
		t.Fatal("operator owns topics but not moderation decisions")
	}
	for _, role := range []string{"super_admin", "reviewer", "operator", "readonly"} {
		if len(permissionsFor(role)) == 0 {
			t.Fatalf("role %s has an empty permission set", role)
		}
	}
	if len(permissionsFor("unknown")) != 0 {
		t.Fatal("unknown roles must be denied by default")
	}
}

func TestNewDeltaLabelCarriesArrowAndSign(t *testing.T) {
	up := makeDelta(120, 100)
	if up.Direction != "up" || up.Label != "↑ +20.0%" {
		t.Fatalf("unexpected up delta: %+v", up)
	}
	down := makeDelta(80, 100)
	if down.Direction != "down" || down.Label != "↓ -20.0%" {
		t.Fatalf("unexpected down delta: %+v", down)
	}
	if flat := makeDelta(5, 0); flat.Direction != "up" {
		t.Fatalf("a non-zero value from zero must read as up, got %+v", flat)
	}
}
