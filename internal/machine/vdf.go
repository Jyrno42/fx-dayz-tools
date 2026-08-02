package machine

import (
	"fmt"
	"strings"
	"unicode"
)

// vdfNode is a parsed Valve KeyValues document. Only one of Value or Children
// means anything at a time: a leaf carries Value, a section carries Children.
//
// Steam uses this format for libraryfolders.vdf and appmanifest_<id>.acf, which
// are the only reliable way to find a game whose library is not the default one.
// The subset here is deliberately tiny (quoted keys, quoted values, nested
// braces, // comments) because that is all Steam actually emits.
type vdfNode struct {
	Value    string
	Children map[string]*vdfNode
	// order preserves declaration order, which matters for libraryfolders where
	// the first matching library should win.
	order []string
}

func (n *vdfNode) child(key string) *vdfNode {
	if n == nil || n.Children == nil {
		return nil
	}
	// Steam is inconsistent about case across versions.
	if c, ok := n.Children[strings.ToLower(key)]; ok {
		return c
	}
	return nil
}

// str returns a leaf value by key path, or "" if any step is missing.
func (n *vdfNode) str(path ...string) string {
	cur := n
	for _, key := range path {
		cur = cur.child(key)
		if cur == nil {
			return ""
		}
	}
	return cur.Value
}

// keys returns the child keys in declaration order.
func (n *vdfNode) keys() []string {
	if n == nil {
		return nil
	}
	return n.order
}

func (n *vdfNode) set(key string, child *vdfNode) {
	lower := strings.ToLower(key)
	if n.Children == nil {
		n.Children = map[string]*vdfNode{}
	}
	if _, exists := n.Children[lower]; !exists {
		n.order = append(n.order, lower)
	}
	n.Children[lower] = child
}

// parseVDF parses a Valve KeyValues document into a root section.
func parseVDF(data []byte) (*vdfNode, error) {
	p := &vdfParser{src: []rune(string(data))}
	root := &vdfNode{}
	if err := p.parseSection(root, true); err != nil {
		return nil, err
	}
	return root, nil
}

type vdfParser struct {
	src []rune
	pos int
}

func (p *vdfParser) parseSection(into *vdfNode, top bool) error {
	for {
		p.skipSpace()
		if p.eof() {
			if top {
				return nil
			}
			return fmt.Errorf("vdf: unexpected end of file inside a section")
		}
		if p.peek() == '}' {
			if top {
				return fmt.Errorf("vdf: unexpected '}' at top level")
			}
			p.pos++
			return nil
		}

		key, err := p.parseString()
		if err != nil {
			return err
		}

		p.skipSpace()
		if p.eof() {
			return fmt.Errorf("vdf: key %q has no value", key)
		}

		if p.peek() == '{' {
			p.pos++
			section := &vdfNode{}
			if err := p.parseSection(section, false); err != nil {
				return err
			}
			into.set(key, section)
			continue
		}

		value, err := p.parseString()
		if err != nil {
			return err
		}
		into.set(key, &vdfNode{Value: value})
	}
}

// parseString reads a quoted or bare token, honouring backslash escapes inside
// quotes, since Windows paths in these files come with doubled backslashes.
func (p *vdfParser) parseString() (string, error) {
	if p.eof() {
		return "", fmt.Errorf("vdf: unexpected end of file, expected a value")
	}

	if p.peek() == '"' {
		p.pos++
		var b strings.Builder
		for !p.eof() {
			c := p.src[p.pos]
			switch c {
			case '\\':
				p.pos++
				if p.eof() {
					return "", fmt.Errorf("vdf: trailing escape at end of file")
				}
				switch p.src[p.pos] {
				case 'n':
					b.WriteRune('\n')
				case 't':
					b.WriteRune('\t')
				default:
					b.WriteRune(p.src[p.pos])
				}
				p.pos++
			case '"':
				p.pos++
				return b.String(), nil
			default:
				b.WriteRune(c)
				p.pos++
			}
		}
		return "", fmt.Errorf("vdf: unterminated quoted string")
	}

	start := p.pos
	for !p.eof() && !unicode.IsSpace(p.peek()) && p.peek() != '{' && p.peek() != '}' {
		p.pos++
	}
	if start == p.pos {
		return "", fmt.Errorf("vdf: expected a token at offset %d", p.pos)
	}
	return string(p.src[start:p.pos]), nil
}

func (p *vdfParser) skipSpace() {
	for !p.eof() {
		c := p.peek()
		if unicode.IsSpace(c) {
			p.pos++
			continue
		}
		// // line comment
		if c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/' {
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
			continue
		}
		return
	}
}

func (p *vdfParser) eof() bool  { return p.pos >= len(p.src) }
func (p *vdfParser) peek() rune { return p.src[p.pos] }
