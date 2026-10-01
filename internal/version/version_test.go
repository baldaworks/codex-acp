package version

import "testing"

func TestStringDefaultsToDev(t *testing.T) {
	orig := buildVersion
	t.Cleanup(func() {
		buildVersion = orig
	})

	buildVersion = ""
	if got := String(); got != "dev" {
		t.Fatalf("String() = %q, want %q", got, "dev")
	}
}

func TestStringReturnsInjectedVersion(t *testing.T) {
	orig := buildVersion
	t.Cleanup(func() {
		buildVersion = orig
	})

	buildVersion = "1.5.6"
	if got := String(); got != "1.5.6" {
		t.Fatalf("String() = %q, want %q", got, "1.5.6")
	}
}
