package bluetooth

import (
	"errors"
	"unsafe"
)

// MAC represents a MAC address, in little endian format.
type MAC [6]byte

// UnmarshalText unmarshals the text into itself.
// The given MAC address must be exactly 17 characters in the format
// 11:22:33:AA:BB:CC; hexadecimal digits are case-insensitive. Any previous
// content of the receiver is overwritten. If it cannot be unmarshaled, an
// error is returned.
func (mac *MAC) UnmarshalText(s []byte) error {
	if len(s) != 17 {
		return ErrInvalidMAC
	}
	*mac = MAC{}
	for group := 0; group < 6; group++ {
		base := group * 3
		if group < 5 && s[base+2] != ':' {
			return ErrInvalidMAC
		}
		high, err := macHexNibble(s[base])
		if err != nil {
			return err
		}
		low, err := macHexNibble(s[base+1])
		if err != nil {
			return err
		}
		mac[5-group] = high<<4 | low
	}
	return nil
}

func macHexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 0xA, nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 0xA, nil
	default:
		return 0, ErrInvalidMAC
	}
}

// ParseMAC parses the given MAC address, which must be in 11:22:33:AA:BB:CC
// format. If it cannot be parsed, an error is returned.
func ParseMAC(s string) (mac MAC, err error) {
	err = (&mac).UnmarshalText([]byte(s))
	return
}

// String returns a human-readable version of this MAC address, such as
// 11:22:33:AA:BB:CC.
func (mac MAC) String() string {
	buf, _ := mac.MarshalText()
	return unsafe.String(unsafe.SliceData(buf), 17)
}

// Address returns the MAC address in the typical (big-endian) format.
func (mac MAC) Address() [6]uint8 {
	return [6]uint8{mac[5], mac[4], mac[3], mac[2], mac[1], mac[0]}
}

const hexDigit = "0123456789ABCDEF"

// AppendText appends the textual representation of itself to the end of b
// (allocating a larger slice if necessary) and returns the updated slice.
func (mac MAC) AppendText(buf []byte) ([]byte, error) {
	for i := 5; i >= 0; i-- {
		if i != 5 {
			buf = append(buf, ':')
		}
		buf = append(buf, hexDigit[mac[i]>>4])
		buf = append(buf, hexDigit[mac[i]&0xF])
	}
	return buf, nil
}

// MarshalText marshals itself into a string of format 11:22:33:AA:BB:CC.
// It is a simple wrapper of the AppentText method.
func (mac MAC) MarshalText() (text []byte, err error) {
	return mac.AppendText(make([]byte, 0, 17))
}

var ErrInvalidMAC = errors.New("bluetooth: failed to parse MAC address")

// MarshalBinary marshals itself into a binary format.
// This is a simple wrapper of the AppendBinary method
func (mac MAC) MarshalBinary() (data []byte, err error) {
	return mac.AppendBinary(make([]byte, 0, 6))
}

var ErrInvalidBinaryMac = errors.New("bluetooth: failed to unmarshal the binary MAC address")

// UnmarshalBinary unmarshals the mac byte slice into itself.
// It will return the ErrInvalidBinaryMac error if the given slice is not exactually 6 in length.
func (mac *MAC) UnmarshalBinary(data []byte) error {
	if len(data) != 6 {
		return ErrInvalidBinaryMac
	}
	copy(mac[:], data)
	return nil
}

// AppendBinary appends the binary representation of itself to the end of b
// (allocating a larger slice if necessary) and returns the updated slice.
func (mac MAC) AppendBinary(b []byte) ([]byte, error) {
	return append(b, mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]), nil
}
