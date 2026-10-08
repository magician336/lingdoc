// Package delivery contains delivery-domain contracts for the LingDoc demo.
package delivery

import (
	"errors"
	"fmt"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
)

const (
	DemoTemplateID         = lingdoctemplate.DemoTemplateID
	DemoTemplateVersion    = lingdoctemplate.DemoTemplateVersion
	DemoRulesetHash        = lingdoctemplate.DemoRulesetHash
	RuleRequiredFields     = lingdoctemplate.RuleRequiredFields
	RuleChapterNonempty    = lingdoctemplate.RuleChapterNonempty
	RuleChapterConfirmed   = lingdoctemplate.RuleChapterConfirmed
	RuleReviewItemsDecided = lingdoctemplate.RuleReviewItems
	RuleSourceAvailable    = lingdoctemplate.RuleSourceAvailable
	SeverityBlocking       = lingdoctemplate.SeverityBlocking
	SeverityWarning        = lingdoctemplate.SeverityWarning
	SeverityInfo           = lingdoctemplate.SeverityInfo
)

const (
	DispositionResolved        = "resolved"
	DispositionRetainedWarning = "retained_warning"
)

var ErrTemplateNotFound = errors.New("lingdoc template not found")

type TemplateReader = lingdoctemplate.Reader
type Template = lingdoctemplate.Template
type Section = lingdoctemplate.Section
type Rule = lingdoctemplate.Rule

// FixedTemplateReader adapts the shared immutable T02 catalog to Delivery.
type FixedTemplateReader struct{}

func NewFixedTemplateReader() TemplateReader { return FixedTemplateReader{} }

func DemoTemplate() Template { return lingdoctemplate.DemoTemplate() }

func (FixedTemplateReader) Get(id, version string) (Template, error) {
	template, err := (lingdoctemplate.FixedDemoReader{}).Get(id, version)
	if err != nil {
		return Template{}, fmt.Errorf("%w: %q version %q", ErrTemplateNotFound, id, version)
	}
	return template, nil
}
