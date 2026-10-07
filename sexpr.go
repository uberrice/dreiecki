package main

import (
	"fmt"
	"strings"
)

// Node is a parsed S-expression: either an atom (Atom set, List nil) or a list.
type Node struct {
	Atom   string
	Quoted bool
	List   []*Node
	IsList bool
}

// Head returns the first atom of a list, e.g. "symbol" for (symbol ...).
func (n *Node) Head() string {
	if n == nil || !n.IsList || len(n.List) == 0 || n.List[0].IsList {
		return ""
	}
	return n.List[0].Atom
}

// Arg returns the i-th argument (after the head) as a string, or "".
func (n *Node) Arg(i int) string {
	if n == nil || !n.IsList || i+1 >= len(n.List) || n.List[i+1].IsList {
		return ""
	}
	return n.List[i+1].Atom
}

// Child returns the first child list with the given head.
func (n *Node) Child(head string) *Node {
	if n == nil {
		return nil
	}
	for _, c := range n.List {
		if c.IsList && c.Head() == head {
			return c
		}
	}
	return nil
}

// Children returns all child lists with the given head.
func (n *Node) Children(head string) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	for _, c := range n.List {
		if c.IsList && c.Head() == head {
			out = append(out, c)
		}
	}
	return out
}

// String serializes the node canonically (single line), used for comparisons.
func (n *Node) String() string {
	var b strings.Builder
	n.write(&b)
	return b.String()
}

func (n *Node) write(b *strings.Builder) {
	if !n.IsList {
		if n.Quoted {
			fmt.Fprintf(b, "%q", n.Atom)
		} else {
			b.WriteString(n.Atom)
		}
		return
	}
	b.WriteByte('(')
	for i, c := range n.List {
		if i > 0 {
			b.WriteByte(' ')
		}
		c.write(b)
	}
	b.WriteByte(')')
}

// ParseSexpr parses a single top-level S-expression.
func ParseSexpr(src string) (*Node, error) {
	p := &parser{src: src}
	p.skipSpace()
	n, err := p.parse()
	if err != nil {
		return nil, err
	}
	return n, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) parse() (*Node, error) {
	if p.pos >= len(p.src) {
		return nil, fmt.Errorf("unexpected end of input")
	}
	switch c := p.src[p.pos]; c {
	case '(':
		p.pos++
		n := &Node{IsList: true}
		for {
			p.skipSpace()
			if p.pos >= len(p.src) {
				return nil, fmt.Errorf("unterminated list")
			}
			if p.src[p.pos] == ')' {
				p.pos++
				return n, nil
			}
			child, err := p.parse()
			if err != nil {
				return nil, err
			}
			n.List = append(n.List, child)
		}
	case ')':
		return nil, fmt.Errorf("unexpected ')' at offset %d", p.pos)
	case '"':
		p.pos++
		var b strings.Builder
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			if ch == '\\' && p.pos+1 < len(p.src) {
				p.pos++
				switch e := p.src[p.pos]; e {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(e)
				}
				p.pos++
				continue
			}
			if ch == '"' {
				p.pos++
				return &Node{Atom: b.String(), Quoted: true}, nil
			}
			b.WriteByte(ch)
			p.pos++
		}
		return nil, fmt.Errorf("unterminated string")
	default:
		start := p.pos
		for p.pos < len(p.src) {
			ch := p.src[p.pos]
			if ch == '(' || ch == ')' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
				break
			}
			p.pos++
		}
		return &Node{Atom: p.src[start:p.pos]}, nil
	}
}
