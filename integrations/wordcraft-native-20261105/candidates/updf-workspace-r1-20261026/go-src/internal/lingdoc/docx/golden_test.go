package docx

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSharedEditorMarkdownGoldenSet(t *testing.T) {
	var cases []struct {
		Name    string   `json:"name"`
		Body    string   `json:"body"`
		Plain   []string `json:"plain"`
		Sources []string `json:"source_ids"`
		Legacy  bool     `json:"legacy_preserve"`
	}
	raw, err := os.ReadFile("testdata/editor-md-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			in := demoInput()
			in.Chapters[0].BodyMarkdown = test.Body
			in.Chapters[0].SourceIDs = test.Sources
			in.Chapters[0].ReviewItems = nil
			in.Sources = nil
			for _, id := range test.Sources {
				in.Sources = append(in.Sources, Source{ID: id, DisplayTitle: id, Locator: "synthetic paragraph", QuotedText: "synthetic"})
			}
			file, err := Render(in)
			if test.Legacy {
				if err == nil {
					t.Fatal("unsupported legacy semantics silently flattened")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(in, file); err != nil {
				t.Fatal(err)
			}
			all, err := paragraphsInFile(file)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, p := range all[3:] {
				if p == in.Chapters[1].Title {
					break
				}
				got = append(got, p)
			}
			if !reflect.DeepEqual(got, test.Plain) {
				t.Fatalf("actual Word text %q want %q", got, test.Plain)
			}
		})
	}
}
