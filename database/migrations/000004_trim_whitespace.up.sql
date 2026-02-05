-- Trim leading/trailing whitespace from text fields
-- NHL API sometimes includes trailing spaces in names (e.g., "Matěj " instead of "Matěj")

UPDATE players SET
    first_name = TRIM(first_name),
    last_name = TRIM(last_name),
    first_name_normalized = TRIM(first_name_normalized),
    last_name_normalized = TRIM(last_name_normalized),
    birth_city = TRIM(birth_city),
    birth_state_province = TRIM(birth_state_province),
    birth_country = TRIM(birth_country),
    headshot_url = TRIM(headshot_url),
    hero_image_url = TRIM(hero_image_url),
    player_slug = TRIM(player_slug),
    draft_team_abbrev = TRIM(draft_team_abbrev)
WHERE
    first_name != TRIM(first_name) OR
    last_name != TRIM(last_name) OR
    first_name_normalized != TRIM(first_name_normalized) OR
    last_name_normalized != TRIM(last_name_normalized) OR
    birth_city != TRIM(birth_city) OR
    birth_state_province != TRIM(birth_state_province) OR
    birth_country != TRIM(birth_country) OR
    headshot_url != TRIM(headshot_url) OR
    hero_image_url != TRIM(hero_image_url) OR
    player_slug != TRIM(player_slug) OR
    draft_team_abbrev != TRIM(draft_team_abbrev);
