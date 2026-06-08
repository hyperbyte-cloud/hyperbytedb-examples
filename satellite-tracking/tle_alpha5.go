package main

// Space-Track Alpha-5: NORAD 100,000–339,999 in fixed-width TLE/3le use a letter
// in the first catalog column. go-satellite's parser expects 5 decimal digits, so
// we decode the true NORAD, rewrite lines 1–2 with a 5-digit surrogate in [2:7]
// (NORAD%100k, never 0) and recompute the TLE checksum in column 69. initl() does not
// use satnum, so the surrogate is only to satisfy parseInt.

import (
	"fmt"
	"strconv"
	"strings"
)

// alpha5Table maps the first character of a 5-char Alpha-5 catalog field to its decade digit (10–33).
// I and O are not used. See https://www.space-track.org/documentation#/tle-alpha5
var alpha5Table = map[byte]int{
	'A': 10, 'B': 11, 'C': 12, 'D': 13, 'E': 14, 'F': 15, 'G': 16, 'H': 17,
	'J': 18, 'K': 19, 'L': 20, 'M': 21, 'N': 22, 'P': 23, 'Q': 24, 'R': 25, 'S': 26, 'T': 27, 'U': 28, 'V': 29, 'W': 30, 'X': 31, 'Y': 32, 'Z': 33,
}

// noradFromTLELine1 returns the public catalog (NORAD) id from a TLE line 1, columns 3–7 (0-based [2:7]).
func noradFromTLELine1(l1 string) (int, error) {
	if len(l1) < 7 {
		return 0, fmt.Errorf("line1 too short")
	}
	f := l1[2:7] // 5 char fixed
	trimS := strings.TrimSpace(f)
	if len(trimS) < 1 {
		return 0, fmt.Errorf("empty catalog field")
	}
	// All digits: classic NORAD (usually < 100000)
	if trimS[0] >= '0' && trimS[0] <= '9' {
		return strconv.Atoi(trimS)
	}
	// Alpha-5: 5 bytes like E8493, A0000
	if len(f) != 5 {
		return 0, fmt.Errorf("alpha5: bad width")
	}
	hi, ok := alpha5Table[f[0]]
	if !ok {
		return 0, fmt.Errorf("alpha5: bad letter %q", f[0])
	}
	lo, err := strconv.Atoi(strings.TrimLeft(f[1:5], " "))
	if err != nil {
		return 0, err
	}
	if lo < 0 || lo > 9999 {
		return 0, fmt.Errorf("alpha5: bad suffix %d", lo)
	}
	return hi*10000 + lo, nil
}

// needsTLEPatch is true if the catalog field uses Alpha-5 (first non-space is a letter).
func needsTLEPatch(l1 string) bool {
	if len(l1) < 7 {
		return false
	}
	for _, c := range l1[2:7] {
		if c != ' ' {
			if c >= 'A' && c <= 'Z' {
				return true
			}
			return false
		}
	}
	return false
}

func fake5DigitCatalog(norad int) int {
	// 5-digit field for go-satellite; avoid 0 (e.g. A0000 = 100000).
	m := norad % 100000
	if m == 0 {
		return 1
	}
	return m
}

// tleChecksum68: standard TLE checksum over first 68 characters (0-based 0:68).
func tleChecksum68(buf []byte) int {
	var sum int
	for i := 0; i < 68 && i < len(buf); i++ {
		c := buf[i]
		if c == '-' {
			sum++
			continue
		}
		if c >= '0' && c <= '9' {
			sum += int(c - '0')
		}
	}
	return sum % 10
}

// patchTLEPairForParser rewrites the catalog in lines 1 and 2 to 5 decimal digits, fixes checksums.
// norad is the true catalog id (including Alpha-5 decoded).
func patchTLEPairForParser(l1, l2 string, norad int) (p1, p2 string) {
	if len(l1) < 69 || len(l2) < 69 {
		return l1, l2
	}
	cat := fmt.Sprintf("%05d", fake5DigitCatalog(norad))
	p1 = patchTLELineCatalog(l1, cat)
	p2 = patchTLELineCatalog(l2, cat)
	return p1, p2
}

func patchTLELineCatalog(line, cat5 string) string {
	if len(cat5) != 5 || len(line) < 8 {
		return line
	}
	if len(line) < 69 {
		return line
	}
	b := make([]byte, 69)
	copy(b, line[:69])
	copy(b[2:7], []byte(cat5))
	ck := tleChecksum68(b[:68])
	b[68] = byte('0' + ck)
	return string(b)
}
