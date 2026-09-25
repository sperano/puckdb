package news

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// feedDocument decodes both RSS 2.0 (<rss><channel><item>) and Atom
// (<feed><entry>) documents.
type feedDocument struct {
	XMLName xml.Name
	Items   []rssItem   `xml:"channel>item"`
	Entries []atomEntry `xml:"entry"`
}

type rssItem struct {
	GUID  string `xml:"guid"`
	Title string `xml:"title"`
	// Links holds every <link> of the item: Sportsnet adds an empty
	// <link type="app-deep-link-field"> after the real one.
	Links       []string `xml:"link"`
	Description string   `xml:"description"`
	// Content is the item's full HTML (content:encoded) when the feed
	// carries it.
	Content string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	PubDate string `xml:"pubDate"`
	Creator string `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Author  string `xml:"author"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Links     []atomLink `xml:"link"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

const atomAlternateRel = "alternate"

// ParseFeed reads up to limit items of an RSS 2.0 or Atom feed. Each item's
// description/summary is its evidence text; its full content
// (content:encoded, Atom content), when the feed carries more than the
// summary, is its body. Story pages are never downloaded.
func ParseFeed(body []byte, retrievedAt time.Time, limit int) ([]Item, error) {
	var doc feedDocument
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = false
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	switch doc.XMLName.Local {
	case "rss", "feed", "RDF":
	default:
		return nil, fmt.Errorf("parse feed: unexpected root element <%s>", doc.XMLName.Local)
	}
	items := make([]Item, 0, len(doc.Items)+len(doc.Entries))
	for _, raw := range doc.Items {
		items = append(items, rssToItem(raw, retrievedAt))
	}
	for _, raw := range doc.Entries {
		items = append(items, atomToItem(raw, retrievedAt))
	}
	return finishItems(items, limit), nil
}

func rssToItem(raw rssItem, retrievedAt time.Time) Item {
	author := raw.Creator
	if author == "" {
		author = raw.Author
	}
	link := ""
	for _, l := range raw.Links {
		if link = strings.TrimSpace(l); link != "" {
			break
		}
	}
	id := strings.TrimSpace(raw.GUID)
	if id == "" {
		id = link
	}
	text := raw.Description
	if CleanText(text, MaxEvidenceRunes) == "" {
		text = raw.Content
	}
	return Item{
		ExternalID: id, URL: link, Title: raw.Title, Text: text, Body: raw.Content, Author: author,
		PublishedAt: ParseFeedTime(raw.PubDate), RetrievedAt: retrievedAt,
	}
}

func atomToItem(raw atomEntry, retrievedAt time.Time) Item {
	link := ""
	for _, l := range raw.Links {
		if l.Rel == "" || l.Rel == atomAlternateRel {
			link = l.Href
			break
		}
	}
	text := raw.Summary
	if text == "" {
		text = raw.Content
	}
	return Item{
		ExternalID: raw.ID, URL: link, Title: raw.Title, Text: text, Body: raw.Content, Author: raw.Author.Name,
		PublishedAt: ParseFeedTime(raw.Published), UpdatedAt: ParseFeedTime(raw.Updated),
		RetrievedAt: retrievedAt,
	}
}

// finishItems normalizes items, drops those with no identity, title or link
// (nothing to attribute them to), and keeps the first limit of them.
func finishItems(items []Item, limit int) []Item {
	out := make([]Item, 0, min(len(items), limit))
	for _, it := range items {
		it = normalizeItem(it)
		if it.ExternalID == "" || it.Title == "" || it.URL == "" {
			continue
		}
		out = append(out, it)
		if len(out) == limit {
			break
		}
	}
	return out
}

// feedTimeLayouts are the date forms seen in feeds: RFC 822/1123 with and
// without seconds or a numeric zone, a 12-hour clock (RotoWire), and RFC 3339.
var feedTimeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 3:04:05 PM MST",
	"Mon, 02 Jan 2006 3:04:05 PM MST",
	"Mon, 2 Jan 2006 15:04 MST",
	"2 Jan 2006 15:04:05 MST",
	time.RFC3339Nano,
	time.RFC3339,
}

// zoneOffsets are the North American zone abbreviations feeds use. Go parses
// an abbreviation it does not know as UTC+0, which would shift a PDT story
// by seven hours.
var zoneOffsets = map[string]int{
	"UT": 0, "UTC": 0, "GMT": 0, "Z": 0,
	"EST": -5, "EDT": -4, "CST": -6, "CDT": -5,
	"MST": -7, "MDT": -6, "PST": -8, "PDT": -7,
	"AST": -4, "ADT": -3, "NST": -3, "NDT": -2,
}

const secondsPerHour = 60 * 60

// ParseFeedTime parses a feed timestamp; zero when empty or unreadable.
func ParseFeedTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range feedTimeLayouts {
		t, err := time.Parse(layout, value)
		if err != nil {
			continue
		}
		return fixZone(t)
	}
	return time.Time{}
}

// fixZone applies the real offset of a zone abbreviation Go did not know.
func fixZone(t time.Time) time.Time {
	name, offset := t.Zone()
	hours, known := zoneOffsets[name]
	if !known || offset == hours*secondsPerHour {
		return t.UTC()
	}
	zoned := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(),
		time.FixedZone(name, hours*secondsPerHour))
	return zoned.UTC()
}
