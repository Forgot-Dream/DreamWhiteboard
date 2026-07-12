package main

import "testing"

func TestValidBootstrapEmail(t *testing.T) {
	for _, email := range []string{"admin@example.com", "first.last+tag@example.co.uk"} {
		if !validBootstrapEmail(email) {
			t.Fatalf("valid bootstrap email rejected: %q", email)
		}
	}
	for _, email := range []string{"", "not-an-email", "Admin <admin@example.com>", "admin@example.com extra"} {
		if validBootstrapEmail(email) {
			t.Fatalf("invalid bootstrap email accepted: %q", email)
		}
	}
}

func TestValidMigrationFilename(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int64
		valid   bool
	}{
		{name: "001_init.sql", version: 1, valid: true},
		{name: "002_v2_security_crdt.sql", version: 2, valid: true},
		{name: "005_asset_garbage_collection.sql", version: 5, valid: true},
		{name: "006_compacted_update_receipt_retention.sql", version: 6, valid: true},
		{name: "2_v2.sql", version: 2, valid: false},
		{name: "002-V2.sql", version: 2, valid: false},
		{name: "002_.sql", version: 2, valid: false},
		{name: "003_future.txt", version: 3, valid: false},
	} {
		if got := validMigrationFilename(test.name, test.version); got != test.valid {
			t.Fatalf("validMigrationFilename(%q, %d) = %v, want %v", test.name, test.version, got, test.valid)
		}
	}
}
