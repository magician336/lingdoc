// Package crossref contains a metadata-only client for the Crossref REST API.
// It deliberately does not fetch publisher pages or full text.
package crossref

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL       = "https://api.crossref.org"
	defaultRequestTime   = 10 * time.Second
	defaultMaxResults    = 20
	maxResponseBodyBytes = 8 << 20
)

var (
	ErrInvalidQuery     = errors.New("crossref: query must not be empty")
	ErrInvalidMaxResult = errors.New("crossref: maxResults must be between 1 and 20")
	ErrInvalidDateRange = errors.New("crossref: dates must be YYYY-MM-DD and from must not be after until")
	ErrInvalidBaseURL   = errors.New("crossref: BaseURL must be an absolute HTTP(S) URL without query or fragment")
	ErrInvalidMailto    = errors.New("crossref: Mailto must be a single valid email address")
)

// Client searches Crossref metadata. An empty BaseURL selects the public
// Crossref REST API. Timeout defaults to ten seconds and can be shortened for
// callers that need a smaller request budget.
type Client struct {
	HTTPClient *http.Client
	BaseURL    string
	Mailto     string
	Timeout    time.Duration
}

// Author preserves the author-name fields Crossref returns. Affiliations are
// flattened to their names while absent source fields remain empty.
type Author struct {
	Given        string   `json:"given,omitempty"`
	Family       string   `json:"family,omitempty"`
	Name         string   `json:"name,omitempty"`
	ORCID        string   `json:"orcid,omitempty"`
	Affiliations []string `json:"affiliations,omitempty"`
}

// License is a Crossref-deposited license declaration. It is metadata only;
// its presence does not assert that the client has retrieved or may redistribute
// the article's full text.
type License struct {
	URL            string `json:"url,omitempty"`
	StartDate      string `json:"start_date,omitempty"`
	ContentVersion string `json:"content_version,omitempty"`
	DelayInDays    *int   `json:"delay_in_days,omitempty"`
}

// UpdateTo preserves Crossref's update-to record, including the raw update
// type. A retraction is only indicated when Crossref explicitly supplies a
// matching update record; an absent record is not evidence that a work is not
// retracted.
type UpdateTo struct {
	DOI     string `json:"doi,omitempty"`
	Type    string `json:"type,omitempty"`
	Updated string `json:"updated,omitempty"`
	Label   string `json:"label,omitempty"`
	Source  string `json:"source,omitempty"`
	Record  string `json:"record,omitempty"`
}

// Work contains the Crossref metadata needed to identify and assess a search
// result. PublishedDate is an ISO-like partial date (YYYY, YYYY-MM, or
// YYYY-MM-DD), preserving the precision Crossref provides.
type Work struct {
	DOI           string     `json:"doi,omitempty"`
	Title         string     `json:"title,omitempty"`
	Authors       []Author   `json:"authors,omitempty"`
	PublishedDate string     `json:"published_date,omitempty"`
	Journal       string     `json:"journal,omitempty"`
	ISSN          []string   `json:"issn,omitempty"`
	Type          string     `json:"type,omitempty"`
	URL           string     `json:"url,omitempty"`
	Licenses      []License  `json:"licenses,omitempty"`
	UpdateTo      []UpdateTo `json:"update_to,omitempty"`
}

// HasRetractionNotice reports only an explicit retraction update in Crossref's
// update-to metadata. False means no retraction update was returned, not that
// the work has been independently verified as unretracted.
func (w Work) HasRetractionNotice() bool {
	for _, update := range w.UpdateTo {
		if strings.EqualFold(strings.TrimSpace(update.Type), "retraction") {
			return true
		}
	}
	return false
}

// Search runs one bibliographic query against Crossref's /works endpoint.
// maxResults is bounded to 1..20. The request is metadata-only and does not
// follow result URLs.
func (c Client) Search(ctx context.Context, query string, maxResults int) ([]Work, error) {
	return c.SearchWithDateRange(ctx, query, maxResults, "", "")
}

// SearchWithDateRange searches bibliographic metadata and applies optional
// publication-date bounds at Crossref. Dates use YYYY-MM-DD so the saved plan
// and the provider request have the same explicit, replayable bounds.
func (c Client) SearchWithDateRange(ctx context.Context, query string, maxResults int, from, until string) ([]Work, error) {
	if ctx == nil {
		return nil, errors.New("crossref: context must not be nil")
	}
	if strings.TrimSpace(query) == "" {
		return nil, ErrInvalidQuery
	}
	if maxResults < 1 || maxResults > defaultMaxResults {
		return nil, ErrInvalidMaxResult
	}
	from = strings.TrimSpace(from)
	until = strings.TrimSpace(until)
	if from != "" {
		if _, err := time.Parse("2006-01-02", from); err != nil {
			return nil, ErrInvalidDateRange
		}
	}
	if until != "" {
		if _, err := time.Parse("2006-01-02", until); err != nil {
			return nil, ErrInvalidDateRange
		}
	}
	if from != "" && until != "" && from > until {
		return nil, ErrInvalidDateRange
	}

	baseURL := strings.TrimSpace(c.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	endpoint, err := worksEndpoint(baseURL)
	if err != nil {
		return nil, err
	}

	mailto := strings.TrimSpace(c.Mailto)
	if mailto != "" {
		address, parseErr := mail.ParseAddress(mailto)
		if parseErr != nil || address.Address != mailto || strings.ContainsAny(mailto, "\r\n") {
			return nil, ErrInvalidMailto
		}
	}

	values := endpoint.Query()
	values.Set("query.bibliographic", strings.TrimSpace(query))
	values.Set("rows", strconv.Itoa(maxResults))
	filters := []string{}
	if from != "" {
		filters = append(filters, "from-pub-date:"+from)
	}
	if until != "" {
		filters = append(filters, "until-pub-date:"+until)
	}
	if len(filters) > 0 {
		values.Set("filter", strings.Join(filters, ","))
	}
	if mailto != "" {
		values.Set("mailto", mailto)
	}
	endpoint.RawQuery = values.Encode()

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultRequestTime
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("crossref: create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent(mailto))

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("crossref: request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return nil, fmt.Errorf("crossref: request returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope apiEnvelope
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBodyBytes))
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("crossref: decode response: %w", err)
	}
	works := make([]Work, 0, min(len(envelope.Message.Items), maxResults))
	for _, item := range envelope.Message.Items {
		if len(works) == maxResults {
			break
		}
		works = append(works, normalizeWork(item))
	}
	return works, nil
}

func worksEndpoint(base string) (*url.URL, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidBaseURL
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/works"
	return parsed, nil
}

func userAgent(mailto string) string {
	if mailto == "" {
		return "LingDoc academic Crossref client"
	}
	return "LingDoc academic Crossref client (mailto:" + mailto + ")"
}

type apiEnvelope struct {
	Message struct {
		Items []apiWork `json:"items"`
	} `json:"message"`
}

type apiWork struct {
	DOI             string        `json:"DOI"`
	Title           []string      `json:"title"`
	Author          []apiAuthor   `json:"author"`
	Published       apiDate       `json:"published"`
	PublishedPrint  apiDate       `json:"published-print"`
	PublishedOnline apiDate       `json:"published-online"`
	Issued          apiDate       `json:"issued"`
	ContainerTitle  []string      `json:"container-title"`
	ISSN            []string      `json:"ISSN"`
	Type            string        `json:"type"`
	URL             string        `json:"URL"`
	License         []apiLicense  `json:"license"`
	UpdateTo        []apiUpdateTo `json:"update-to"`
}

type apiAuthor struct {
	Given       string `json:"given"`
	Family      string `json:"family"`
	Name        string `json:"name"`
	ORCID       string `json:"ORCID"`
	Affiliation []struct {
		Name string `json:"name"`
	} `json:"affiliation"`
}

type apiDate struct {
	DateParts [][]int `json:"date-parts"`
}

type apiLicense struct {
	URL            string  `json:"URL"`
	Start          apiDate `json:"start"`
	ContentVersion string  `json:"content-version"`
	DelayInDays    *int    `json:"delay-in-days"`
}

type apiUpdateTo struct {
	DOI     string `json:"DOI"`
	Type    string `json:"type"`
	Updated string `json:"updated"`
	Label   string `json:"label"`
	Source  string `json:"source"`
	Record  string `json:"record"`
}

func normalizeWork(item apiWork) Work {
	work := Work{
		DOI:           item.DOI,
		Title:         firstNonEmpty(item.Title),
		PublishedDate: publishedDate(item.Published, item.PublishedPrint, item.PublishedOnline, item.Issued),
		Journal:       firstNonEmpty(item.ContainerTitle),
		ISSN:          item.ISSN,
		Type:          item.Type,
		URL:           item.URL,
		Authors:       make([]Author, 0, len(item.Author)),
		Licenses:      make([]License, 0, len(item.License)),
		UpdateTo:      make([]UpdateTo, 0, len(item.UpdateTo)),
	}
	for _, raw := range item.Author {
		author := Author{Given: raw.Given, Family: raw.Family, Name: raw.Name, ORCID: raw.ORCID, Affiliations: make([]string, 0, len(raw.Affiliation))}
		for _, affiliation := range raw.Affiliation {
			author.Affiliations = append(author.Affiliations, affiliation.Name)
		}
		work.Authors = append(work.Authors, author)
	}
	for _, raw := range item.License {
		work.Licenses = append(work.Licenses, License{
			URL:            raw.URL,
			StartDate:      formatDate(raw.Start),
			ContentVersion: raw.ContentVersion,
			DelayInDays:    raw.DelayInDays,
		})
	}
	for _, raw := range item.UpdateTo {
		work.UpdateTo = append(work.UpdateTo, UpdateTo(raw))
	}
	return work
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func publishedDate(dates ...apiDate) string {
	for _, date := range dates {
		if value := formatDate(date); value != "" {
			return value
		}
	}
	return ""
}

func formatDate(date apiDate) string {
	if len(date.DateParts) == 0 || len(date.DateParts[0]) == 0 || date.DateParts[0][0] == 0 {
		return ""
	}
	parts := date.DateParts[0]
	switch len(parts) {
	case 1:
		return fmt.Sprintf("%04d", parts[0])
	case 2:
		return fmt.Sprintf("%04d-%02d", parts[0], parts[1])
	default:
		return fmt.Sprintf("%04d-%02d-%02d", parts[0], parts[1], parts[2])
	}
}
