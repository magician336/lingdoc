// lingdoc-docx-demo renders a synthetic frozen input to an editable DOCX.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/lingdoc/docx"
)

type frozen struct {
	ProjectName  string `json:"project_name"`
	DeliveryKind string `json:"delivery_kind"`
	Chapters     []struct {
		Title        string   `json:"title"`
		BodyMarkdown string   `json:"body_markdown"`
		SourceIDs    []string `json:"source_ids"`
		ReviewItems  []struct {
			ID        string `json:"id"`
			Statement string `json:"statement"`
		} `json:"review_items"`
		Confirmation struct {
			ReviewDecisions []struct {
				ReviewItemID string `json:"review_item_id"`
				Disposition  string `json:"disposition"`
				Reason       string `json:"reason"`
			} `json:"review_decisions"`
		} `json:"confirmation"`
	} `json:"chapters"`
	Sources []struct {
		ID           string `json:"id"`
		DisplayTitle string `json:"display_title"`
		Locator      string `json:"locator"`
		QuotedText   string `json:"quoted_text"`
	} `json:"sources"`
}

func main() {
	inputPath := flag.String("input", "docs/08-本轮实施方案/contracts/frozen-input.canonical.json", "synthetic frozen input")
	outputPath := flag.String("output", "lingdoc-demo.docx", "output DOCX path")
	flag.Parse()
	if err := run(*inputPath, *outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(inputPath, outputPath string) error {
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return err
	}
	var src frozen
	if err := json.Unmarshal(raw, &src); err != nil {
		return err
	}
	in := docx.Input{ProjectName: src.ProjectName, DeliveryKind: src.DeliveryKind}
	for _, source := range src.Sources {
		in.Sources = append(in.Sources, docx.Source{ID: source.ID, DisplayTitle: source.DisplayTitle, Locator: source.Locator, QuotedText: source.QuotedText})
	}
	for _, chapter := range src.Chapters {
		out := docx.Chapter{Title: chapter.Title, BodyMarkdown: chapter.BodyMarkdown, SourceIDs: chapter.SourceIDs}
		decisions := map[string]struct{ disposition, reason string }{}
		for _, decision := range chapter.Confirmation.ReviewDecisions {
			if _, exists := decisions[decision.ReviewItemID]; exists {
				return fmt.Errorf("duplicate decision for %s", decision.ReviewItemID)
			}
			decisions[decision.ReviewItemID] = struct{ disposition, reason string }{decision.Disposition, decision.Reason}
		}
		for _, item := range chapter.ReviewItems {
			decision, exists := decisions[item.ID]
			if !exists {
				return fmt.Errorf("missing decision for %s", item.ID)
			}
			out.ReviewItems = append(out.ReviewItems, docx.ReviewItem{ID: item.ID, Statement: item.Statement, Disposition: decision.disposition, Reason: decision.reason})
			delete(decisions, item.ID)
		}
		if len(decisions) != 0 {
			return fmt.Errorf("orphan review decision in %s", chapter.Title)
		}
		in.Chapters = append(in.Chapters, out)
	}
	data, err := docx.Render(in)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", outputPath, len(data))
	return nil
}
