package bluetooth

import (
	"errors"
	"testing"
)

func TestParseMACAcceptsUpperAndLowerCase(t *testing.T) {
	upper, err := ParseMAC("11:22:33:AA:BB:CC")
	if err != nil {
		t.Fatalf("ParseMAC(uppercase) error = %v", err)
	}
	lower, err := ParseMAC("11:22:33:aa:bb:cc")
	if err != nil {
		t.Fatalf("ParseMAC(lowercase) error = %v", err)
	}
	mixed, err := ParseMAC("11:22:33:aA:Bb:CC")
	if err != nil {
		t.Fatalf("ParseMAC(mixed case) error = %v", err)
	}
	if upper != lower || upper != mixed {
		t.Fatalf("case variants parsed to different MACs: %v %v %v", upper, lower, mixed)
	}
	if got := upper.String(); got != "11:22:33:AA:BB:CC" {
		t.Fatalf("String() = %q, want 11:22:33:AA:BB:CC", got)
	}
	want := MAC{0xCC, 0xBB, 0xAA, 0x33, 0x22, 0x11}
	if upper != want {
		t.Fatalf("parsed bytes = %v, want %v", upper, want)
	}
}

func TestParseMACRejectsMalformedInput(t *testing.T) {
	tests := []string{
		"",
		"11:22:33:AA:BB",      // too short
		"11:22:33:AA:BB:CC:",  // trailing colon
		"11:22::33:AA:BB:CC",  // empty group
		"1122:33:44:AA:BB:CC", // bad grouping
		"11;22;33;AA;BB;CC",   // wrong separator
		"11-22-33-AA-BB-CC",   // wrong separator
		"11:22:33:AA:BB:GG",   // non-hex digit
		" 1:22:33:AA:BB:CC",   // leading space
		"11:22:33:AA:BB:CC11", // too long
	}
	for _, value := range tests {
		if _, err := ParseMAC(value); !errors.Is(err, ErrInvalidMAC) {
			t.Fatalf("ParseMAC(%q) error = %v, want ErrInvalidMAC", value, err)
		}
	}
}

func TestUnmarshalTextOverwritesPreviousContent(t *testing.T) {
	var mac MAC
	if _, err := ParseMAC("FF:FF:FF:FF:FF:FF"); err != nil {
		t.Fatalf("ParseMAC() error = %v", err)
	}
	if err := mac.UnmarshalText([]byte("FF:FF:FF:FF:FF:FF")); err != nil {
		t.Fatalf("UnmarshalText(ones) error = %v", err)
	}
	if err := mac.UnmarshalText([]byte("00:11:22:33:44:55")); err != nil {
		t.Fatalf("UnmarshalText(zeros) error = %v", err)
	}
	want := MAC{0x55, 0x44, 0x33, 0x22, 0x11, 0x00}
	if mac != want {
		t.Fatalf("reused receiver = %v, want %v (previous bits must not merge)", mac, want)
	}
}

func TestMACAddressSetPropagatesParseErrors(t *testing.T) {
	var address MACAddress
	if err := address.Set("bogus"); !errors.Is(err, ErrInvalidMAC) {
		t.Fatalf("Set(bogus) error = %v, want ErrInvalidMAC", err)
	}
	if err := address.Set("11:22:33:AA:BB:CC"); err != nil {
		t.Fatalf("Set(valid) error = %v", err)
	}
	if got := address.MAC.String(); got != "11:22:33:AA:BB:CC" {
		t.Fatalf("address MAC = %q, want 11:22:33:AA:BB:CC", got)
	}
}
