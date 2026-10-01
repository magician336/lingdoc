package workspace

import (
	"regexp"
	"strings"
)

var plainHeading = regexp.MustCompile(`^#{1,6}[ \t]+(.+)$`)
var plainBullet = regexp.MustCompile(`^[-*+][ \t]+(.+)$`)
var plainNumbered = regexp.MustCompile(`^([0-9]+)[.)][ \t]+(.+)$`)

// plainParagraphs preserves text and source markers while representing simple
// Markdown headings and lists as editable paragraphs in the MVP DOCX format.
// Tables, links, code and inline formatting remain unchanged and are rejected
// by the strict DOCX renderer rather than silently losing content.
func plainParagraphs(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		switch {
		case plainHeading.MatchString(line):
			lines[i] = plainHeading.ReplaceAllString(line, "$1")
		case plainBullet.MatchString(line):
			lines[i] = plainBullet.ReplaceAllString(line, "• $1")
		case plainNumbered.MatchString(line):
			lines[i] = plainNumbered.ReplaceAllString(line, "($1) $2")
		}
	}
	return strings.Join(lines, "\n")
}
