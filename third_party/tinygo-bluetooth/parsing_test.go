package bluetooth

import (
	"testing"
)

// malformedPayload fills buf so the second field starts at index 28 and claims
// 30 content bytes: the claimed end (58) lies far beyond buf.len (31), so the
// legacy unchecked parsing indexed past both the payload length and the
// backing [31]byte array.
func malformedPayload(fieldType byte) *rawAdvertisementPayload {
	buf := &rawAdvertisementPayload{}
	buf.data[0] = 27 // first field: 27 content bytes, harmless type
	buf.data[1] = 0x00
	buf.data[28] = 30        // second field claims 30 content bytes
	buf.data[29] = fieldType // its type byte
	buf.len = 31
	return buf
}

func TestRawPayloadManufacturerDataSurvivesMalformedLength(t *testing.T) {
	buf := malformedPayload(0xff)
	got := buf.ManufacturerData()
	if len(got) != 0 {
		t.Fatalf("ManufacturerData() = %v, want malformed field rejected", got)
	}
}

func TestRawPayloadServiceDataSurvivesMalformedLength(t *testing.T) {
	for _, fieldType := range []byte{0x16, 0x20, 0x21} {
		buf := malformedPayload(fieldType)
		got := buf.ServiceData()
		if len(got) != 0 {
			t.Fatalf("ServiceData() for type 0x%02X = %v, want malformed field rejected", fieldType, got)
		}
	}
}

func TestRawPayloadServiceDataRejectsUndersizedFields(t *testing.T) {
	// A 0x20 field must carry type + 4 UUID bytes (fieldLength >= 5) and a
	// 0x21 field type + 16 UUID bytes (fieldLength >= 17); shorter claims
	// used to read UUID bytes outside the field.
	buf32 := &rawAdvertisementPayload{}
	buf32.data[0] = 3 // type + 2 data bytes only
	buf32.data[1] = 0x20
	buf32.data[2] = 0xAB
	buf32.data[3] = 0xCD
	buf32.len = 4
	if got := buf32.ServiceData(); len(got) != 0 {
		t.Fatalf("ServiceData() = %v, want undersized 0x20 field skipped", got)
	}

	buf128 := &rawAdvertisementPayload{}
	buf128.data[0] = 3
	buf128.data[1] = 0x21
	buf128.data[2] = 0xAB
	buf128.data[3] = 0xCD
	buf128.len = 4
	if got := buf128.ServiceData(); len(got) != 0 {
		t.Fatalf("ServiceData() = %v, want undersized 0x21 field skipped", got)
	}

	// A well-formed 0x16 field with no payload remains valid.
	buf16 := &rawAdvertisementPayload{}
	buf16.data[0] = 3 // type + 2 UUID bytes
	buf16.data[1] = 0x16
	buf16.data[2] = 0x0F
	buf16.data[3] = 0x18
	buf16.len = 4
	got := buf16.ServiceData()
	if len(got) != 1 || got[0].UUID != New16BitUUID(0x180F) || len(got[0].Data) != 0 {
		t.Fatalf("ServiceData() = %v, want one valid 0x16 element", got)
	}

	bufMan := &rawAdvertisementPayload{}
	bufMan.data[0] = 4 // type + company id + 1 data byte
	bufMan.data[1] = 0xff
	bufMan.data[2] = 0x4c
	bufMan.data[3] = 0x00
	bufMan.data[4] = 0x55
	bufMan.len = 5
	gotMan := bufMan.ManufacturerData()
	if len(gotMan) != 1 || gotMan[0].CompanyID != 0x004C || len(gotMan[0].Data) != 1 {
		t.Fatalf("ManufacturerData() = %v, want one valid manufacturer element", gotMan)
	}
}

func TestUUIDUnmarshalTextResetsReusedReceiver(t *testing.T) {
	original, err := ParseUUID("00001234-0000-1000-8000-00805f9b34fb")
	if err != nil {
		t.Fatalf("ParseUUID() error = %v", err)
	}

	// A reused receiver must not OR the previous value into the new one.
	reused := original
	if err := reused.UnmarshalText([]byte("1234")); err != nil {
		t.Fatalf("UnmarshalText() error = %v", err)
	}
	if want := New16BitUUID(0x1234); reused != want {
		t.Fatalf("reused UnmarshalText() = %v, want %v", reused, want)
	}

	// The same applies when a 128-bit value overwrites a prior value.
	if err := reused.UnmarshalText([]byte("00005678-0000-1000-8000-00805f9b34fb")); err != nil {
		t.Fatalf("UnmarshalText() error = %v", err)
	}
	want128, err := ParseUUID("00005678-0000-1000-8000-00805f9b34fb")
	if err != nil {
		t.Fatalf("ParseUUID() error = %v", err)
	}
	if reused != want128 {
		t.Fatalf("reused UnmarshalText() = %v, want %v", reused, want128)
	}
}
