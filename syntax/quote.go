// Copyright 2017 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package syntax

// Starlark quoted string utilities.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// unesc maps single-letter chars following \ to their actual values.
var unesc = [256]byte{
	'a':  '\a',
	'b':  '\b',
	'f':  '\f',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'v':  '\v',
	'\\': '\\',
	'\'': '\'',
	'"':  '"',
}

// esc maps escape-worthy bytes to the char that should follow \.
var esc = [256]byte{
	'\a': 'a',
	'\b': 'b',
	'\f': 'f',
	'\n': 'n',
	'\r': 'r',
	'\t': 't',
	'\v': 'v',
	'\\': '\\',
	'\'': '\'',
	'"':  '"',
}

// unquote unquotes the quoted string, returning the actual
// string value, whether the original was triple-quoted,
// whether it was a byte string, and an error describing invalid input.
func unquote(quoted string) (s string, triple, isByte bool, err error) {
	// Check for raw prefix: means don't interpret the inner \.
	raw := false
	if strings.HasPrefix(quoted, "r") {
		raw = true
		quoted = quoted[1:]
	}
	// Check for bytes prefix.
	if strings.HasPrefix(quoted, "b") {
		isByte = true
		quoted = quoted[1:]
	}

	if len(quoted) < 2 {
		err = fmt.Errorf("string literal too short")
		return
	}

	if quoted[0] != '"' && quoted[0] != '\'' || quoted[0] != quoted[len(quoted)-1] {
		err = fmt.Errorf("string literal has invalid quotes")
		return
	}

	// Check for triple quoted string.
	quote := quoted[0]
	if len(quoted) >= 6 && quoted[1] == quote && quoted[2] == quote && quoted[:3] == quoted[len(quoted)-3:] {
		triple = true
		quoted = quoted[3 : len(quoted)-3]
	} else {
		quoted = quoted[1 : len(quoted)-1]
	}

	// Now quoted is the quoted data, but no quotes.
	// If we're in raw mode or there are no escapes or
	// carriage returns, we're done.
	var unquoteChars string
	if raw {
		unquoteChars = "\r"
	} else {
		unquoteChars = "\\\r"
	}
	if !strings.ContainsAny(quoted, unquoteChars) {
		s = quoted
		return
	}

	// Otherwise process quoted string.
	// Each iteration processes one escape sequence along with the
	// plain text leading up to it.
	buf := new(strings.Builder)
	for {
		// Remove prefix before escape sequence.
		i := strings.IndexAny(quoted, unquoteChars)
		if i < 0 {
			i = len(quoted)
		}
		buf.WriteString(quoted[:i])
		quoted = quoted[i:]

		if len(quoted) == 0 {
			break
		}

		// Process carriage return.
		if quoted[0] == '\r' {
			buf.WriteByte('\n')
			if len(quoted) > 1 && quoted[1] == '\n' {
				quoted = quoted[2:]
			} else {
				quoted = quoted[1:]
			}
			continue
		}

		// Process escape sequence.
		if len(quoted) == 1 {
			err = fmt.Errorf(`truncated escape sequence \`)
			return
		}

		switch quoted[1] {
		default:
			// In Starlark, like Go, a backslash must escape something.
			// (Python still treats unnecessary backslashes literally,
			// but since 3.6 has emitted a deprecation warning.)
			err = fmt.Errorf("invalid escape sequence \\%c", quoted[1])
			return

		case '\n':
			// Ignore the escape and the line break.
			quoted = quoted[2:]

		case 'a', 'b', 'f', 'n', 'r', 't', 'v', '\\', '\'', '"':
			// One-char escape.
			// Escapes are allowed for both kinds of quotation
			// mark, not just the kind in use.
			buf.WriteByte(unesc[quoted[1]])
			quoted = quoted[2:]

		case '0', '1', '2', '3', '4', '5', '6', '7':
			// Octal escape, up to 3 digits, \OOO.
			n := int(quoted[1] - '0')
			quoted = quoted[2:]
			for i := 1; i < 3; i++ {
				if len(quoted) == 0 || quoted[0] < '0' || '7' < quoted[0] {
					break
				}
				n = n*8 + int(quoted[0]-'0')
				quoted = quoted[1:]
			}
			if n >= 256 {
				// NOTE: Python silently discards the high bit,
				// so that '\541' == '\141' == 'a'.
				// Let's see if we can avoid doing that in BUILD files.
				err = fmt.Errorf(`invalid escape sequence \%03o`, n)
				return
			}
			buf.WriteByte(byte(n))

		case 'x':
			// Hexadecimal escape, exactly 2 digits, \xXX.
			if len(quoted) < 4 {
				err = fmt.Errorf(`truncated escape sequence %s`, quoted)
				return
			}
			n, err1 := strconv.ParseUint(quoted[2:4], 16, 0)
			if err1 != nil {
				err = fmt.Errorf(`invalid escape sequence %s`, quoted[:4])
				return
			}
			buf.WriteByte(byte(n))
			quoted = quoted[4:]

		case 'u', 'U':
			// Unicode code point, 4 (\uXXXX) or 8 (\UXXXXXXXX) hex digits.
			// Unpaired surrogates are allowed (unlike in Go).
			sz := 6
			if quoted[1] == 'U' {
				sz = 10
			}
			if len(quoted) < sz {
				err = fmt.Errorf(`truncated escape sequence %s`, quoted)
				return
			}
			n, err1 := strconv.ParseUint(quoted[2:sz], 16, 0)
			if err1 != nil {
				err = fmt.Errorf(`invalid escape sequence %s`, quoted[:sz])
				return
			}
			if n > unicode.MaxRune {
				err = fmt.Errorf(`code point out of range: %s (max \U%08x)`,
					quoted[:sz], n)
				return
			}
			buf.WriteRune(rune(n))
			quoted = quoted[sz:]
		}
	}

	s = buf.String()
	return
}

// indexByte returns the index of the first instance of b in s, or else -1.
func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// hex is a list of the hexadecimal digits, for use in quoting.
// We always print lower-case hexadecimal.
const hex = "0123456789abcdef"

// quote returns the quoted form of the string value "x".
// If triple is true, quote uses the triple-quoted form """x""".
func quote(unquoted string, triple bool) string {
	q := `"`
	if triple {
		q = `"""`
	}

	buf := new(strings.Builder)
	buf.WriteString(q)

	for i := 0; i < len(unquoted); i++ {
		c := unquoted[i]
		if c == '"' && triple && (i+1 < len(unquoted) && unquoted[i+1] != '"' || i+2 < len(unquoted) && unquoted[i+2] != '"') {
			// Can pass up to two quotes through, because they are followed by a non-quote byte.
			buf.WriteByte(c)
			if i+1 < len(unquoted) && unquoted[i+1] == '"' {
				buf.WriteByte(c)
				i++
			}
			continue
		}
		if triple && c == '\n' {
			// Can allow newline in triple-quoted string.
			buf.WriteByte(c)
			continue
		}
		if c == '\'' {
			// Can allow ' since we always use ".
			buf.WriteByte(c)
			continue
		}
		if esc[c] != 0 {
			buf.WriteByte('\\')
			buf.WriteByte(esc[c])
			continue
		}
		if c < 0x20 || c >= 0x80 {
			// BUILD files are supposed to be Latin-1, so escape all control and high bytes.
			// I'd prefer to use \x here, but Blaze does not implement
			// \x in quoted strings (b/7272572).
			buf.WriteByte('\\')
			buf.WriteByte(hex[c>>6]) // actually octal but reusing hex digits 0-7.
			buf.WriteByte(hex[(c>>3)&7])
			buf.WriteByte(hex[c&7])
			/*
				buf.WriteByte('\\')
				buf.WriteByte('x')
				buf.WriteByte(hex[c>>4])
				buf.WriteByte(hex[c&0xF])
			*/
			continue
		}
		buf.WriteByte(c)
		continue
	}

	buf.WriteString(q)
	return buf.String()
}
