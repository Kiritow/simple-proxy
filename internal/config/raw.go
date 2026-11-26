package config

import (
	"strconv"
	"strings"
)

// A raw site is configured by its raw configuration, written by hand: nginx
// configuration for the http context, such as server blocks, which is
// generated as is, except for references to certificates:
//
//	ssl_certificate     {{cert "name"}};
//	ssl_certificate_key {{key "name"}};
//
// They are replaced by the quoted paths of the certificate chain and the
// private key of the certificate of that name, a Go string literal. Unlike
// paths written by hand, they follow certificates as they are replaced and
// renewed.

// RawRef is a reference to a certificate in a raw configuration.
type RawRef struct {
	Kind       string // "cert" for the certificate chain, "key" for the private key
	Name       string // of the certificate
	Start, End int    // byte offsets of the reference in the configuration
}

// RawInfo describes a raw configuration.
type RawInfo struct {
	// ServerNames are the names of its server blocks, in order, without
	// duplicates.
	ServerNames []string
	Refs        []RawRef
}

// ParseRaw checks the syntax of a raw configuration, as far as it can
// without nginx: blocks, quotes and semicolons, and references to
// certificates. nginx checks the rest when the configuration is applied.
func ParseRaw(text string) (RawInfo, *Problem) {
	var info RawInfo
	// The words of the current directive, and the directives of the
	// enclosing blocks, as far as needed to find server names.
	var words []string
	var blocks []string // names of the enclosing blocks
	var opened []int    // lines where they were opened
	line := 1
	seen := map[string]bool{}

	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == ';':
			if len(words) == 0 {
				return info, &Problem{Code: RawUnexpected, Value: strconv.Itoa(line)}
			}
			if len(words) > 1 && words[0] == "server_name" && len(blocks) == 1 && blocks[0] == "server" {
				for _, name := range words[1:] {
					if name != "_" && name != "" && !seen[name] {
						seen[name] = true
						info.ServerNames = append(info.ServerNames, name)
					}
				}
			}
			words = words[:0]
			i++
		case c == '{' && strings.HasPrefix(text[i:], "{{"):
			end := strings.Index(text[i:], "}}")
			if end < 0 {
				return info, &Problem{Code: RawBadRef, Value: firstLine(text[i:])}
			}
			ref, ok := parseRef(text[i+2 : i+end])
			if !ok {
				return info, &Problem{Code: RawBadRef, Value: text[i : i+end+2]}
			}
			ref.Start, ref.End = i, i+end+2
			info.Refs = append(info.Refs, ref)
			words = append(words, "")
			i += end + 2
		case c == '{':
			if len(words) == 0 {
				return info, &Problem{Code: RawUnexpected, Value: strconv.Itoa(line)}
			}
			blocks = append(blocks, words[0])
			opened = append(opened, line)
			words = words[:0]
			i++
		case c == '}':
			if len(words) > 0 {
				return info, &Problem{Code: RawMissingSemicolon, Value: strconv.Itoa(line)}
			}
			if len(blocks) == 0 {
				return info, &Problem{Code: RawUnexpected, Value: strconv.Itoa(line)}
			}
			blocks, opened = blocks[:len(blocks)-1], opened[:len(opened)-1]
			i++
		default:
			word, n, lines, ok := readWord(text[i:])
			if !ok {
				return info, &Problem{Code: RawUnterminatedQuote, Value: strconv.Itoa(line)}
			}
			words = append(words, word)
			line += lines
			i += n
		}
	}
	switch {
	case len(words) > 0:
		return info, &Problem{Code: RawMissingSemicolon, Value: strconv.Itoa(line)}
	case len(blocks) > 0:
		return info, &Problem{Code: RawUnclosedBlock, Value: strconv.Itoa(opened[len(opened)-1])}
	}
	return info, nil
}

// readWord reads a word as nginx does: up to a space, a semicolon or an
// opening brace that is not part of a variable such as ${name}, with quoted
// and escaped characters. It returns the word without quotes, its length in
// text, the line breaks in it, and false if a quote is not closed.
func readWord(text string) (word string, n, lines int, ok bool) {
	var b strings.Builder
	i := 0
	if q := text[0]; q == '"' || q == '\'' {
		for i = 1; i < len(text); i++ {
			switch text[i] {
			case '\\':
				if i+1 < len(text) {
					i++
					b.WriteByte(text[i])
				}
				continue
			case '\n':
				lines++
			case q:
				return b.String(), i + 1, lines, true
			}
			b.WriteByte(text[i])
		}
		return "", 0, 0, false
	}
	for ; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\\' && i+1 < len(text):
			i++
			b.WriteByte(text[i])
			continue
		case c == '{' && i > 0 && text[i-1] == '$':
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == ';' || c == '{':
			return b.String(), i, lines, true
		}
		b.WriteByte(c)
	}
	return b.String(), i, lines, true
}

// parseRef parses the inside of a reference, such as `cert "name"`.
func parseRef(s string) (RawRef, bool) {
	kind, quoted, _ := strings.Cut(strings.TrimSpace(s), " ")
	name, err := strconv.Unquote(strings.TrimSpace(quoted))
	if err != nil || (kind != "cert" && kind != "key") || name == "" {
		return RawRef{}, false
	}
	return RawRef{Kind: kind, Name: name}, true
}

// RawCertRef returns a reference to the certificate chain or, if key is
// set, the private key of the certificate of the given name.
func RawCertRef(name string, key bool) string {
	kind := "cert"
	if key {
		kind = "key"
	}
	return "{{" + kind + " " + strconv.Quote(name) + "}}"
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// Names returns the server names of s: those of its server_name, or of
// the server blocks of its raw configuration.
func (s Server) Names() []string {
	if s.Raw != nil {
		info, _ := ParseRaw(*s.Raw)
		return info.ServerNames
	}
	return strings.Fields(s.ServerName)
}

// UsesCert reports whether s uses the certificate of the given name.
func (s Server) UsesCert(name string) bool {
	if s.Raw != nil {
		info, _ := ParseRaw(*s.Raw)
		for _, ref := range info.Refs {
			if ref.Name == name {
				return true
			}
		}
		return false
	}
	return s.UseHTTPS() && s.Certificate() == name
}
