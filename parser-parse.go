package readability

import (
	"fmt"
	"io"
	nurl "net/url"
	"strings"
	"time"

	"github.com/go-shiori/dom"
	"golang.org/x/net/html"
)

// Parse parses a reader and find the main readable content.
func (ps *Parser) Parse(input io.Reader, pageURL *nurl.URL) (Article, error) {
	// Parse input
	doc, err := dom.Parse(input)
	if err != nil {
		return Article{}, fmt.Errorf("failed to parse input: %v", err)
	}

	return ps.ParseDocument(doc, pageURL)
}

// ParseDocument parses the specified document and find the main readable content.
func (ps *Parser) ParseDocument(doc *html.Node, pageURL *nurl.URL) (Article, error) {
	// Clone document to make sure the original kept untouched
	ps.doc = dom.Clone(doc, true)

	// Reset parser data
	ps.articleTitle = ""
	ps.articleByline = ""
	ps.articleDir = ""
	ps.articleSiteName = ""
	ps.documentURI = pageURL
	ps.attempts = []parseAttempt{}
	ps.flags = flags{
		stripUnlikelys:     true,
		useWeightClasses:   true,
		cleanConditionally: true,
	}

	// Avoid parsing too large documents, as per configuration option
	if ps.MaxElemsToParse > 0 {
		numTags := len(dom.GetElementsByTagName(ps.doc, "*"))
		if numTags > ps.MaxElemsToParse {
			return Article{}, fmt.Errorf("documents too large: %d elements", numTags)
		}
	}

	// Unwrap image from noscript
	ps.unwrapNoscriptImages(ps.doc)

	// Extract JSON-LD metadata before removing scripts
	var jsonLd map[string]string
	if !ps.DisableJSONLD {
		jsonLd, _ = ps.getJSONLD()
	}

	// Remove script tags from the document.
	ps.removeScripts(ps.doc)

	// Prepares the HTML document
	ps.prepDocument()

	// Fetch metadata
	metadata := ps.getArticleMetadata(jsonLd)
	ps.articleTitle = metadata["title"]

	// Try to grab article content
	finalHTMLContent := ""
	finalTextContent := ""
	articleContent := ps.grabArticle()
	var readableNode *html.Node

	if articleContent != nil {
		ps.postProcessContent(articleContent)

		// If we haven't found an excerpt in the article's metadata,
		// use the article's first paragraph as the excerpt. This is used
		// for displaying a preview of the article's content.
		if metadata["excerpt"] == "" {
			paragraphs := dom.GetElementsByTagName(articleContent, "p")
			if len(paragraphs) > 0 {
				metadata["excerpt"] = strings.TrimSpace(dom.TextContent(paragraphs[0]))
			}
		}

		readableNode = dom.FirstElementChild(articleContent)
		finalHTMLContent = dom.InnerHTML(articleContent)
		finalTextContent = dom.TextContent(articleContent)
		finalTextContent = strings.TrimSpace(finalTextContent)
	}

	finalByline := metadata["byline"]
	if finalByline == "" {
		finalByline = ps.articleByline
	}

	// Excerpt is an supposed to be short and concise,
	// so it shouldn't have any new line
	excerpt := strings.TrimSpace(metadata["excerpt"])
	excerpt = strings.Join(strings.Fields(excerpt), " ")

	// go-readability special:
	// Internet is dangerous and weird, and sometimes we will find
	// metadata isn't encoded using a valid Utf-8, so here we check it.
	var replacementTitle string
	if pageURL != nil {
		replacementTitle = pageURL.String()
	}

	validTitle := strings.ToValidUTF8(ps.articleTitle, replacementTitle)
	validByline := strings.ToValidUTF8(finalByline, "")
	validExcerpt := strings.ToValidUTF8(excerpt, "")

	datePublished := ps.getDate(metadata, "datePublished")
	dateModified := ps.getDate(metadata, "dataModified")

	return Article{
		Title:         validTitle,
		Byline:        validByline,
		Node:          readableNode,
		Content:       finalHTMLContent,
		TextContent:   finalTextContent,
		Length:        charCount(finalTextContent),
		Excerpt:       validExcerpt,
		SiteName:      metadata["siteName"],
		Image:         metadata["image"],
		Favicon:       metadata["favicon"],
		PublishedTime: datePublished,
		ModifiedTime:  dateModified,
	}, nil
}

func (ps *Parser) getDate(metadata map[string]string, fieldName string) *time.Time {
	dateStr, ok := metadata[fieldName]
	if ok && len(dateStr) > 0 {
		return getParsedDate(dateStr)
	}
	return nil
}

func getParsedDate(dateStr string) *time.Time {
	// Following formats have been seen in the wild.
	formats := []string{
		time.RFC822,  // RSS
		time.RFC822Z, // RSS
		time.RFC3339, // Atom
		time.UnixDate,
		time.RubyDate,
		time.RFC850,
		time.RFC1123Z,
		time.RFC1123,
		time.ANSIC,
		"Mon, January 2 2006 15:04:05 -0700",
		"Mon, January 02, 2006, 15:04:05 MST",
		"Mon, January 02, 2006 15:04:05 MST",
		"Mon, Jan 2, 2006 15:04 MST",
		"Mon, Jan 2 2006 15:04 MST",
		"Mon, Jan 2, 2006 15:04:05 MST",
		"Mon, Jan 2 2006 15:04:05 -700",
		"Mon, Jan 2 2006 15:04:05 -0700",
		"Mon Jan 2 15:04 2006",
		"Mon Jan 2 15:04:05 2006 MST",
		"Mon Jan 02, 2006 3:04 pm",
		"Mon, Jan 02,2006 15:04:05 MST",
		"Mon Jan 02 2006 15:04:05 -0700",
		"Monday, January 2, 2006 15:04:05 MST",
		"Monday, January 2, 2006 03:04 PM",
		"Monday, January 2, 2006",
		"Monday, January 02, 2006",
		"Monday, 2 January 2006 15:04:05 MST",
		"Monday, 2 January 2006 15:04:05 -0700",
		"Monday, 2 Jan 2006 15:04:05 MST",
		"Monday, 2 Jan 2006 15:04:05 -0700",
		"Monday, 02 January 2006 15:04:05 MST",
		"Monday, 02 January 2006 15:04:05 -0700",
		"Monday, 02 January 2006 15:04:05",
		"Mon, 2 January 2006 15:04 MST",
		"Mon, 2 January 2006, 15:04 -0700",
		"Mon, 2 January 2006, 15:04:05 MST",
		"Mon, 2 January 2006 15:04:05 MST",
		"Mon, 2 January 2006 15:04:05 -0700",
		"Mon, 2 January 2006",
		"Mon, 2 Jan 2006 3:04:05 PM -0700",
		"Mon, 2 Jan 2006 15:4:5 MST",
		"Mon, 2 Jan 2006 15:4:5 -0700 GMT",
		"Mon, 2, Jan 2006 15:4",
		"Mon, 2 Jan 2006 15:04 MST",
		"Mon, 2 Jan 2006, 15:04 -0700",
		"Mon, 2 Jan 2006 15:04 -0700",
		"Mon, 2 Jan 2006 15:04:05 UT",
		"Mon, 2 Jan 2006 15:04:05MST",
		"Mon, 2 Jan 2006 15:04:05 MST",
		"Mon 2 Jan 2006 15:04:05 MST",
		"mon,2 Jan 2006 15:04:05 MST",
		"Mon, 2 Jan 2006 15:04:05 -0700 MST",
		"Mon, 2 Jan 2006 15:04:05-0700",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05",
		"Mon, 2 Jan 2006 15:04",
		"Mon,2 Jan 2006",
		"Mon, 2 Jan 2006",
		"Mon, 2 Jan 15:04:05 MST",
		"Mon, 2 Jan 06 15:04:05 MST",
		"Mon, 2 Jan 06 15:04:05 -0700",
		"Mon, 2006-01-02 15:04",
		"Mon,02 January 2006 14:04:05 MST",
		"Mon, 02 January 2006",
		"Mon, 02 Jan 2006 3:04:05 PM MST",
		"Mon, 02 Jan 2006 15 -0700",
		"Mon,02 Jan 2006 15:04 MST",
		"Mon, 02 Jan 2006 15:04 MST",
		"Mon, 02 Jan 2006 15:04 -0700",
		"Mon, 02 Jan 2006 15:04:05 Z",
		"Mon, 02 Jan 2006 15:04:05 UT",
		"Mon, 02 Jan 2006 15:04:05 MST-07:00",
		"Mon, 02 Jan 2006 15:04:05 MST -0700",
		"Mon, 02 Jan 2006, 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04:05MST",
		"Mon, 02 Jan 2006 15:04:05 MST",
		"Mon , 02 Jan 2006 15:04:05 MST",
		"Mon, 02 Jan 2006 15:04:05 GMT-0700",
		"Mon,02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 -07:00",
		"Mon, 02 Jan 2006 15:04:05 --0700",
		"Mon 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 -07",
		"Mon, 02 Jan 2006 15:04:05 00",
		"Mon, 02 Jan 2006 15:04:05",
		"Mon, 02 Jan 2006",
		"Mon, 02 Jan 06 15:04:05 MST",
		"January 2, 2006 3:04 PM",
		"January 2, 2006, 3:04 p.m.",
		"January 2, 2006 15:04:05 MST",
		"January 2, 2006 15:04:05",
		"January 2, 2006 03:04 PM",
		"January 2, 2006",
		"January 02, 2006 15:04:05 MST",
		"January 02, 2006 15:04",
		"January 02, 2006 03:04 PM",
		"January 02, 2006",
		"Jan 2, 2006 3:04:05 PM MST",
		"Jan 2, 2006 3:04:05 PM",
		"Jan 2, 2006 15:04:05 MST",
		"Jan 2, 2006",
		"Jan 02 2006 03:04:05PM",
		"Jan 02, 2006",
		"6/1/2 15:04",
		"6-1-2 15:04",
		"2 January 2006 15:04:05 MST",
		"2 January 2006 15:04:05 -0700",
		"2 January 2006",
		"2 Jan 2006 15:04:05 Z",
		"2 Jan 2006 15:04:05 MST",
		"2 Jan 2006 15:04:05 -0700",
		"2 Jan 2006",
		"2.1.2006 15:04:05",
		"2/1/2006",
		"2-1-2006",
		"2006 January 02",
		"2006-1-2T15:04:05Z",
		"2006-1-2 15:04:05",
		"2006-1-2",
		"2006-1-02T15:04:05Z",
		"2006-01-02T15:04Z",
		"2006-01-02T15:04-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00:00",
		"2006-01-02T15:04:05:-0700",
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05 -0700",
		"2006-01-02T15:04:05:00",
		"2006-01-02T15:04:05",
		"2006-01-02 at 15:04:05",
		"2006-01-02 15:04:05Z",
		"2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05-0700",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04",
		"2006-01-02 00:00:00.0 15:04:05.0 -0700",
		"2006/01/02",
		"2006-01-02",
		"15:04 02.01.2006 -0700",
		"1/2/2006 3:04:05 PM MST",
		"1/2/2006 3:04:05 PM",
		"1/2/2006 15:04:05 MST",
		"1/2/2006",
		"06/1/2 15:04",
		"06-1-2 15:04",
		"02 Monday, Jan 2006 15:04",
		"02 Jan 2006 15:04 MST",
		"02 Jan 2006 15:04:05 UT",
		"02 Jan 2006 15:04:05 MST",
		"02 Jan 2006 15:04:05 -0700",
		"02 Jan 2006 15:04:05",
		"02 Jan 2006",
		"02/01/2006 15:04 MST",
		"02-01-2006 15:04:05 MST",
		"02.01.2006 15:04:05",
		"02/01/2006 15:04:05",
		"02.01.2006 15:04",
		"02/01/2006 - 15:04",
		"02.01.2006 -0700",
		"02/01/2006",
		"02-01-2006",
		"01/02/2006 3:04 PM",
		"01/02/2006 15:04:05 MST",
		"01/02/2006 - 15:04",
		"01/02/2006",
		"01-02-2006",
	}
	for i, format := range formats {
		parsedDate, err := time.Parse(format, dateStr)
		if err == nil {
			return &parsedDate
		} else if i == len(formats)-1 {
			fmt.Printf("Failed to parse date \"%s\"\n", dateStr)
		}
	}
	return nil
}
