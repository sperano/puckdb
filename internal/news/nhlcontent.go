package news

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// nhlStoryURLPrefix is where NHL.com serves league and team-site stories by
// slug.
const nhlStoryURLPrefix = "https://www.nhl.com/news/"

// NHL content API tag sources.
const (
	nhlTagSourcePlayer = "player"
	nhlTagSourceTeam   = "team"
)

type nhlContentPage struct {
	Items []nhlStory `json:"items"`
}

type nhlStory struct {
	EntityID        string   `json:"_entityId"`
	SelfURL         string   `json:"selfUrl"`
	Slug            string   `json:"slug"`
	Title           string   `json:"title"`
	Headline        string   `json:"headline"`
	Summary         string   `json:"summary"`
	ContentDate     string   `json:"contentDate"`
	LastUpdatedDate string   `json:"lastUpdatedDate"`
	Tags            []nhlTag `json:"tags"`
	Context         *nhlTag  `json:"context"`
}

type nhlTag struct {
	Title              string          `json:"title"`
	ExternalSourceName string          `json:"externalSourceName"`
	RawExtraData       json.RawMessage `json:"extraData"`
}

// ParseNHLContent reads up to limit stories of an NHL.com content API page.
// Player tags become structured subjects (NHL player IDs); team tags and the
// story's team context become team hints.
func ParseNHLContent(body []byte, retrievedAt time.Time, limit int) ([]Item, error) {
	var page nhlContentPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("parse NHL content: %w", err)
	}
	items := make([]Item, 0, len(page.Items))
	for _, story := range page.Items {
		items = append(items, nhlStoryToItem(story, retrievedAt))
	}
	return finishItems(items, limit), nil
}

func nhlStoryToItem(story nhlStory, retrievedAt time.Time) Item {
	title := story.Headline
	if title == "" {
		title = story.Title
	}
	item := Item{
		ExternalID: story.EntityID, Title: title, Text: story.Summary, BodyURL: story.SelfURL,
		PublishedAt: ParseFeedTime(story.ContentDate), UpdatedAt: ParseFeedTime(story.LastUpdatedDate),
		RetrievedAt: retrievedAt,
	}
	if story.Slug != "" {
		item.URL = nhlStoryURLPrefix + story.Slug
	}
	tags := story.Tags
	if story.Context != nil {
		tags = append(tags, *story.Context)
	}
	for _, tag := range tags {
		extra := tag.extraData()
		switch tag.ExternalSourceName {
		case nhlTagSourcePlayer:
			if id, err := strconv.ParseInt(extra["playerId"], 10, 64); err == nil && id > 0 {
				item.Subjects = appendSubject(item.Subjects, Subject{NHLPlayerID: id, Name: tag.Title})
			}
		case nhlTagSourceTeam:
			if abbrev := strings.TrimSpace(extra["abbreviation"]); abbrev != "" {
				item.TeamHints = append(item.TeamHints, abbrev)
			}
		}
	}
	return item
}

// extraData decodes the tag's extra data, whose values are strings for the
// fields read here but may be anything for other tags.
func (t nhlTag) extraData() map[string]string {
	var raw map[string]any
	if err := json.Unmarshal(t.RawExtraData, &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		switch value := v.(type) {
		case string:
			out[k] = value
		case float64:
			out[k] = strconv.FormatInt(int64(value), 10)
		}
	}
	return out
}

func appendSubject(subjects []Subject, s Subject) []Subject {
	for _, existing := range subjects {
		if existing.NHLPlayerID == s.NHLPlayerID && existing.YahooPlayerID == s.YahooPlayerID {
			return subjects
		}
	}
	return append(subjects, s)
}

// nhlMarkdownPart is the type of a story page part that holds text; the
// others are photos, videos, embeds and documents.
const nhlMarkdownPart = "markdown"

// nhlPlayerEntityCode marks an inline link to a player in a story body.
const nhlPlayerEntityCode = "player"

var (
	// nhlEntityPattern matches the opening tag of an inline entity link:
	// <forge-entity title="Adam Sykora" slug="adam-sykora-8483669" code="player">.
	nhlEntityPattern = regexp.MustCompile(`<forge-entity\b([^>]*)>`)
	// nhlEntityAttrPattern reads one attribute of an entity tag.
	nhlEntityAttrPattern = regexp.MustCompile(`(\w+)="([^"]*)"`)
	// nhlSlugPlayerIDPattern reads the NHL player ID that ends a player slug.
	nhlSlugPlayerIDPattern = regexp.MustCompile(`-(\d+)$`)
	// nhlPlayerLinkPattern matches a markdown link to a player page:
	// [Ville Ottavainen](https://www.nhl.com/player/ville-ottavainen-8482866/stats).
	nhlPlayerLinkPattern = regexp.MustCompile(`\[([^\]]+)\]\(https?://(?:www\.)?nhl\.com/(?:[\w-]+/)*player/[\w-]*?-(\d+)(?:[/?#][^)]*)?\)`)
)

type nhlStoryPage struct {
	Parts []nhlStoryPart `json:"parts"`
}

// nhlStoryPart is one part of a story page. Content is a string for
// markdown parts and may be an object for the others.
type nhlStoryPart struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

// ParseNHLStoryBody reads a story page of the NHL.com content API (a story's
// selfUrl): the text of its markdown parts, and the players the text links
// (inline entities or links to player pages), as body subjects with their
// NHL IDs.
func ParseNHLStoryBody(page []byte) (string, []Subject, error) {
	var story nhlStoryPage
	if err := json.Unmarshal(page, &story); err != nil {
		return "", nil, fmt.Errorf("parse NHL story page: %w", err)
	}
	var texts []string
	var subjects []Subject
	for _, part := range story.Parts {
		if part.Type != nhlMarkdownPart {
			continue
		}
		var content string
		if err := json.Unmarshal(part.Content, &content); err != nil {
			return "", nil, fmt.Errorf("parse NHL story page text: %w", err)
		}
		texts = append(texts, content)
		for _, s := range nhlBodyPlayers(content) {
			subjects = appendSubject(subjects, s)
		}
	}
	return strings.Join(texts, "\n\n"), subjects, nil
}

// nhlBodyPlayers returns the players a story text links inline.
func nhlBodyPlayers(text string) []Subject {
	var subjects []Subject
	for _, tag := range nhlEntityPattern.FindAllStringSubmatch(text, -1) {
		attrs := make(map[string]string)
		for _, attr := range nhlEntityAttrPattern.FindAllStringSubmatch(tag[1], -1) {
			attrs[attr[1]] = attr[2]
		}
		if attrs["code"] != nhlPlayerEntityCode {
			continue
		}
		match := nhlSlugPlayerIDPattern.FindStringSubmatch(attrs["slug"])
		if match == nil {
			continue
		}
		subjects = appendBodyPlayer(subjects, match[1], attrs["title"])
	}
	for _, link := range nhlPlayerLinkPattern.FindAllStringSubmatch(text, -1) {
		subjects = appendBodyPlayer(subjects, link[2], link[1])
	}
	return subjects
}

// appendBodyPlayer adds the player with NHL ID id unless already listed.
func appendBodyPlayer(subjects []Subject, id, name string) []Subject {
	playerID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || playerID <= 0 {
		return subjects
	}
	return appendSubject(subjects, Subject{NHLPlayerID: playerID, Name: html.UnescapeString(strings.TrimSpace(name)), InBody: true})
}
