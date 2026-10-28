package crossref

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestUpdateDateShapes(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"date-parts":[[2024,3]]}`, "2024-03"},
		{`{"date-parts":[[2023,4,22]],"date-time":"2023-04-22T00:00:00Z","timestamp":1682121600000}`, "2023-04-22T00:00:00Z"},
		{`"2022-01-02"`, "2022-01-02"},
		{`null`, ""},
	} {
		var date apiUpdateDate
		if err := json.Unmarshal([]byte(tc.input), &date); err != nil || string(date) != tc.want {
			t.Errorf("date %s => %q, %v; want %q", tc.input, date, err, tc.want)
		}
	}
	var date apiUpdateDate
	if err := json.Unmarshal([]byte(`42`), &date); err == nil {
		t.Fatal("invalid update date accepted")
	}
}

func TestSearchEncodesRequestAndMapsMetadata(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.URL.Path != "/works" {
			t.Errorf("path = %q, want /works", r.URL.Path)
		}
		if got, want := r.URL.Query().Get("query.bibliographic"), "sleep & machine learning/2024"; got != want {
			t.Errorf("query.bibliographic = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("rows"), "7"; got != want {
			t.Errorf("rows = %q, want %q", got, want)
		}
		if got, want := r.URL.Query().Get("mailto"), "reader@example.org"; got != want {
			t.Errorf("mailto = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("User-Agent"), "LingDoc academic Crossref client (mailto:reader@example.org)"; got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Errorf("Accept = %q, want %q", got, want)
		}
		if _, err := fmt.Fprint(w, `{"status":"ok","message":{"items":[{"DOI":"10.5555/example.1","title":["A metadata-only title"],"author":[{"given":"Ada","family":"Lovelace","ORCID":"https://orcid.org/0000-0000-0000-0001","affiliation":[{"name":"Example University"}]},{"name":"Consortium"}],"published-online":{"date-parts":[[2024,3,9]]},"container-title":["Journal of Examples"],"ISSN":["1234-5678","8765-4321"],"type":"journal-article","URL":"https://doi.org/10.5555/example.1","license":[{"URL":"https://creativecommons.org/licenses/by/4.0/","start":{"date-parts":[[2024,3]]},"content-version":"vor","delay-in-days":0}],"update-to":[{"DOI":"10.5555/example.retraction","type":"retraction","updated":{"date-parts":[[2025,1,2]],"date-time":"2025-01-02T03:04:05Z","timestamp":1735787045000},"label":"Retraction","source":"Crossref"}]}]}}`); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Mailto: "reader@example.org"}
	works, err := client.Search(context.Background(), "  sleep & machine learning/2024  ", 7)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("request count = %d, want 1 (publisher URLs must not be followed)", requests)
	}
	if len(works) != 1 {
		t.Fatalf("len(works) = %d, want 1", len(works))
	}
	work := works[0]
	if work.DOI != "10.5555/example.1" || work.Title != "A metadata-only title" || work.Journal != "Journal of Examples" || work.URL != "https://doi.org/10.5555/example.1" {
		t.Fatalf("identity metadata not mapped: %+v", work)
	}
	if len(work.ISSN) != 2 || work.ISSN[0] != "1234-5678" || work.ISSN[1] != "8765-4321" || work.Type != "journal-article" {
		t.Fatalf("ISSN or Crossref type not mapped: ISSN=%v Type=%q", work.ISSN, work.Type)
	}
	if work.PublishedDate != "2024-03-09" {
		t.Fatalf("PublishedDate = %q, want 2024-03-09", work.PublishedDate)
	}
	if len(work.Authors) != 2 || work.Authors[0].Given != "Ada" || work.Authors[0].Family != "Lovelace" || work.Authors[0].ORCID != "https://orcid.org/0000-0000-0000-0001" || len(work.Authors[0].Affiliations) != 1 || work.Authors[0].Affiliations[0] != "Example University" || work.Authors[1].Name != "Consortium" {
		t.Fatalf("author metadata not mapped: %+v", work.Authors)
	}
	if len(work.Licenses) != 1 || work.Licenses[0].URL != "https://creativecommons.org/licenses/by/4.0/" || work.Licenses[0].StartDate != "2024-03" || work.Licenses[0].ContentVersion != "vor" || work.Licenses[0].DelayInDays == nil || *work.Licenses[0].DelayInDays != 0 {
		t.Fatalf("license metadata not mapped: %+v", work.Licenses)
	}
	if !work.IsRetractionNotice() || len(work.UpdateTo) != 1 || work.UpdateTo[0].Type != "retraction" || work.UpdateTo[0].DOI != "10.5555/example.retraction" || work.UpdateTo[0].Label != "Retraction" || work.UpdateTo[0].Updated != "2025-01-02T03:04:05Z" {
		t.Fatalf("retraction update-to metadata not preserved: %+v", work.UpdateTo)
	}
}

func TestSearchPreservesMissingFieldsAndDatePrecision(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprint(w, `{"message":{"items":[{"DOI":"10.5555/year-only","issued":{"date-parts":[[2020]]}},{"DOI":"10.5555/unknown-date"}]}}`); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()

	works, err := (Client{BaseURL: server.URL}).Search(context.Background(), "topic", 2)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(works) != 2 {
		t.Fatalf("len(works) = %d, want 2", len(works))
	}
	if works[0].PublishedDate != "2020" {
		t.Errorf("year-only PublishedDate = %q, want 2020", works[0].PublishedDate)
	}
	if works[1].PublishedDate != "" || works[1].Title != "" || works[1].Journal != "" || works[1].IsRetractionNotice() {
		t.Errorf("missing source metadata was invented: %+v", works[1])
	}
}

func TestSearchCapsReturnedItemsToRequestedLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("rows"); got != "1" {
			t.Errorf("rows = %q, want 1", got)
		}
		if _, err := fmt.Fprint(w, `{"message":{"items":[{"DOI":"10.1/first"},{"DOI":"10.1/second"}]}}`); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()

	works, err := (Client{BaseURL: server.URL}).Search(context.Background(), "topic", 1)
	if err != nil {
		t.Fatalf("Search returned error: %v", err)
	}
	if len(works) != 1 || works[0].DOI != "10.1/first" {
		t.Fatalf("Search did not enforce requested result cap: %+v", works)
	}
}

func TestSearchRejectsInvalidInputWithoutRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := Client{BaseURL: server.URL}

	tests := []struct {
		name       string
		query      string
		maxResults int
		wantErr    error
	}{
		{name: "blank query", query: " \t ", maxResults: 1, wantErr: ErrInvalidQuery},
		{name: "zero results", query: "topic", maxResults: 0, wantErr: ErrInvalidMaxResult},
		{name: "too many results", query: "topic", maxResults: 21, wantErr: ErrInvalidMaxResult},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.Search(context.Background(), test.query, test.maxResults)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("invalid input caused %d HTTP requests", requests)
	}
}

func TestSearchWithDateRangeAppliesProviderFilters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query().Get("filter"), "from-pub-date:2020-01-01,until-pub-date:2024-12-31"; got != want {
			t.Errorf("filter = %q, want %q", got, want)
		}
		if _, err := fmt.Fprint(w, `{"message":{"items":[]}}`); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	defer server.Close()

	if _, err := (Client{BaseURL: server.URL}).SearchWithDateRange(context.Background(), "topic", 10, "2020-01-01", "2024-12-31"); err != nil {
		t.Fatal(err)
	}
}

func TestSearchWithDateRangeRejectsInvalidBoundsWithoutRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := Client{BaseURL: server.URL}
	for _, dates := range [][2]string{{"2025-01-01", "2024-12-31"}, {"2020-13-01", ""}, {"2020-01", ""}} {
		if _, err := client.SearchWithDateRange(context.Background(), "topic", 10, dates[0], dates[1]); !errors.Is(err, ErrInvalidDateRange) {
			t.Fatalf("dates %v error = %v, want ErrInvalidDateRange", dates, err)
		}
	}
	if requests != 0 {
		t.Fatalf("invalid date bounds caused %d HTTP requests", requests)
	}
}

func TestSearchRejectsInvalidBaseURLAndMailto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		client Client
		want   error
	}{
		{name: "relative base url", client: Client{BaseURL: "/works"}, want: ErrInvalidBaseURL},
		{name: "base url query", client: Client{BaseURL: "https://api.crossref.org?debug=1"}, want: ErrInvalidBaseURL},
		{name: "mailto injection", client: Client{Mailto: "reader@example.org\r\nX-Injected: yes"}, want: ErrInvalidMailto},
		{name: "display mailto", client: Client{Mailto: "Reader <reader@example.org>"}, want: ErrInvalidMailto},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.client.Search(context.Background(), "topic", 1)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSearchReturnsNon2xxAsError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := (Client{BaseURL: server.URL}).Search(context.Background(), "topic", 1)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") || !strings.Contains(err.Error(), "upstream unavailable") {
		t.Fatalf("error = %v, want HTTP 503 with bounded response detail", err)
	}
}

// TestLiveCrossrefSearchSmoke is opt-in because it contacts the public Crossref
// service. It verifies the production endpoint separately from the synthetic
// httptest suite without making default CI depend on external availability.
func TestLiveCrossrefSearchSmoke(t *testing.T) {
	if os.Getenv("LINGDOC_CROSSREF_LIVE") != "1" {
		t.Skip("set LINGDOC_CROSSREF_LIVE=1 to call the public Crossref API")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	works, err := (Client{Timeout: 12 * time.Second}).Search(ctx, "controlled academic research", 1)
	if err != nil {
		t.Fatalf("live Crossref search failed: %v", err)
	}
	if len(works) != 1 || strings.TrimSpace(works[0].DOI) == "" || strings.TrimSpace(works[0].Title) == "" {
		t.Fatalf("live Crossref returned no usable bibliographic metadata: %+v", works)
	}
	t.Logf("live Crossref metadata smoke passed: one DOI and title returned")
}

func TestSearchUsesContextCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	client := Client{
		BaseURL: "https://crossref.test",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, "topic", 1)
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestSearchAppliesRequestTimeout(t *testing.T) {
	t.Parallel()

	client := Client{
		BaseURL: "https://crossref.test",
		Timeout: 10 * time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		})},
	}
	_, err := client.Search(context.Background(), "topic", 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
