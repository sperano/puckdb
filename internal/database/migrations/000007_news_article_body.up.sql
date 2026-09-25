-- Full story text for news article versions, next to the bounded
-- evidence_text summary that reports and classification keep using. Filled
-- from NHL.com story pages (sources with fetch_body) and from a feed's own
-- full content (RSS content:encoded, Atom content); empty when the source
-- offers no more than its summary.
ALTER TABLE news_article_versions ADD COLUMN body text DEFAULT ''::text NOT NULL;

-- The source's update time of an article as last seen in its feed, refreshed
-- on every sighting even when the content is unchanged. A story page is
-- downloaded again only when this moves, so a re-stamped story whose content
-- did not change is not fetched on every refresh.
ALTER TABLE news_articles ADD COLUMN source_updated_at timestamp with time zone;
