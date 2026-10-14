package main

// A small TOML reader: enough for herdrmon's config.toml and for reading the
// parts of Herdr's config.toml this plugin cares about (theme name, custom
// colours, which sidebar tables exist). No writer.
//
// Supported: comments, [table] and [[array.of.tables]] headers with dotted
// and quoted keys, dotted keys in assignments, basic and literal strings
// (single- and multi-line), integers, floats, booleans, arrays (multi-line,
// nested) and inline tables. Dates are kept as strings. Anything else is a
// *TomlError with the line number.
//
// It is deliberately this lenient reader rather than a strict TOML library:
// `configure` must accept and refuse exactly the configs it always has.
//
// Values: string, float64 (every number, as in JSON), bool, []any, *Table.

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Table keeps its keys in insertion order: [agents.<id>] entries are
// matched in the order they were written.
type Table struct {
	keys []string
	m    map[string]any
}

func NewTable() *Table { return &Table{m: map[string]any{}} }

func (t *Table) Get(k string) (any, bool) {
	if t == nil {
		return nil, false
	}
	v, ok := t.m[k]
	return v, ok
}

func (t *Table) Set(k string, v any) {
	if _, ok := t.m[k]; !ok {
		t.keys = append(t.keys, k)
	}
	t.m[k] = v
}

func (t *Table) Delete(k string) {
	if _, ok := t.m[k]; !ok {
		return
	}
	delete(t.m, k)
	for i, x := range t.keys {
		if x == k {
			t.keys = append(t.keys[:i:i], t.keys[i+1:]...)
			break
		}
	}
}

func (t *Table) Keys() []string {
	if t == nil {
		return nil
	}
	return t.keys
}

func (t *Table) Len() int { return len(t.Keys()) }

// Sub: the table at k, or nil.
func (t *Table) Sub(k string) *Table {
	v, _ := t.Get(k)
	s, _ := v.(*Table)
	return s
}

type TomlError struct {
	Msg  string
	Line int
}

func (e *TomlError) Error() string { return fmt.Sprintf("toml: line %d: %s", e.Line, e.Msg) }

var (
	reBareKey  = regexp.MustCompile(`^[A-Za-z0-9_-]+`)
	reRawValue = regexp.MustCompile(`^[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff},\]}#]+`)
	reInfNan   = regexp.MustCompile(`^[+-]?(inf|nan)$`)
	reInt      = regexp.MustCompile(`^[+-]?\d+$`)
	reHex      = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	reOct      = regexp.MustCompile(`^0o[0-7]+$`)
	reBin      = regexp.MustCompile(`^0b[01]+$`)
	reFloat    = regexp.MustCompile(`^[+-]?\d+(\.\d+)?([eE][+-]?\d+)?$`)
	reDate     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	reTime     = regexp.MustCompile(`^\d{2}:\d{2}`)
	reTimeTail = regexp.MustCompile(`^ \d{2}:\d{2}[^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff},\]}#]*`)
	reHexDigit = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	reEOLSlash = regexp.MustCompile(`^\\[ \t]*\n`)
)

// jsSpace: JavaScript's \s.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

type tomlParser struct {
	src     []rune
	i, line int
	root    *Table
	current *Table
}

type tomlPanic struct{ err *TomlError }

func (p *tomlParser) fail(msg string) {
	panic(tomlPanic{&TomlError{Msg: msg, Line: p.line}})
}

func (p *tomlParser) eof() bool { return p.i >= len(p.src) }

func (p *tomlParser) peek() rune {
	if p.eof() {
		return -1
	}
	return p.src[p.i]
}

func (p *tomlParser) at(k int) rune {
	if k < 0 || k >= len(p.src) {
		return -1
	}
	return p.src[k]
}

func (p *tomlParser) startsWith(s string, k int) bool {
	rs := []rune(s)
	if k+len(rs) > len(p.src) {
		return false
	}
	for n, r := range rs {
		if p.src[k+n] != r {
			return false
		}
	}
	return true
}

func (p *tomlParser) slice(from, to int) string {
	if to > len(p.src) {
		to = len(p.src)
	}
	if from > to {
		return ""
	}
	return string(p.src[from:to])
}

func (p *tomlParser) indexOf(s string, from int) int {
	for k := from; k < len(p.src); k++ {
		if p.startsWith(s, k) {
			return k
		}
	}
	return -1
}

func (p *tomlParser) skipWs() {
	for !p.eof() && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
}

func (p *tomlParser) skipComment() {
	if p.peek() == '#' {
		for !p.eof() && p.src[p.i] != '\n' {
			p.i++
		}
	}
}

// Whitespace, newlines and comments, as allowed inside arrays.
func (p *tomlParser) skipAll() {
	for {
		p.skipWs()
		p.skipComment()
		if p.peek() == '\n' {
			p.i++
			p.line++
			continue
		}
		return
	}
}

func (p *tomlParser) endOfLine() {
	p.skipWs()
	p.skipComment()
	if !p.eof() && p.src[p.i] != '\n' {
		p.fail("unexpected " + jsStringify(string(p.src[p.i])))
	}
}

func (p *tomlParser) basicString() string {
	var out strings.Builder
	if p.startsWith(`"""`, p.i) {
		p.i += 3
		if p.peek() == '\n' {
			p.i++
			p.line++
		}
		for {
			if p.eof() {
				p.fail("unterminated string")
			}
			if p.startsWith(`"""`, p.i) && !p.startsWith(`""""`, p.i) {
				p.i += 3
				return out.String()
			}
			if p.src[p.i] == '\\' {
				if p.at(p.i+1) == '\n' || reEOLSlash.MatchString(p.slice(p.i, p.i+64)) {
					// Line-ending backslash: trim the newline and leading whitespace.
					p.i++
					for !p.eof() && jsSpace(p.src[p.i]) {
						if p.src[p.i] == '\n' {
							p.line++
						}
						p.i++
					}
					continue
				}
				out.WriteString(p.escape())
				continue
			}
			if p.src[p.i] == '\n' {
				p.line++
			}
			out.WriteRune(p.src[p.i])
			p.i++
		}
	}
	p.i++
	for {
		if p.eof() || p.src[p.i] == '\n' {
			p.fail("unterminated string")
		}
		if p.src[p.i] == '"' {
			p.i++
			return out.String()
		}
		if p.src[p.i] == '\\' {
			out.WriteString(p.escape())
			continue
		}
		out.WriteRune(p.src[p.i])
		p.i++
	}
}

func (p *tomlParser) escape() string {
	c := p.at(p.i + 1)
	p.i += 2
	switch c {
	case 'b':
		return "\b"
	case 't':
		return "\t"
	case 'n':
		return "\n"
	case 'f':
		return "\f"
	case 'r':
		return "\r"
	case '"':
		return `"`
	case '\\':
		return `\`
	case 'e':
		return "\x1b"
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		hex := p.slice(p.i, p.i+n)
		if !reHexDigit.MatchString(hex) || len([]rune(hex)) != n {
			p.fail("bad unicode escape")
		}
		p.i += n
		v, _ := strconv.ParseUint(hex, 16, 32)
		if v > 0x10ffff {
			p.fail("bad unicode escape")
		}
		return string(rune(v))
	}
	if c < 0 {
		p.fail(`bad escape \undefined`)
	}
	p.fail(`bad escape \` + string(c))
	return ""
}

func (p *tomlParser) literalString() string {
	if p.startsWith(`'''`, p.i) {
		p.i += 3
		if p.peek() == '\n' {
			p.i++
			p.line++
		}
		end := p.indexOf(`'''`, p.i)
		if end < 0 {
			p.fail("unterminated string")
		}
		stop := end
		for p.at(stop+3) == '\'' {
			stop++
		}
		out := p.slice(p.i, stop)
		p.line += strings.Count(out, "\n")
		p.i = stop + 3
		return out
	}
	p.i++
	end := p.indexOf(`'`, p.i)
	nl := p.indexOf("\n", p.i)
	if end < 0 || (nl >= 0 && nl < end) {
		p.fail("unterminated string")
	}
	out := p.slice(p.i, end)
	p.i = end + 1
	return out
}

func (p *tomlParser) key() string {
	p.skipWs()
	switch p.peek() {
	case '"':
		return p.basicString()
	case '\'':
		return p.literalString()
	}
	m := reBareKey.FindString(p.slice(p.i, p.i+256))
	if m == "" {
		p.fail("expected a key")
	}
	p.i += len([]rune(m))
	return m
}

func (p *tomlParser) dottedKey() []string {
	parts := []string{p.key()}
	for {
		p.skipWs()
		if p.peek() != '.' {
			return parts
		}
		p.i++
		parts = append(parts, p.key())
	}
}

func parseBig(digits string, base int) float64 {
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return math.NaN()
	}
	f, _ := new(big.Float).SetInt(n).Float64()
	return f
}

func (p *tomlParser) value() any {
	p.skipWs()
	switch p.peek() {
	case '"':
		return p.basicString()
	case '\'':
		return p.literalString()
	case '[':
		return p.array()
	case '{':
		return p.inlineTable()
	}
	raw := reRawValue.FindString(p.slice(p.i, p.i+128))
	if raw == "" {
		p.fail("expected a value")
	}
	p.i += len([]rune(raw))
	switch {
	case raw == "true":
		return true
	case raw == "false":
		return false
	case reInfNan.MatchString(raw):
		if strings.HasSuffix(raw, "inf") {
			if raw[0] == '-' {
				return math.Inf(-1)
			}
			return math.Inf(1)
		}
		return math.NaN()
	}
	clean := strings.ReplaceAll(raw, "_", "")
	switch {
	case reInt.MatchString(clean):
		f, _ := strconv.ParseFloat(clean, 64)
		return f
	case reHex.MatchString(clean):
		return parseBig(clean[2:], 16)
	case reOct.MatchString(clean):
		return parseBig(clean[2:], 8)
	case reBin.MatchString(clean):
		return parseBig(clean[2:], 2)
	case reFloat.MatchString(clean):
		f, _ := strconv.ParseFloat(clean, 64)
		return f
	}
	// Dates and times: keep the text, possibly with a space-separated time.
	if reDate.MatchString(raw) || reTime.MatchString(raw) {
		if t := reTimeTail.FindString(p.slice(p.i, p.i+64)); t != "" {
			p.i += len([]rune(t))
			return raw + t
		}
		return raw
	}
	p.fail("bad value " + jsStringify(raw))
	return nil
}

func (p *tomlParser) array() []any {
	p.i++
	out := []any{}
	for {
		p.skipAll()
		if p.peek() == ']' {
			p.i++
			return out
		}
		out = append(out, p.value())
		p.skipAll()
		if p.peek() == ',' {
			p.i++
			continue
		}
		if p.peek() == ']' {
			p.i++
			return out
		}
		p.fail("expected , or ] in array")
	}
}

func (p *tomlParser) inlineTable() *Table {
	p.i++
	out := NewTable()
	p.skipWs()
	if p.peek() == '}' {
		p.i++
		return out
	}
	for {
		k := p.dottedKey()
		p.skipWs()
		if p.peek() != '=' {
			p.fail("expected =")
		}
		p.i++
		p.assign(out, k, p.value())
		p.skipWs()
		if p.peek() == ',' {
			p.i++
			continue
		}
		if p.peek() == '}' {
			p.i++
			return out
		}
		p.fail("expected , or } in inline table")
	}
}

func (p *tomlParser) assign(table *Table, keys []string, v any) {
	t := table
	for _, k := range keys[:len(keys)-1] {
		cur, ok := t.Get(k)
		if !ok {
			n := NewTable()
			t.Set(k, n)
			t = n
			continue
		}
		sub, isTable := cur.(*Table)
		if !isTable {
			p.fail("key " + k + " is not a table")
		}
		t = sub
	}
	last := keys[len(keys)-1]
	if _, ok := t.Get(last); ok {
		p.fail("duplicate key " + last)
	}
	t.Set(last, v)
}

func (p *tomlParser) header() {
	arrayOf := p.startsWith("[[", p.i)
	if arrayOf {
		p.i += 2
	} else {
		p.i++
	}
	keys := p.dottedKey()
	p.skipWs()
	closer := "]"
	if arrayOf {
		closer = "]]"
	}
	if !p.startsWith(closer, p.i) {
		p.fail("unterminated table header")
	}
	p.i += len(closer)
	p.endOfLine()
	t := p.root
	for n, k := range keys {
		last := n == len(keys)-1
		cur, ok := t.Get(k)
		if last && arrayOf {
			if !ok {
				cur = []any{}
			}
			list, isList := cur.([]any)
			if !isList {
				p.fail(k + " is not an array of tables")
			}
			fresh := NewTable()
			t.Set(k, append(list, fresh))
			t = fresh
			continue
		}
		if !ok {
			n := NewTable()
			t.Set(k, n)
			t = n
			continue
		}
		switch c := cur.(type) {
		case []any:
			// Walking through an array of tables descends into its last element.
			var lastEl any
			if len(c) > 0 {
				lastEl = c[len(c)-1]
			}
			sub, isTable := lastEl.(*Table)
			if !isTable {
				p.fail(k + " is not a table")
			}
			t = sub
		case *Table:
			t = c
		default:
			p.fail(k + " is not a table")
		}
	}
	p.current = t
}

// ParseToml parses text into a *Table, or returns a *TomlError.
func ParseToml(text string) (root *Table, err error) {
	p := &tomlParser{src: []rune(strings.ReplaceAll(text, "\r\n", "\n")), line: 1, root: NewTable()}
	p.current = p.root
	defer func() {
		if r := recover(); r != nil {
			tp, ok := r.(tomlPanic)
			if !ok {
				panic(r)
			}
			root, err = nil, tp.err
		}
	}()
	for !p.eof() {
		p.skipAll()
		if p.eof() {
			break
		}
		if p.peek() == '[' {
			p.header()
			continue
		}
		k := p.dottedKey()
		p.skipWs()
		if p.peek() != '=' {
			p.fail("expected =")
		}
		p.i++
		p.assign(p.current, k, p.value())
		p.endOfLine()
	}
	return p.root, nil
}

// jsStringify: JSON.stringify of a string, as JavaScript writes it (used
// where messages quote a value, so they read the same as before).
func jsStringify(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
