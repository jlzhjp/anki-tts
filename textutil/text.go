// Package textutil contains text normalization helpers for speech input.
package textutil

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// FromHTML converts an Anki HTML field value into readable plain text.
func FromHTML(value string) (string, error) {
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}

	var output strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			return
		}
		switch node.Type {
		case html.TextNode:
			output.WriteString(node.Data)
		case html.ElementNode:
			switch node.Data {
			case "br":
				output.WriteByte('\n')
			case "p", "div", "li", "tr":
				if output.Len() > 0 {
					output.WriteByte('\n')
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if node.Type == html.ElementNode {
			switch node.Data {
			case "p", "div", "li", "tr":
				output.WriteByte('\n')
			}
		}
	}
	walk(doc)
	return normalizeWhitespace(output.String()), nil
}

func normalizeWhitespace(value string) string {
	lines := strings.Split(value, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		fields := strings.FieldsFunc(line, unicode.IsSpace)
		if len(fields) > 0 {
			normalized = append(normalized, strings.Join(fields, " "))
		}
	}
	return strings.Join(normalized, "\n")
}
