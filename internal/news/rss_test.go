package news

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFeedRotoWireFixture(t *testing.T) {
	items, err := ParseFeed(readFixture(t, "rotowire.xml"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 5)

	first := items[0]
	assert.Equal(t, "nhl600001", first.ExternalID)
	assert.Equal(t, "Brayden McNabb: Banned three games", first.Title)
	assert.Empty(t, first.Author)
	assert.NotContains(t, first.Text, "\n", "the description is collapsed to one line")
	assert.Contains(t, first.Text, "Visit RotoWire.com for more analysis")

	second := items[1]
	assert.Equal(t, "nhl600002", second.ExternalID)
	assert.Equal(t, time.Date(2026, time.September, 25, 2, 29, 0, 0, time.UTC), second.PublishedAt,
		"7:29 PM PDT is 02:29 UTC the next day")
}

func TestParseFeedSportsnetAtomFixture(t *testing.T) {
	items, err := ParseFeed(readFixture(t, "sportsnet.atom"), retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 1)

	it := items[0]
	assert.Equal(t, "tag:sportsnet.ca,2026:article-6709060", it.ExternalID)
	assert.Equal(t, "https://www.sportsnet.ca/nhl/article/canucks-acquire-defenceman/", it.URL)
	assert.Equal(t, "The Vancouver Canucks acquired defenceman Mario Ferraro from the San Jose Sharks on Sunday.", it.Text)
	assert.Equal(t, "Iain MacIntyre", it.Author)
	assert.Equal(t, time.Date(2026, time.September, 20, 18, 30, 0, 0, time.UTC), it.PublishedAt)
	assert.Equal(t, time.Date(2026, time.September, 20, 20, 0, 0, 0, time.UTC), it.UpdatedAt)
}

func TestParseFeedLimitIsHonored(t *testing.T) {
	items, err := ParseFeed(readFixture(t, "rotowire.xml"), retrievedAt, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestParseFeedDropsItemsWithoutTitleOrLink(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel>
		<item><guid>has-title</guid><title>Something happened</title><link>https://example.test/a</link><description>x</description></item>
		<item><guid>no-title</guid><link>https://example.test/b</link><description>x</description></item>
		<item><guid>no-link</guid><title>NHL Featured</title></item>
	</channel></rss>`)
	items, err := ParseFeed(body, retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "has-title", items[0].ExternalID)
}

func TestParseFeedRejectsNonFeedRoot(t *testing.T) {
	_, err := ParseFeed([]byte(`<html><body>not a feed</body></html>`), retrievedAt, defaultMaxItems)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected root element")
}

func TestParseFeedRejectsMalformedXML(t *testing.T) {
	_, err := ParseFeed([]byte(`<rss version="2.0"><channel><item><title>Unclosed`), retrievedAt, defaultMaxItems)
	require.Error(t, err)
}

func TestParseFeedTime(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Time
	}{
		{"RFC1123Z", "Thu, 24 Sep 2026 10:00:00 -0400", time.Date(2026, time.September, 24, 14, 0, 0, 0, time.UTC)},
		{"RFC1123 with GMT", "Thu, 24 Sep 2026 10:00:00 GMT", time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)},
		{"EST abbreviation", "Thu, 24 Sep 2026 10:00:00 EST", time.Date(2026, time.September, 24, 15, 0, 0, 0, time.UTC)},
		{"EDT abbreviation", "Thu, 24 Sep 2026 10:00:00 EDT", time.Date(2026, time.September, 24, 14, 0, 0, 0, time.UTC)},
		{"CDT abbreviation", "Thu, 24 Sep 2026 10:00:00 CDT", time.Date(2026, time.September, 24, 15, 0, 0, 0, time.UTC)},
		{"RFC3339", "2026-09-24T10:00:00-04:00", time.Date(2026, time.September, 24, 14, 0, 0, 0, time.UTC)},
		{"empty", "", time.Time{}},
		{"garbage", "not a date at all", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFeedTime(tt.value)
			assert.True(t, got.Equal(tt.want), "got %v, want %v", got, tt.want)
			if !tt.want.IsZero() {
				assert.Equal(t, time.UTC, got.Location())
			}
		})
	}
}

func TestParseFeedKeepsFullContentAsBody(t *testing.T) {
	body := []byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
<item><title>Canucks sign Ferraro</title><link>https://x.example.com/1</link>
  <description><![CDATA[The Canucks signed Mario Ferraro.]]></description>
  <content:encoded><![CDATA[<p>The Canucks signed Mario Ferraro.</p><p>He gets two years.</p>]]></content:encoded></item>
<item><title>Top 10 prospects ranked</title><link>https://x.example.com/2</link>
  <description><![CDATA[]]></description>
  <content:encoded><![CDATA[ Our annual ranking of the league's best prospects. ]]></content:encoded></item>
<item><title>Celebrini named captain</title><link>https://x.example.com/3</link>
  <description>The Sharks named Macklin Celebrini captain.</description>
  <content:encoded><![CDATA[<p><div id="video"></div><script>player.load({id: 1});</script></p>]]></content:encoded></item>
</channel></rss>`)
	items, err := ParseFeed(body, retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 3)

	assert.Equal(t, "The Canucks signed Mario Ferraro.", items[0].Text)
	assert.Equal(t, "The Canucks signed Mario Ferraro.\n\nHe gets two years.", items[0].Body)

	assert.Equal(t, "Our annual ranking of the league's best prospects.", items[1].Text,
		"an empty description falls back to the content")
	assert.Empty(t, items[1].Body, "content that is only the summary is not a body")

	assert.Equal(t, "The Sharks named Macklin Celebrini captain.", items[2].Text)
	assert.Empty(t, items[2].Body, "a video embed has no story text")
}

func TestParseFeedAtomContentIsBody(t *testing.T) {
	body := []byte(`<feed xmlns="http://www.w3.org/2005/Atom"><entry><id>1</id><title>Ferraro signs</title>
<link href="https://x.example.com/1"/><summary>Ferraro signs.</summary>
<content type="html">&lt;p&gt;Ferraro signs.&lt;/p&gt;&lt;p&gt;Two years.&lt;/p&gt;</content></entry></feed>`)
	items, err := ParseFeed(body, retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "Ferraro signs.", items[0].Text)
	assert.Equal(t, "Ferraro signs.\n\nTwo years.", items[0].Body)
}

func TestParseFeedTakesTheFirstNonEmptyLink(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel><item><title>Ferraro signs</title>
<link><![CDATA[ https://www.sportsnet.ca/nhl/article/ferraro/ ]]></link>
<guid isPermaLink="false">p1</guid>
<link type="app-deep-link-field"></link></item></channel></rss>`)
	items, err := ParseFeed(body, retrievedAt, defaultMaxItems)
	require.NoError(t, err)
	require.Len(t, items, 1, "a trailing empty deep-link element must not erase the item's link")
	assert.Equal(t, "https://www.sportsnet.ca/nhl/article/ferraro/", items[0].URL)
}
