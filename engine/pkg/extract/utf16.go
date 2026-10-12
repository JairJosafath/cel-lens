package extract

import (
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
)

// UTF8 returns data as UTF-8 when it is a well-formed UTF-16 stream that starts
// with a byte order mark, without the mark; the line and column of a character
// are the same in both. Any other data, including a UTF-16 stream with an odd
// length or an unpaired surrogate, is returned as it is, for the YAML parser to
// report. The result may share data.
func UTF8(data []byte) []byte {
	if text, ok := decodeUTF16(data); ok {
		return text
	}
	return data
}

// Text returns data as UTF-8 text for finding the lines and columns of the
// parser, which counts a bad character as one. A UTF-16 stream with a byte
// order mark is decoded as UTF8 does, except that an unpaired surrogate, or a
// last odd byte, becomes one U+FFFD, so that a broken character in one place
// does not change the position of any other. Other data is returned as it is.
// The result may share data.
func Text(data []byte) []byte {
	if text, _ := decodeUTF16(data); text != nil {
		return text
	}
	return data
}

// decodeUTF16 decodes the UTF-16 stream data, which starts with a byte order
// mark. ok is false when data is not such a stream or is not well formed; text
// is then the decoding with each bad unit replaced by U+FFFD, or nil when data
// has no byte order mark.
func decodeUTF16(data []byte) (text []byte, ok bool) {
	var order binary.ByteOrder
	switch {
	case len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe:
		order = binary.LittleEndian
	case len(data) >= 2 && data[0] == 0xfe && data[1] == 0xff:
		order = binary.BigEndian
	default:
		return nil, false
	}
	ok = len(data)%2 == 0
	units := make([]uint16, (len(data)-2)/2)
	for i := range units {
		units[i] = order.Uint16(data[2+2*i:])
	}
	runes := make([]rune, 0, len(units)+1)
	for i := 0; i < len(units); i++ {
		u := rune(units[i])
		switch {
		case u >= 0xdc00 && u < 0xe000:
			u, ok = utf8.RuneError, false
		case u >= 0xd800 && u < 0xdc00:
			if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] >= 0xe000 {
				u, ok = utf8.RuneError, false
				break
			}
			u = utf16.DecodeRune(u, rune(units[i+1]))
			i++
		}
		runes = append(runes, u)
	}
	if len(data)%2 != 0 {
		runes = append(runes, utf8.RuneError)
	}
	return []byte(string(runes)), ok
}
