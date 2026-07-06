package toolset

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// htmlToMarkdown converts an HTML document into a compact markdown-ish text
// representation. It is intentionally conservative: it extracts visible text and
// a handful of structural elements (headings, links, lists, emphasis) and drops
// everything else (scripts, styles, attributes, etc).
//
// This is NOT a full-fidelity converter. Its purpose is to turn fetched pages
// into readable text that is cheaper to summarize and easier to sanitize. When
// the input does not parse as HTML the original string is returned unchanged.
func htmlToMarkdown(input string) string {
	doc, err := html.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	var c converter
	c.walk(doc)
	out := c.b.String()
	// Collapse runs of 3+ newlines down to a paragraph break.
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

type converter struct {
	b strings.Builder
}

// skip returns true for elements whose subtree carries no useful prose.
func skip(a atom.Atom) bool {
	switch a {
	case atom.Script, atom.Style, atom.Head, atom.Noscript, atom.Template,
		atom.Svg, atom.Iframe, atom.Object, atom.Embed:
		return true
	}
	return false
}

// hidden reports whether an element is visually hidden via inline CSS or
// aria-hidden. Such content is a common prompt-injection vector (the text never
// renders for a human but reaches a naive scraper), so we drop its subtree.
func hidden(n *html.Node) bool {
	if strings.EqualFold(attr(n, "aria-hidden"), "true") {
		return true
	}
	for _, a := range n.Attr {
		// `hidden` is a boolean attribute: presence means hidden (often valueless).
		if a.Key == "hidden" {
			return true
		}
	}
	style := strings.ToLower(attr(n, "style"))
	if style == "" {
		return false
	}
	style = strings.ReplaceAll(style, " ", "")
	return strings.Contains(style, "display:none") ||
		strings.Contains(style, "visibility:hidden")
}

func (c *converter) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		c.writeText(n.Data)
		return
	case html.ElementNode:
		if skip(n.DataAtom) || hidden(n) {
			return
		}
	}

	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.DataAtom-atom.H1) + 1
		c.block()
		c.b.WriteString(strings.Repeat("#", level) + " ")
		c.children(n)
		c.block()
		return
	case atom.Br:
		c.b.WriteString("\n")
		return
	case atom.Hr:
		c.block()
		c.b.WriteString("---")
		c.block()
		return
	case atom.Li:
		c.block()
		c.b.WriteString("- ")
		c.children(n)
		c.b.WriteString("\n")
		return
	case atom.A:
		href := attr(n, "href")
		text := strings.TrimSpace(plainText(n))
		if href != "" && text != "" {
			c.writeText("[" + text + "](" + href + ")")
			return
		}
		c.children(n)
		return
	case atom.Strong, atom.B:
		c.b.WriteString("**")
		c.children(n)
		c.b.WriteString("**")
		return
	case atom.Em, atom.I:
		c.b.WriteString("*")
		c.children(n)
		c.b.WriteString("*")
		return
	case atom.Code:
		c.b.WriteString("`")
		c.children(n)
		c.b.WriteString("`")
		return
	case atom.P, atom.Div, atom.Section, atom.Article, atom.Header, atom.Footer,
		atom.Ul, atom.Ol, atom.Table, atom.Tr, atom.Blockquote, atom.Pre, atom.Main, atom.Nav:
		c.block()
		c.children(n)
		c.block()
		return
	case atom.Td, atom.Th:
		c.children(n)
		c.b.WriteString(" ")
		return
	case atom.Title:
		// Title lives in <head>, which we skip; if it appears in body, treat as text.
		c.children(n)
		return
	}

	c.children(n)
}

func (c *converter) children(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch)
	}
}

// block ensures the builder ends with a paragraph break, without stacking blanks.
func (c *converter) block() {
	s := c.b.String()
	if s == "" {
		return
	}
	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		c.b.WriteString("\n")
		return
	}
	c.b.WriteString("\n\n")
}

// writeText appends text, collapsing internal whitespace and avoiding a leading
// space right after a newline.
func (c *converter) writeText(s string) {
	collapsed := collapseSpaces(s)
	if collapsed == "" {
		return
	}
	cur := c.b.String()
	if collapsed == " " && (cur == "" || strings.HasSuffix(cur, "\n") || strings.HasSuffix(cur, " ")) {
		return
	}
	if strings.HasPrefix(collapsed, " ") && (cur == "" || strings.HasSuffix(cur, "\n") || strings.HasSuffix(cur, " ")) {
		collapsed = strings.TrimPrefix(collapsed, " ")
	}
	c.b.WriteString(collapsed)
}

// collapseSpaces replaces any run of whitespace with a single space. Leading and
// trailing whitespace is preserved as a single space so word boundaries survive.
func collapseSpaces(s string) string {
	if strings.TrimSpace(s) == "" {
		if s == "" {
			return ""
		}
		return " "
	}
	var b strings.Builder
	b.Grow(len(s))
	if isSpace(rune(s[0])) {
		b.WriteByte(' ')
	}
	inSpace := false
	for _, r := range s {
		if isSpace(r) {
			inSpace = true
			continue
		}
		if inSpace {
			b.WriteByte(' ')
			inSpace = false
		}
		b.WriteRune(r)
	}
	if inSpace {
		b.WriteByte(' ')
	}
	return b.String()
}

func isSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f', '\v', 0x00A0:
		return true
	}
	return false
}

// plainText returns the concatenated text content of a node's subtree.
func plainText(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
			return
		}
		if node.Type == html.ElementNode && skip(node.DataAtom) {
			return
		}
		for ch := node.FirstChild; ch != nil; ch = ch.NextSibling {
			rec(ch)
		}
	}
	rec(n)
	return collapseSpaces(b.String())
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}
