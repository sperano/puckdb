-- Consolidated initial schema (squash of former migrations 000001-000015).
-- Generated from a pg_dump of a database with all 15 migrations applied,
-- followed by the seed data those migrations inserted.

--
--




--
-- Name: chat_role; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.chat_role AS ENUM (
    'system',
    'user',
    'assistant',
    'tool'
);


--
-- Name: game_schedule_state; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.game_schedule_state AS ENUM (
    'OK',
    'DONT_PLAY',
    'PPD',
    'SUSP',
    'TBD',
    'COMPLETED',
    'CNCL'
);


--
-- Name: game_state; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.game_state AS ENUM (
    'FUT',
    'PRE',
    'LIVE',
    'FINAL',
    'OFF',
    'PPD',
    'SUSP',
    'CRIT'
);


--
-- Name: game_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.game_type AS ENUM (
    'preseason',
    'regular_season',
    'playoffs',
    'all_star',
    'world_cup',
    'world_cup_2004',
    'world_cup_pre_tournament',
    'olympics',
    'young_stars',
    'pwhl_showcase',
    'lockout_lost',
    'canada_cup',
    'exhibition_overseas',
    'womens_all_star',
    'four_nations'
);


--
-- Name: goalie_decision; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.goalie_decision AS ENUM (
    'W',
    'L',
    'T',
    'OTL'
);


--
-- Name: hand_side; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.hand_side AS ENUM (
    'L',
    'R'
);


--
-- Name: ice_side; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.ice_side AS ENUM (
    'left',
    'right'
);


--
-- Name: official_role; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.official_role AS ENUM (
    'referee',
    'linesman'
);


--
-- Name: period_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.period_type AS ENUM (
    'REG',
    'OT',
    'SO'
);


--
-- Name: play_event_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.play_event_type AS ENUM (
    'faceoff',
    'hit',
    'giveaway',
    'goal',
    'shot-on-goal',
    'missed-shot',
    'blocked-shot',
    'penalty',
    'stoppage',
    'period-start',
    'period-end',
    'shootout-complete',
    'game-end',
    'takeaway',
    'delayed-penalty',
    'failed-shot-attempt'
);


--
-- Name: player_position; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.player_position AS ENUM (
    'C',
    'LW',
    'RW',
    'F',
    'D',
    'G'
);


--
-- Name: shift_detail; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.shift_detail AS ENUM (
    '0',
    '801',
    '802',
    '803',
    '804',
    '805',
    '806',
    '807',
    '808',
    '809',
    '810',
    '811'
);


--
-- Name: shift_type; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.shift_type AS ENUM (
    '505',
    '517'
);


--
-- Name: shootout_result; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.shootout_result AS ENUM (
    'goal',
    'save'
);


--
-- Name: team_kind_enum; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.team_kind_enum AS ENUM (
    'nhl',
    'international'
);


--
-- Name: zone_code; Type: TYPE; Schema: public; Owner: -
--

CREATE TYPE public.zone_code AS ENUM (
    'O',
    'D',
    'N'
);




--
-- Name: club_goalie_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.club_goalie_stats (
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL,
    games_played integer NOT NULL,
    games_started integer NOT NULL,
    wins integer NOT NULL,
    losses integer NOT NULL,
    overtime_losses integer NOT NULL,
    goals_against_average real NOT NULL,
    save_percentage real NOT NULL,
    shots_against integer NOT NULL,
    saves integer NOT NULL,
    goals_against integer NOT NULL,
    shutouts integer NOT NULL,
    goals integer NOT NULL,
    assists integer NOT NULL,
    points integer NOT NULL,
    penalty_minutes integer NOT NULL,
    toi_seconds bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: club_skater_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.club_skater_stats (
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL,
    games_played integer NOT NULL,
    goals integer NOT NULL,
    assists integer NOT NULL,
    points integer NOT NULL,
    plus_minus integer NOT NULL,
    penalty_minutes integer NOT NULL,
    power_play_goals integer NOT NULL,
    shorthanded_goals integer NOT NULL,
    game_winning_goals integer NOT NULL,
    overtime_goals integer NOT NULL,
    shots integer NOT NULL,
    shooting_pctg real NOT NULL,
    avg_toi_per_game real NOT NULL,
    avg_shifts_per_game real NOT NULL,
    faceoff_win_pctg real NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_goalie_shot_location_summary; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_goalie_shot_location_summary (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    location_code text NOT NULL,
    goals_against integer,
    goals_against_percentile real,
    goals_against_league_avg real,
    saves integer,
    saves_percentile real,
    saves_league_avg real,
    save_pctg real,
    save_pctg_percentile real,
    save_pctg_league_avg real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT edge_goalie_shot_location_summary_location_code_check CHECK ((location_code = ANY (ARRAY['all'::text, 'high'::text, 'long'::text, 'mid'::text])))
);


--
-- Name: edge_goalie_shot_locations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_goalie_shot_locations (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    area text NOT NULL,
    saves integer,
    saves_percentile real,
    save_pctg real,
    save_pctg_percentile real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_goalie_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_goalie_stats (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    gaa_value real,
    gaa_percentile real,
    gaa_league_avg real,
    games_above_900_value real,
    games_above_900_percentile real,
    games_above_900_league_avg real,
    goal_diff_per_60_value real,
    goal_diff_per_60_percentile real,
    goal_diff_per_60_league_avg real,
    goal_support_avg_value real,
    goal_support_avg_percentile real,
    goal_support_avg_league_avg real,
    point_pctg_value real,
    point_pctg_percentile real,
    point_pctg_league_avg real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_skater_shot_locations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_skater_shot_locations (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    area text NOT NULL,
    sog integer,
    goals integer,
    shooting_pctg real,
    sog_percentile real,
    goals_percentile real,
    shooting_pctg_percentile real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_skater_sog_summary; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_skater_sog_summary (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    location_code text NOT NULL,
    shots integer,
    shots_percentile real,
    shots_league_avg real,
    goals integer,
    goals_percentile real,
    goals_league_avg real,
    shooting_pctg real,
    shooting_pctg_percentile real,
    shooting_pctg_league_avg real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT edge_skater_sog_summary_location_code_check CHECK ((location_code = ANY (ARRAY['all'::text, 'high'::text, 'long'::text, 'mid'::text])))
);


--
-- Name: edge_skater_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_skater_stats (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    top_speed_imperial real,
    top_speed_metric real,
    top_speed_percentile real,
    top_speed_league_avg_imperial real,
    top_speed_league_avg_metric real,
    bursts_over_20 integer,
    bursts_over_20_percentile real,
    bursts_over_20_league_avg real,
    total_distance_imperial real,
    total_distance_metric real,
    total_distance_percentile real,
    max_game_distance_imperial real,
    max_game_distance_metric real,
    max_game_distance_percentile real,
    top_shot_speed_imperial real,
    top_shot_speed_metric real,
    top_shot_speed_percentile real,
    top_shot_speed_league_avg_imperial real,
    top_shot_speed_league_avg_metric real,
    oz_pctg real,
    oz_percentile real,
    oz_league_avg real,
    nz_pctg real,
    nz_percentile real,
    nz_league_avg real,
    dz_pctg real,
    dz_percentile real,
    dz_league_avg real,
    oz_ev_pctg real,
    oz_ev_percentile real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_team_shot_differential; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_team_shot_differential (
    team_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    shot_attempt_differential real,
    shot_attempt_differential_rank integer,
    sog_differential real,
    sog_differential_rank integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_team_shot_locations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_team_shot_locations (
    team_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    area text NOT NULL,
    shots integer,
    shots_rank integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_team_sog_summary; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_team_sog_summary (
    team_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    location_code text NOT NULL,
    shots integer,
    shots_rank integer,
    shots_league_avg real,
    goals integer,
    goals_rank integer,
    goals_league_avg real,
    shooting_pctg real,
    shooting_pctg_rank integer,
    shooting_pctg_league_avg real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT edge_team_sog_summary_location_code_check CHECK ((location_code = ANY (ARRAY['all'::text, 'high'::text, 'long'::text, 'mid'::text])))
);


--
-- Name: edge_team_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_team_stats (
    team_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    shot_attempts_over_90 integer,
    shot_attempts_over_90_rank integer,
    top_shot_speed_imperial real,
    top_shot_speed_metric real,
    top_shot_speed_rank integer,
    bursts_over_22 integer,
    bursts_over_22_rank integer,
    bursts_over_20 integer,
    bursts_over_20_rank integer,
    speed_max_imperial real,
    speed_max_metric real,
    speed_max_rank integer,
    total_distance integer,
    total_distance_rank integer,
    oz_pctg real,
    oz_rank integer,
    oz_league_avg real,
    oz_ev_pctg real,
    oz_ev_rank integer,
    nz_pctg real,
    nz_rank integer,
    nz_league_avg real,
    dz_pctg real,
    dz_rank integer,
    dz_league_avg real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: edge_team_zone_time_by_strength; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.edge_team_zone_time_by_strength (
    team_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    strength_code text NOT NULL,
    oz_pctg real,
    oz_rank integer,
    nz_pctg real,
    nz_rank integer,
    dz_pctg real,
    dz_rank integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT edge_team_zone_time_by_strength_strength_code_check CHECK ((strength_code = ANY (ARRAY['all'::text, 'es'::text, 'pp'::text, 'pk'::text])))
);


--
-- Name: franchises; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.franchises (
    id bigint NOT NULL,
    full_name text NOT NULL,
    team_common_name text NOT NULL,
    team_place_name text NOT NULL
);


--
-- Name: game_broadcasts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_broadcasts (
    game_id bigint NOT NULL,
    broadcast_id bigint NOT NULL,
    market text NOT NULL,
    country_code text NOT NULL,
    network text NOT NULL,
    sequence_number integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: game_coaches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_coaches (
    game_id bigint NOT NULL,
    team_id bigint NOT NULL,
    head_coach text NOT NULL
);


--
-- Name: game_goalie_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_goalie_stats (
    game_id bigint NOT NULL,
    player_id bigint NOT NULL,
    team_id bigint NOT NULL,
    is_home boolean NOT NULL,
    sweater_number smallint NOT NULL,
    decision public.goalie_decision,
    starter boolean,
    shots_against integer DEFAULT 0 NOT NULL,
    saves integer DEFAULT 0 NOT NULL,
    save_pctg real,
    goals_against smallint DEFAULT 0 NOT NULL,
    even_strength_goals_against smallint DEFAULT 0 NOT NULL,
    power_play_goals_against smallint DEFAULT 0 NOT NULL,
    shorthanded_goals_against smallint DEFAULT 0 NOT NULL,
    even_strength_shots_against text,
    power_play_shots_against text,
    shorthanded_shots_against text,
    toi_seconds integer DEFAULT 0 NOT NULL,
    penalty_minutes smallint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: game_officials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_officials (
    game_id bigint NOT NULL,
    role public.official_role NOT NULL,
    sequence smallint NOT NULL,
    name text NOT NULL
);


--
-- Name: game_scratches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_scratches (
    game_id bigint NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL
);


--
-- Name: game_skater_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_skater_stats (
    game_id bigint NOT NULL,
    player_id bigint NOT NULL,
    team_id bigint NOT NULL,
    is_home boolean NOT NULL,
    sweater_number smallint NOT NULL,
    "position" public.player_position NOT NULL,
    goals smallint DEFAULT 0 NOT NULL,
    assists smallint DEFAULT 0 NOT NULL,
    points smallint DEFAULT 0 NOT NULL,
    plus_minus smallint DEFAULT 0 NOT NULL,
    shots_on_goal smallint DEFAULT 0 NOT NULL,
    toi_seconds integer DEFAULT 0 NOT NULL,
    shifts smallint DEFAULT 0 NOT NULL,
    faceoff_winning_pctg real,
    hits smallint DEFAULT 0 NOT NULL,
    blocked_shots smallint DEFAULT 0 NOT NULL,
    penalty_minutes smallint DEFAULT 0 NOT NULL,
    giveaways smallint DEFAULT 0 NOT NULL,
    takeaways smallint DEFAULT 0 NOT NULL,
    power_play_goals smallint DEFAULT 0 NOT NULL,
    power_play_points smallint DEFAULT 0 NOT NULL,
    game_winning_goals smallint DEFAULT 0 NOT NULL,
    ot_goals smallint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: game_three_stars; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.game_three_stars (
    game_id bigint NOT NULL,
    star smallint NOT NULL,
    player_id bigint NOT NULL,
    CONSTRAINT game_three_stars_star_check CHECK (((star >= 1) AND (star <= 3)))
);


--
-- Name: games; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.games (
    id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    game_date date NOT NULL,
    venue text DEFAULT ''::text NOT NULL,
    venue_location text DEFAULT ''::text NOT NULL,
    start_time_utc timestamp with time zone,
    eastern_utc_offset text DEFAULT ''::text NOT NULL,
    venue_utc_offset text DEFAULT ''::text NOT NULL,
    game_state public.game_state DEFAULT 'FUT'::public.game_state NOT NULL,
    game_schedule_state public.game_schedule_state DEFAULT 'OK'::public.game_schedule_state NOT NULL,
    period_number smallint DEFAULT 0 NOT NULL,
    period_type public.period_type DEFAULT 'REG'::public.period_type NOT NULL,
    max_regulation_periods smallint DEFAULT 3 NOT NULL,
    clock_time_remaining text DEFAULT ''::text NOT NULL,
    clock_seconds_remaining integer DEFAULT 0 NOT NULL,
    clock_running boolean DEFAULT false NOT NULL,
    clock_in_intermission boolean DEFAULT false NOT NULL,
    home_team_id bigint NOT NULL,
    home_team_score integer DEFAULT 0 NOT NULL,
    home_team_sog integer DEFAULT 0 NOT NULL,
    away_team_id bigint NOT NULL,
    away_team_score integer DEFAULT 0 NOT NULL,
    away_team_sog integer DEFAULT 0 NOT NULL,
    limited_scoring boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: goal_highlights; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.goal_highlights (
    game_id bigint NOT NULL,
    event_id bigint NOT NULL,
    player_id bigint NOT NULL,
    period smallint NOT NULL,
    time_in_period text NOT NULL,
    goals_to_date smallint,
    highlight_clip_id bigint,
    highlight_clip_url text,
    discrete_clip_id bigint
);


--
-- Name: players; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.players (
    id bigint NOT NULL,
    yahoo_id bigint,
    first_name text NOT NULL,
    last_name text NOT NULL,
    first_name_normalized text DEFAULT ''::text NOT NULL,
    last_name_normalized text DEFAULT ''::text NOT NULL,
    team_id bigint,
    "position" public.player_position,
    shoots_catches public.hand_side,
    height_inches integer,
    weight_pounds integer,
    birth_date date,
    birth_city text,
    birth_state_province text,
    birth_country text,
    sweater_number integer,
    is_active boolean DEFAULT true NOT NULL,
    headshot_url text DEFAULT ''::text NOT NULL,
    hero_image_url text,
    yahoo_image text DEFAULT ''::text NOT NULL,
    yahoo_home_url text DEFAULT ''::text NOT NULL,
    player_slug text,
    draft_year integer,
    draft_team_abbrev text,
    draft_round integer,
    draft_pick_in_round integer,
    draft_overall_pick integer
);


--
-- Name: goalie_recent_stats; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.goalie_recent_stats AS
 SELECT ggs.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.team_id AS current_team_id,
    g.season,
    count(DISTINCT ggs.game_id) AS gp,
    sum(
        CASE
            WHEN (ggs.decision = 'W'::public.goalie_decision) THEN 1
            ELSE 0
        END) AS wins,
    sum(
        CASE
            WHEN (ggs.decision = 'L'::public.goalie_decision) THEN 1
            ELSE 0
        END) AS losses,
    sum(ggs.goals_against) AS ga,
    sum(ggs.saves) AS saves,
    sum(ggs.shots_against) AS shots_against,
    round((((sum(ggs.goals_against))::numeric / (NULLIF(sum(ggs.toi_seconds), 0))::numeric) * (3600)::numeric), 2) AS gaa,
    round(((sum(ggs.saves))::numeric / (NULLIF(sum(ggs.shots_against), 0))::numeric), 3) AS sv_pct
   FROM ((public.game_goalie_stats ggs
     JOIN public.games g ON (((g.id = ggs.game_id) AND (g.game_type = 'regular_season'::public.game_type))))
     JOIN public.players p ON ((p.id = ggs.player_id)))
  WHERE ((ggs.toi_seconds >= 300) AND (g.game_date >= (CURRENT_DATE - '30 days'::interval)))
  GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;


--
-- Name: goalie_season_stats; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.goalie_season_stats AS
 SELECT ggs.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p.team_id AS current_team_id,
    g.season,
    count(DISTINCT ggs.game_id) AS gp,
    sum(
        CASE
            WHEN (ggs.decision = 'W'::public.goalie_decision) THEN 1
            ELSE 0
        END) AS wins,
    sum(
        CASE
            WHEN (ggs.decision = 'L'::public.goalie_decision) THEN 1
            ELSE 0
        END) AS losses,
    sum(ggs.goals_against) AS ga,
    sum(ggs.saves) AS saves,
    sum(ggs.shots_against) AS shots_against,
    round((((sum(ggs.goals_against))::numeric / (NULLIF(sum(ggs.toi_seconds), 0))::numeric) * (3600)::numeric), 2) AS gaa,
    round(((sum(ggs.saves))::numeric / (NULLIF(sum(ggs.shots_against), 0))::numeric), 3) AS sv_pct
   FROM ((public.game_goalie_stats ggs
     JOIN public.games g ON (((g.id = ggs.game_id) AND (g.game_type = 'regular_season'::public.game_type))))
     JOIN public.players p ON ((p.id = ggs.player_id)))
  WHERE (ggs.toi_seconds >= 300)
  GROUP BY ggs.player_id, p.first_name, p.last_name, p.yahoo_id, p.team_id, g.season;


--
-- Name: maurice_conversations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.maurice_conversations (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    title text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: maurice_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.maurice_messages (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    conversation_id uuid NOT NULL,
    role public.chat_role NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    tool_calls jsonb,
    tool_call_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: play_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.play_events (
    game_id bigint NOT NULL,
    event_id bigint NOT NULL,
    period integer NOT NULL,
    period_type public.period_type NOT NULL,
    time_in_period text NOT NULL,
    time_remaining text NOT NULL,
    situation_code integer,
    home_team_defending_side public.ice_side,
    type_desc_key public.play_event_type NOT NULL,
    sort_order integer NOT NULL,
    x_coord integer,
    y_coord integer,
    zone_code public.zone_code,
    event_owner_team_id bigint,
    shot_type text,
    shooting_player_id bigint,
    goalie_in_net_id bigint,
    blocking_player_id bigint,
    scoring_player_id bigint,
    scoring_player_total integer,
    assist1_player_id bigint,
    assist1_player_total integer,
    assist2_player_id bigint,
    assist2_player_total integer,
    away_score integer,
    home_score integer,
    highlight_clip_id bigint,
    highlight_clip_url text,
    discrete_clip_id bigint,
    penalty_type_code text,
    penalty_desc_key text,
    penalty_duration integer,
    committed_by_player_id bigint,
    drawn_by_player_id bigint,
    hitting_player_id bigint,
    hittee_player_id bigint,
    winning_player_id bigint,
    losing_player_id bigint,
    player_id bigint,
    reason text,
    away_sog integer,
    home_sog integer
);


--
-- Name: COLUMN play_events.situation_code; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.play_events.situation_code IS '4-digit integer encoding on-ice strength: [away_goalie][away_skaters][home_skaters][home_goalie]. Example: 1551 = both goalies in, 5v5. 0541 = away empty net, 5v4 home power play.';


--
-- Name: COLUMN play_events.penalty_type_code; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.play_events.penalty_type_code IS 'NHL penalty type code string (e.g. "PS-HOOKING"). See penalty_desc_key for human-readable description.';


--
-- Name: player_awards; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.player_awards (
    player_id bigint NOT NULL,
    trophy_name text NOT NULL,
    season integer NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: player_season_totals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.player_season_totals (
    player_id bigint NOT NULL,
    season integer NOT NULL,
    game_type public.game_type NOT NULL,
    league_abbrev text NOT NULL,
    team_name text NOT NULL,
    team_id bigint,
    sequence integer DEFAULT 0 NOT NULL,
    games_played integer NOT NULL,
    goals integer,
    assists integer,
    points integer,
    plus_minus integer,
    pim integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: season_rosters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.season_rosters (
    season integer NOT NULL,
    team_id bigint NOT NULL,
    player_id bigint NOT NULL,
    "position" public.player_position,
    shoots_catches text NOT NULL,
    sweater_number smallint NOT NULL,
    height_inches smallint NOT NULL,
    weight_pounds smallint NOT NULL,
    birth_date text NOT NULL,
    birth_city text,
    birth_state_province text,
    birth_country text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: season_teams; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.season_teams (
    season integer NOT NULL,
    team_id bigint NOT NULL,
    franchise_id bigint,
    full_name text NOT NULL,
    abbrev text NOT NULL,
    logo_url text,
    division_name text,
    division_abbrev text,
    conference_name text,
    conference_abbrev text,
    team_kind public.team_kind_enum DEFAULT 'nhl'::public.team_kind_enum NOT NULL,
    CONSTRAINT season_teams_nhl_division_required CHECK (((team_kind <> 'nhl'::public.team_kind_enum) OR ((division_name IS NOT NULL) AND (division_abbrev IS NOT NULL))))
);


--
-- Name: seasons; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.seasons (
    id integer NOT NULL,
    standings_start date NOT NULL,
    standings_end date NOT NULL
);


--
-- Name: shifts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.shifts (
    id bigint NOT NULL,
    game_id bigint NOT NULL,
    player_id bigint NOT NULL,
    team_id bigint NOT NULL,
    period integer NOT NULL,
    start_time text NOT NULL,
    end_time text NOT NULL,
    duration text NOT NULL,
    shift_number integer NOT NULL,
    type_code public.shift_type NOT NULL,
    detail_code public.shift_detail NOT NULL,
    event_number bigint NOT NULL,
    event_description text
);


--
-- Name: shootout_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.shootout_attempts (
    game_id bigint NOT NULL,
    sequence smallint NOT NULL,
    player_id bigint NOT NULL,
    team_id bigint NOT NULL,
    shot_type text NOT NULL,
    result public.shootout_result NOT NULL,
    game_winner boolean DEFAULT false NOT NULL
);


--
-- Name: sim_agent_daily_player_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_daily_player_stats (
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    date date NOT NULL,
    player_id bigint NOT NULL,
    category text NOT NULL,
    value numeric DEFAULT 0 NOT NULL,
    goalie_ga integer,
    goalie_toi_seconds integer
);


--
-- Name: sim_agent_daily_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_daily_stats (
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    date date NOT NULL,
    category text NOT NULL,
    value numeric DEFAULT 0 NOT NULL,
    goalie_ga integer,
    goalie_toi_seconds integer
);


--
-- Name: sim_agent_tool_calls; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_tool_calls (
    turn_id integer NOT NULL,
    round_index integer NOT NULL,
    sequence integer NOT NULL,
    tool_name text NOT NULL,
    recovered_name text,
    arguments_raw text NOT NULL,
    arguments jsonb,
    result text NOT NULL,
    outcome text NOT NULL,
    failure_reason text,
    applied_transaction_id integer,
    latency_ms integer DEFAULT 0 NOT NULL,
    CONSTRAINT ck_sim_agent_tool_calls_outcome CHECK ((outcome = ANY (ARRAY['accepted'::text, 'parse_error'::text, 'validation_rejected'::text, 'unknown_tool'::text, 'unhandled'::text])))
);


--
-- Name: sim_agent_totals; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_totals (
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    category text NOT NULL,
    value numeric DEFAULT 0 NOT NULL,
    goalie_ga integer,
    goalie_toi_seconds integer
);


--
-- Name: sim_agent_turn_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_turn_messages (
    turn_id integer NOT NULL,
    ordinal integer NOT NULL,
    role text NOT NULL,
    content text NOT NULL,
    tool_call_id text,
    tool_calls jsonb,
    CONSTRAINT ck_sim_agent_turn_messages_role CHECK ((role = ANY (ARRAY['system'::text, 'user'::text, 'assistant'::text, 'tool'::text])))
);


--
-- Name: sim_agent_turn_rounds; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_turn_rounds (
    turn_id integer NOT NULL,
    round_index integer NOT NULL,
    assistant_text text DEFAULT ''::text NOT NULL,
    prompt_tokens integer DEFAULT 0 NOT NULL,
    completion_tokens integer DEFAULT 0 NOT NULL,
    cache_creation_tokens integer DEFAULT 0 NOT NULL,
    cache_read_tokens integer DEFAULT 0 NOT NULL,
    latency_ms integer DEFAULT 0 NOT NULL
);


--
-- Name: sim_agent_turns; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agent_turns (
    id integer NOT NULL,
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    sim_date date,
    phase text NOT NULL,
    pick_number integer DEFAULT 0 NOT NULL,
    status text NOT NULL,
    skip_reason text,
    error_kind text,
    error_detail text,
    provider text NOT NULL,
    model text NOT NULL,
    temperature numeric,
    max_tokens integer,
    rounds integer DEFAULT 0 NOT NULL,
    prompt_tokens integer DEFAULT 0 NOT NULL,
    completion_tokens integer DEFAULT 0 NOT NULL,
    cache_creation_tokens integer DEFAULT 0 NOT NULL,
    cache_read_tokens integer DEFAULT 0 NOT NULL,
    cost_usd numeric DEFAULT 0 NOT NULL,
    latency_ms integer DEFAULT 0 NOT NULL,
    final_text text DEFAULT ''::text NOT NULL,
    started_at timestamp with time zone NOT NULL,
    completed_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_sim_agent_turns_date_by_phase CHECK ((((phase = 'team_name'::text) AND (sim_date IS NULL)) OR ((phase = ANY (ARRAY['draft'::text, 'daily'::text])) AND (sim_date IS NOT NULL)))),
    CONSTRAINT ck_sim_agent_turns_phase CHECK ((phase = ANY (ARRAY['team_name'::text, 'draft'::text, 'daily'::text]))),
    CONSTRAINT ck_sim_agent_turns_status CHECK ((status = ANY (ARRAY['ok'::text, 'errored'::text, 'skipped'::text])))
);


--
-- Name: sim_agent_turns_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sim_agent_turns_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sim_agent_turns_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sim_agent_turns_id_seq OWNED BY public.sim_agent_turns.id;


--
-- Name: sim_agents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_agents (
    id integer NOT NULL,
    pool_id integer NOT NULL,
    draft_position integer,
    provider text NOT NULL,
    model text NOT NULL,
    strategy text NOT NULL,
    notes text DEFAULT ''::text NOT NULL,
    timeout_seconds integer DEFAULT 0 NOT NULL,
    temperature numeric,
    api_base text DEFAULT ''::text NOT NULL,
    max_tokens integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    team_name text DEFAULT ''::text NOT NULL,
    strategy_summary text DEFAULT ''::text NOT NULL,
    CONSTRAINT sim_agents_notes_check CHECK ((octet_length(notes) <= 50000))
);


--
-- Name: sim_agents_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sim_agents_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sim_agents_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sim_agents_id_seq OWNED BY public.sim_agents.id;


--
-- Name: sim_lineup_moves; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_lineup_moves (
    transaction_id integer NOT NULL,
    sequence integer NOT NULL,
    player_id bigint NOT NULL,
    from_slot text NOT NULL,
    to_slot text NOT NULL,
    displaced_player_id bigint
);


--
-- Name: sim_pools; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_pools (
    id integer NOT NULL,
    name text NOT NULL,
    season integer NOT NULL,
    status text DEFAULT 'draft'::text NOT NULL,
    sim_date date,
    num_teams integer NOT NULL,
    waiver_days integer NOT NULL,
    draft_rounds integer NOT NULL,
    max_llm_cost_usd_per_pool numeric NOT NULL,
    categories text[] NOT NULL,
    roster_c integer DEFAULT 0 NOT NULL,
    roster_lw integer DEFAULT 0 NOT NULL,
    roster_rw integer DEFAULT 0 NOT NULL,
    roster_d integer DEFAULT 0 NOT NULL,
    roster_g integer DEFAULT 0 NOT NULL,
    roster_util integer DEFAULT 0 NOT NULL,
    roster_bn integer DEFAULT 0 NOT NULL,
    roster_ir integer DEFAULT 0 NOT NULL,
    total_llm_cost_usd numeric DEFAULT 0 NOT NULL,
    workflow_id text GENERATED ALWAYS AS (('sim-pool-'::text || (id)::text)) STORED,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    record_full_messages boolean DEFAULT true NOT NULL,
    stop_after text DEFAULT 'never'::text NOT NULL,
    max_season_days integer DEFAULT 0 NOT NULL,
    CONSTRAINT sim_pools_max_season_days_check CHECK ((max_season_days >= 0)),
    CONSTRAINT sim_pools_stop_after_check CHECK ((stop_after = ANY (ARRAY['never'::text, 'team_name'::text, 'draft'::text, 'season'::text])))
);


--
-- Name: sim_pools_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sim_pools_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sim_pools_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sim_pools_id_seq OWNED BY public.sim_pools.id;


--
-- Name: sim_rosters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_rosters (
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    player_id bigint NOT NULL,
    slot text NOT NULL,
    acquired_at date NOT NULL,
    acquired_via text DEFAULT 'draft'::text NOT NULL
);


--
-- Name: sim_standings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_standings (
    pool_id integer NOT NULL,
    date date NOT NULL,
    agent_id integer NOT NULL,
    category text NOT NULL,
    value numeric NOT NULL,
    roto_points numeric NOT NULL
);


--
-- Name: sim_transactions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_transactions (
    id integer NOT NULL,
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    date date NOT NULL,
    type text NOT NULL,
    player_id bigint,
    reasoning text DEFAULT ''::text NOT NULL,
    round integer,
    pick integer,
    drop_player_id bigint,
    error_kind text,
    error_detail text,
    cost_usd numeric,
    cap_usd numeric,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sim_transactions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sim_transactions_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sim_transactions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sim_transactions_id_seq OWNED BY public.sim_transactions.id;


--
-- Name: sim_waiver_claims; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_waiver_claims (
    id integer NOT NULL,
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    player_id bigint NOT NULL,
    drop_player_id bigint,
    filed_date date NOT NULL,
    process_date date NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    resolved_at date,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sim_waiver_claims_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.sim_waiver_claims_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: sim_waiver_claims_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.sim_waiver_claims_id_seq OWNED BY public.sim_waiver_claims.id;


--
-- Name: sim_waiver_priority; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sim_waiver_priority (
    pool_id integer NOT NULL,
    agent_id integer NOT NULL,
    priority integer NOT NULL
);


--
-- Name: skater_recent_stats; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.skater_recent_stats AS
 SELECT gss.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p."position",
    p.team_id AS current_team_id,
    g.season,
    count(DISTINCT gss.game_id) AS gp,
    sum(gss.goals) AS goals,
    sum(gss.assists) AS assists,
    sum(gss.points) AS points,
    sum(gss.plus_minus) AS plus_minus,
    sum(gss.penalty_minutes) AS pim,
    sum(gss.shots_on_goal) AS sog,
    sum(gss.power_play_points) AS ppp,
    sum(gss.power_play_goals) AS ppg,
    sum(gss.hits) AS hits,
    sum(gss.blocked_shots) AS blocks,
    round((((sum(gss.toi_seconds))::numeric / (NULLIF(count(DISTINCT gss.game_id), 0))::numeric) / (60)::numeric), 1) AS avg_toi_min
   FROM ((public.game_skater_stats gss
     JOIN public.games g ON (((g.id = gss.game_id) AND (g.game_type = 'regular_season'::public.game_type))))
     JOIN public.players p ON ((p.id = gss.player_id)))
  WHERE (g.game_date >= (CURRENT_DATE - '30 days'::interval))
  GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p."position", p.team_id, g.season;


--
-- Name: skater_season_stats; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.skater_season_stats AS
 SELECT gss.player_id,
    p.first_name,
    p.last_name,
    p.yahoo_id,
    p."position",
    p.team_id AS current_team_id,
    g.season,
    count(DISTINCT gss.game_id) AS gp,
    sum(gss.goals) AS goals,
    sum(gss.assists) AS assists,
    sum(gss.points) AS points,
    sum(gss.plus_minus) AS plus_minus,
    sum(gss.penalty_minutes) AS pim,
    sum(gss.shots_on_goal) AS sog,
    sum(gss.power_play_points) AS ppp,
    sum(gss.power_play_goals) AS ppg,
    sum(gss.hits) AS hits,
    sum(gss.blocked_shots) AS blocks,
    round((((sum(gss.toi_seconds))::numeric / (NULLIF(count(DISTINCT gss.game_id), 0))::numeric) / (60)::numeric), 1) AS avg_toi_min
   FROM ((public.game_skater_stats gss
     JOIN public.games g ON (((g.id = gss.game_id) AND (g.game_type = 'regular_season'::public.game_type))))
     JOIN public.players p ON ((p.id = gss.player_id)))
  GROUP BY gss.player_id, p.first_name, p.last_name, p.yahoo_id, p."position", p.team_id, g.season;


--
-- Name: standings_snapshots; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.standings_snapshots (
    season integer NOT NULL,
    date date NOT NULL,
    team_id bigint NOT NULL,
    team_abbrev text NOT NULL,
    wins integer NOT NULL,
    losses integer NOT NULL,
    ot_losses integer NOT NULL,
    points integer NOT NULL,
    division_abbrev text NOT NULL,
    division_name text NOT NULL,
    conference_abbrev text,
    conference_name text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_draft_results; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_draft_results (
    league_id integer NOT NULL,
    round integer NOT NULL,
    pick integer NOT NULL,
    team_id integer NOT NULL,
    player_id integer NOT NULL,
    cost integer,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_league_roster_positions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_league_roster_positions (
    league_id integer NOT NULL,
    "position" text NOT NULL,
    position_type text DEFAULT ''::text NOT NULL,
    count integer DEFAULT 1 NOT NULL,
    is_starting_position boolean DEFAULT true NOT NULL
);


--
-- Name: yahoo_league_stat_categories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_league_stat_categories (
    league_id integer NOT NULL,
    stat_id integer NOT NULL,
    name text NOT NULL,
    abbr text DEFAULT ''::text NOT NULL,
    stat_group text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    value real
);


--
-- Name: yahoo_leagues; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_leagues (
    id integer NOT NULL,
    league_key text NOT NULL,
    name text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    logo_url text DEFAULT ''::text NOT NULL,
    season integer NOT NULL,
    game_code text DEFAULT 'nhl'::text NOT NULL,
    num_teams integer DEFAULT 0 NOT NULL,
    scoring_type text DEFAULT ''::text NOT NULL,
    league_type text DEFAULT ''::text NOT NULL,
    draft_status text DEFAULT ''::text NOT NULL,
    is_pro_league boolean DEFAULT false NOT NULL,
    is_cash_league boolean DEFAULT false NOT NULL,
    start_date date,
    end_date date,
    draft_type text DEFAULT ''::text NOT NULL,
    is_auction_draft boolean DEFAULT false NOT NULL,
    draft_time timestamp with time zone,
    draft_pick_time integer,
    waiver_type text DEFAULT ''::text NOT NULL,
    waiver_rule text DEFAULT ''::text NOT NULL,
    waiver_time integer,
    trade_end_date date,
    trade_ratify_type text DEFAULT ''::text NOT NULL,
    trade_reject_time integer,
    max_teams integer,
    player_pool text DEFAULT ''::text NOT NULL,
    post_draft_players text DEFAULT ''::text NOT NULL,
    cant_cut_list text DEFAULT ''::text NOT NULL,
    uses_playoff boolean DEFAULT true NOT NULL,
    persistent_url text DEFAULT ''::text NOT NULL,
    league_update_timestamp bigint,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_matchups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_matchups (
    league_id integer NOT NULL,
    week integer NOT NULL,
    team1_id integer NOT NULL,
    team2_id integer NOT NULL,
    team1_points real,
    team2_points real,
    status text,
    is_playoffs boolean DEFAULT false NOT NULL,
    is_consolation boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_team_rosters; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_team_rosters (
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    date date NOT NULL,
    player_id integer NOT NULL,
    coverage_type text DEFAULT 'date'::text NOT NULL,
    is_editable boolean DEFAULT false NOT NULL,
    player_key text DEFAULT ''::text NOT NULL,
    selected_position text DEFAULT ''::text NOT NULL,
    is_flex boolean DEFAULT false NOT NULL,
    player_status text,
    player_status_full text,
    injury_note text,
    on_disabled_list boolean,
    position_type text,
    display_position text,
    primary_position text,
    eligible_positions text[],
    uniform_number integer,
    editorial_team_abbr text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_roster_players; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.yahoo_roster_players AS
 SELECT r.league_id,
    r.team_id,
    r.date,
    r.player_id AS yahoo_player_id,
    r.player_key,
    r.selected_position,
    r.is_flex,
    r.coverage_type,
    r.is_editable,
    p.id AS nhl_player_id,
    p.first_name,
    p.last_name,
    p."position" AS nhl_position,
    p.team_id AS nhl_team_id,
    p.is_active,
    p.headshot_url
   FROM (public.yahoo_team_rosters r
     LEFT JOIN public.players p ON ((p.yahoo_id = r.player_id)));


--
-- Name: yahoo_team_summaries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_team_summaries (
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    date date NOT NULL,
    coverage_type text DEFAULT 'date'::text NOT NULL,
    goals real,
    assists real,
    points real,
    plus_minus real,
    pim real,
    ppp real,
    sog real,
    faceoffs_won real,
    faceoffs_lost real,
    wins real,
    goals_against real,
    gaa real,
    shots_against real,
    saves real,
    save_pct real,
    shutouts real,
    shp real,
    gwg real,
    hits real,
    blocks real,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_teams; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_teams (
    league_id integer NOT NULL,
    id integer NOT NULL,
    team_key text NOT NULL,
    name text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    logo_url text DEFAULT ''::text NOT NULL,
    draft_position integer,
    waiver_priority integer,
    number_of_moves integer DEFAULT 0 NOT NULL,
    number_of_trades integer DEFAULT 0 NOT NULL,
    is_owned_by_current_login boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: yahoo_season_team_totals; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.yahoo_season_team_totals AS
 SELECT s.league_id,
    s.team_id,
    t.name AS team_name,
    l.season,
    l.num_teams,
    l.scoring_type,
    sum(COALESCE(s.goals, (0)::real)) AS goals,
    sum(COALESCE(s.assists, (0)::real)) AS assists,
    sum(COALESCE(s.plus_minus, (0)::real)) AS plus_minus,
    sum(COALESCE(s.pim, (0)::real)) AS pim,
    sum(COALESCE(s.ppp, (0)::real)) AS ppp,
    sum(COALESCE(s.sog, (0)::real)) AS sog,
    sum(COALESCE(s.wins, (0)::real)) AS wins,
    sum(COALESCE(s.goals_against, (0)::real)) AS ga
   FROM ((public.yahoo_team_summaries s
     JOIN public.yahoo_teams t ON (((t.league_id = s.league_id) AND (t.id = s.team_id))))
     JOIN public.yahoo_leagues l ON ((l.id = s.league_id)))
  GROUP BY s.league_id, s.team_id, t.name, l.season, l.num_teams, l.scoring_type;


--
-- Name: yahoo_roto_standings; Type: VIEW; Schema: public; Owner: -
--

CREATE VIEW public.yahoo_roto_standings AS
 WITH ranked AS (
         SELECT st.league_id,
            st.team_id,
            st.team_name,
            st.season,
            st.num_teams,
            st.scoring_type,
            st.goals,
            st.assists,
            st.plus_minus,
            st.pim,
            st.ppp,
            st.sog,
            st.wins,
            st.ga,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.goals DESC) AS g_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.assists DESC) AS a_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.plus_minus DESC) AS pm_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.pim DESC) AS pim_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.ppp DESC) AS ppp_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.sog DESC) AS sog_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.wins DESC) AS w_rank,
            rank() OVER (PARTITION BY st.league_id ORDER BY st.ga) AS ga_rank
           FROM public.yahoo_season_team_totals st
          WHERE (st.scoring_type = 'roto'::text)
        )
 SELECT league_id,
    team_id,
    team_name,
    season,
    num_teams,
    goals,
    (((num_teams + 1) - g_rank))::integer AS g_pts,
    assists,
    (((num_teams + 1) - a_rank))::integer AS a_pts,
    plus_minus,
    (((num_teams + 1) - pm_rank))::integer AS pm_pts,
    pim,
    (((num_teams + 1) - pim_rank))::integer AS pim_pts,
    ppp,
    (((num_teams + 1) - ppp_rank))::integer AS ppp_pts,
    sog,
    (((num_teams + 1) - sog_rank))::integer AS sog_pts,
    wins,
    (((num_teams + 1) - w_rank))::integer AS w_pts,
    ga,
    (((num_teams + 1) - ga_rank))::integer AS ga_pts,
    (((((((((num_teams + 1) - g_rank) + ((num_teams + 1) - a_rank)) + ((num_teams + 1) - pm_rank)) + ((num_teams + 1) - pim_rank)) + ((num_teams + 1) - ppp_rank)) + ((num_teams + 1) - sog_rank)) + ((num_teams + 1) - w_rank)) + ((num_teams + 1) - ga_rank)) AS total_roto_pts
   FROM ranked r
  ORDER BY league_id, (((((((((num_teams + 1) - g_rank) + ((num_teams + 1) - a_rank)) + ((num_teams + 1) - pm_rank)) + ((num_teams + 1) - pim_rank)) + ((num_teams + 1) - ppp_rank)) + ((num_teams + 1) - sog_rank)) + ((num_teams + 1) - w_rank)) + ((num_teams + 1) - ga_rank)) DESC;


--
-- Name: yahoo_team_managers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_team_managers (
    league_id integer NOT NULL,
    team_id integer NOT NULL,
    id integer NOT NULL,
    nickname text DEFAULT ''::text NOT NULL,
    guid text DEFAULT ''::text NOT NULL,
    email text DEFAULT ''::text NOT NULL,
    image_url text DEFAULT ''::text NOT NULL,
    felo_score integer DEFAULT 0 NOT NULL,
    felo_tier text DEFAULT ''::text NOT NULL,
    is_current_login boolean DEFAULT false NOT NULL,
    is_commissioner boolean DEFAULT false NOT NULL
);


--
-- Name: yahoo_transaction_players; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_transaction_players (
    league_id integer NOT NULL,
    transaction_key text NOT NULL,
    player_id integer NOT NULL,
    player_key text DEFAULT ''::text NOT NULL,
    type text NOT NULL,
    source_type text DEFAULT ''::text NOT NULL,
    source_team_key text DEFAULT ''::text NOT NULL,
    destination_type text DEFAULT ''::text NOT NULL,
    destination_team_key text DEFAULT ''::text NOT NULL
);


--
-- Name: yahoo_transactions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.yahoo_transactions (
    league_id integer NOT NULL,
    transaction_key text NOT NULL,
    type text NOT NULL,
    "timestamp" bigint,
    status text,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: sim_agent_turns id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turns ALTER COLUMN id SET DEFAULT nextval('public.sim_agent_turns_id_seq'::regclass);


--
-- Name: sim_agents id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agents ALTER COLUMN id SET DEFAULT nextval('public.sim_agents_id_seq'::regclass);


--
-- Name: sim_pools id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_pools ALTER COLUMN id SET DEFAULT nextval('public.sim_pools_id_seq'::regclass);


--
-- Name: sim_transactions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions ALTER COLUMN id SET DEFAULT nextval('public.sim_transactions_id_seq'::regclass);


--
-- Name: sim_waiver_claims id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims ALTER COLUMN id SET DEFAULT nextval('public.sim_waiver_claims_id_seq'::regclass);


--
-- Name: club_goalie_stats club_goalie_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_goalie_stats
    ADD CONSTRAINT club_goalie_stats_pkey PRIMARY KEY (season, game_type, team_id, player_id);


--
-- Name: club_skater_stats club_skater_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_skater_stats
    ADD CONSTRAINT club_skater_stats_pkey PRIMARY KEY (season, game_type, team_id, player_id);


--
-- Name: edge_goalie_shot_location_summary edge_goalie_shot_location_summary_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_location_summary
    ADD CONSTRAINT edge_goalie_shot_location_summary_pkey PRIMARY KEY (player_id, season, game_type, location_code);


--
-- Name: edge_goalie_shot_locations edge_goalie_shot_locations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_locations
    ADD CONSTRAINT edge_goalie_shot_locations_pkey PRIMARY KEY (player_id, season, game_type, area);


--
-- Name: edge_goalie_stats edge_goalie_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_stats
    ADD CONSTRAINT edge_goalie_stats_pkey PRIMARY KEY (player_id, season, game_type);


--
-- Name: edge_skater_shot_locations edge_skater_shot_locations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_shot_locations
    ADD CONSTRAINT edge_skater_shot_locations_pkey PRIMARY KEY (player_id, season, game_type, area);


--
-- Name: edge_skater_sog_summary edge_skater_sog_summary_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_sog_summary
    ADD CONSTRAINT edge_skater_sog_summary_pkey PRIMARY KEY (player_id, season, game_type, location_code);


--
-- Name: edge_skater_stats edge_skater_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_stats
    ADD CONSTRAINT edge_skater_stats_pkey PRIMARY KEY (player_id, season, game_type);


--
-- Name: edge_team_shot_differential edge_team_shot_differential_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_differential
    ADD CONSTRAINT edge_team_shot_differential_pkey PRIMARY KEY (team_id, season, game_type);


--
-- Name: edge_team_shot_locations edge_team_shot_locations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_locations
    ADD CONSTRAINT edge_team_shot_locations_pkey PRIMARY KEY (team_id, season, game_type, area);


--
-- Name: edge_team_sog_summary edge_team_sog_summary_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_sog_summary
    ADD CONSTRAINT edge_team_sog_summary_pkey PRIMARY KEY (team_id, season, game_type, location_code);


--
-- Name: edge_team_stats edge_team_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_stats
    ADD CONSTRAINT edge_team_stats_pkey PRIMARY KEY (team_id, season, game_type);


--
-- Name: edge_team_zone_time_by_strength edge_team_zone_time_by_strength_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_zone_time_by_strength
    ADD CONSTRAINT edge_team_zone_time_by_strength_pkey PRIMARY KEY (team_id, season, game_type, strength_code);


--
-- Name: franchises franchises_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.franchises
    ADD CONSTRAINT franchises_pkey PRIMARY KEY (id);


--
-- Name: game_broadcasts game_broadcasts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_broadcasts
    ADD CONSTRAINT game_broadcasts_pkey PRIMARY KEY (game_id, broadcast_id);


--
-- Name: game_coaches game_coaches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_coaches
    ADD CONSTRAINT game_coaches_pkey PRIMARY KEY (game_id, team_id);


--
-- Name: game_goalie_stats game_goalie_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_goalie_stats
    ADD CONSTRAINT game_goalie_stats_pkey PRIMARY KEY (game_id, player_id);


--
-- Name: game_officials game_officials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_officials
    ADD CONSTRAINT game_officials_pkey PRIMARY KEY (game_id, role, sequence);


--
-- Name: game_scratches game_scratches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_scratches
    ADD CONSTRAINT game_scratches_pkey PRIMARY KEY (game_id, player_id);


--
-- Name: game_skater_stats game_skater_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_skater_stats
    ADD CONSTRAINT game_skater_stats_pkey PRIMARY KEY (game_id, player_id);


--
-- Name: game_three_stars game_three_stars_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_three_stars
    ADD CONSTRAINT game_three_stars_pkey PRIMARY KEY (game_id, star);


--
-- Name: games games_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.games
    ADD CONSTRAINT games_pkey PRIMARY KEY (id);


--
-- Name: goal_highlights goal_highlights_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.goal_highlights
    ADD CONSTRAINT goal_highlights_pkey PRIMARY KEY (game_id, event_id);


--
-- Name: maurice_conversations maurice_conversations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maurice_conversations
    ADD CONSTRAINT maurice_conversations_pkey PRIMARY KEY (id);


--
-- Name: maurice_messages maurice_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maurice_messages
    ADD CONSTRAINT maurice_messages_pkey PRIMARY KEY (id);


--
-- Name: play_events play_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.play_events
    ADD CONSTRAINT play_events_pkey PRIMARY KEY (game_id, event_id);


--
-- Name: player_awards player_awards_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_awards
    ADD CONSTRAINT player_awards_pkey PRIMARY KEY (player_id, trophy_name, season);


--
-- Name: player_season_totals player_season_totals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_season_totals
    ADD CONSTRAINT player_season_totals_pkey PRIMARY KEY (player_id, season, game_type, league_abbrev, sequence);


--
-- Name: players players_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.players
    ADD CONSTRAINT players_pkey PRIMARY KEY (id);


--
-- Name: players players_yahoo_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.players
    ADD CONSTRAINT players_yahoo_id_key UNIQUE (yahoo_id);


--
-- Name: season_rosters season_rosters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_rosters
    ADD CONSTRAINT season_rosters_pkey PRIMARY KEY (season, team_id, player_id);


--
-- Name: season_teams season_teams_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_teams
    ADD CONSTRAINT season_teams_pkey PRIMARY KEY (season, team_id);


--
-- Name: seasons seasons_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.seasons
    ADD CONSTRAINT seasons_pkey PRIMARY KEY (id);


--
-- Name: shifts shifts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shifts
    ADD CONSTRAINT shifts_pkey PRIMARY KEY (id);


--
-- Name: shootout_attempts shootout_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shootout_attempts
    ADD CONSTRAINT shootout_attempts_pkey PRIMARY KEY (game_id, sequence);


--
-- Name: sim_agent_daily_player_stats sim_agent_daily_player_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_player_stats
    ADD CONSTRAINT sim_agent_daily_player_stats_pkey PRIMARY KEY (pool_id, agent_id, date, player_id, category);


--
-- Name: sim_agent_daily_stats sim_agent_daily_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_stats
    ADD CONSTRAINT sim_agent_daily_stats_pkey PRIMARY KEY (pool_id, agent_id, date, category);


--
-- Name: sim_agent_tool_calls sim_agent_tool_calls_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_tool_calls
    ADD CONSTRAINT sim_agent_tool_calls_pkey PRIMARY KEY (turn_id, round_index, sequence);


--
-- Name: sim_agent_totals sim_agent_totals_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_totals
    ADD CONSTRAINT sim_agent_totals_pkey PRIMARY KEY (pool_id, agent_id, category);


--
-- Name: sim_agent_turn_messages sim_agent_turn_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turn_messages
    ADD CONSTRAINT sim_agent_turn_messages_pkey PRIMARY KEY (turn_id, ordinal);


--
-- Name: sim_agent_turn_rounds sim_agent_turn_rounds_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turn_rounds
    ADD CONSTRAINT sim_agent_turn_rounds_pkey PRIMARY KEY (turn_id, round_index);


--
-- Name: sim_agent_turns sim_agent_turns_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turns
    ADD CONSTRAINT sim_agent_turns_pkey PRIMARY KEY (id);


--
-- Name: sim_agents sim_agents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agents
    ADD CONSTRAINT sim_agents_pkey PRIMARY KEY (id);


--
-- Name: sim_lineup_moves sim_lineup_moves_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_lineup_moves
    ADD CONSTRAINT sim_lineup_moves_pkey PRIMARY KEY (transaction_id, sequence);


--
-- Name: sim_pools sim_pools_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_pools
    ADD CONSTRAINT sim_pools_pkey PRIMARY KEY (id);


--
-- Name: sim_rosters sim_rosters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_rosters
    ADD CONSTRAINT sim_rosters_pkey PRIMARY KEY (pool_id, agent_id, player_id);


--
-- Name: sim_rosters sim_rosters_pool_id_player_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_rosters
    ADD CONSTRAINT sim_rosters_pool_id_player_id_key UNIQUE (pool_id, player_id);


--
-- Name: sim_standings sim_standings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_standings
    ADD CONSTRAINT sim_standings_pkey PRIMARY KEY (pool_id, date, agent_id, category);


--
-- Name: sim_transactions sim_transactions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions
    ADD CONSTRAINT sim_transactions_pkey PRIMARY KEY (id);


--
-- Name: sim_waiver_claims sim_waiver_claims_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims
    ADD CONSTRAINT sim_waiver_claims_pkey PRIMARY KEY (id);


--
-- Name: sim_waiver_priority sim_waiver_priority_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_priority
    ADD CONSTRAINT sim_waiver_priority_pkey PRIMARY KEY (pool_id, agent_id);


--
-- Name: standings_snapshots standings_snapshots_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.standings_snapshots
    ADD CONSTRAINT standings_snapshots_pkey PRIMARY KEY (season, date, team_id);


--
-- Name: yahoo_draft_results yahoo_draft_results_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_draft_results
    ADD CONSTRAINT yahoo_draft_results_pkey PRIMARY KEY (league_id, round, pick);


--
-- Name: yahoo_league_roster_positions yahoo_league_roster_positions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_league_roster_positions
    ADD CONSTRAINT yahoo_league_roster_positions_pkey PRIMARY KEY (league_id, "position");


--
-- Name: yahoo_league_stat_categories yahoo_league_stat_categories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_league_stat_categories
    ADD CONSTRAINT yahoo_league_stat_categories_pkey PRIMARY KEY (league_id, stat_id);


--
-- Name: yahoo_leagues yahoo_leagues_league_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_leagues
    ADD CONSTRAINT yahoo_leagues_league_key_key UNIQUE (league_key);


--
-- Name: yahoo_leagues yahoo_leagues_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_leagues
    ADD CONSTRAINT yahoo_leagues_pkey PRIMARY KEY (id);


--
-- Name: yahoo_matchups yahoo_matchups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_pkey PRIMARY KEY (league_id, week, team1_id, team2_id);


--
-- Name: yahoo_team_managers yahoo_team_managers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_managers
    ADD CONSTRAINT yahoo_team_managers_pkey PRIMARY KEY (league_id, team_id, id);


--
-- Name: yahoo_team_rosters yahoo_team_rosters_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_rosters
    ADD CONSTRAINT yahoo_team_rosters_pkey PRIMARY KEY (league_id, team_id, date, player_id);


--
-- Name: yahoo_team_summaries yahoo_team_summaries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_summaries
    ADD CONSTRAINT yahoo_team_summaries_pkey PRIMARY KEY (league_id, team_id, date);


--
-- Name: yahoo_teams yahoo_teams_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_teams
    ADD CONSTRAINT yahoo_teams_pkey PRIMARY KEY (league_id, id);


--
-- Name: yahoo_teams yahoo_teams_team_key_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_teams
    ADD CONSTRAINT yahoo_teams_team_key_key UNIQUE (team_key);


--
-- Name: yahoo_transaction_players yahoo_transaction_players_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_transaction_players
    ADD CONSTRAINT yahoo_transaction_players_pkey PRIMARY KEY (league_id, transaction_key, player_id);


--
-- Name: yahoo_transactions yahoo_transactions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_transactions
    ADD CONSTRAINT yahoo_transactions_pkey PRIMARY KEY (league_id, transaction_key);


--
-- Name: idx_edge_goalie_stats_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_edge_goalie_stats_season ON public.edge_goalie_stats USING btree (season, game_type);


--
-- Name: idx_edge_skater_stats_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_edge_skater_stats_season ON public.edge_skater_stats USING btree (season, game_type);


--
-- Name: idx_edge_team_stats_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_edge_team_stats_season ON public.edge_team_stats USING btree (season, game_type);


--
-- Name: idx_game_goalie_stats_decision; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_goalie_stats_decision ON public.game_goalie_stats USING btree (decision) WHERE (decision IS NOT NULL);


--
-- Name: idx_game_goalie_stats_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_goalie_stats_player ON public.game_goalie_stats USING btree (player_id);


--
-- Name: idx_game_goalie_stats_starter; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_goalie_stats_starter ON public.game_goalie_stats USING btree (starter) WHERE (starter = true);


--
-- Name: idx_game_goalie_stats_team; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_goalie_stats_team ON public.game_goalie_stats USING btree (team_id);


--
-- Name: idx_game_scratches_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_scratches_player ON public.game_scratches USING btree (player_id);


--
-- Name: idx_game_skater_stats_gwg; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_skater_stats_gwg ON public.game_skater_stats USING btree (game_winning_goals) WHERE (game_winning_goals > 0);


--
-- Name: idx_game_skater_stats_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_skater_stats_player ON public.game_skater_stats USING btree (player_id);


--
-- Name: idx_game_skater_stats_position; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_skater_stats_position ON public.game_skater_stats USING btree ("position");


--
-- Name: idx_game_skater_stats_team; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_skater_stats_team ON public.game_skater_stats USING btree (team_id);


--
-- Name: idx_game_three_stars_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_game_three_stars_player ON public.game_three_stars USING btree (player_id);


--
-- Name: idx_games_away_team; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_away_team ON public.games USING btree (away_team_id);


--
-- Name: idx_games_game_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_game_date ON public.games USING btree (game_date);


--
-- Name: idx_games_game_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_game_state ON public.games USING btree (game_state);


--
-- Name: idx_games_game_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_game_type ON public.games USING btree (game_type);


--
-- Name: idx_games_home_team; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_home_team ON public.games USING btree (home_team_id);


--
-- Name: idx_games_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_season ON public.games USING btree (season);


--
-- Name: idx_games_season_game_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_games_season_game_type ON public.games USING btree (season, game_type);


--
-- Name: idx_goal_highlights_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_goal_highlights_player ON public.goal_highlights USING btree (player_id);


--
-- Name: idx_maurice_conversations_updated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_maurice_conversations_updated ON public.maurice_conversations USING btree (updated_at DESC);


--
-- Name: idx_maurice_messages_conversation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_maurice_messages_conversation ON public.maurice_messages USING btree (conversation_id, created_at);


--
-- Name: idx_play_events_game_period; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_play_events_game_period ON public.play_events USING btree (game_id, period);


--
-- Name: idx_play_events_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_play_events_type ON public.play_events USING btree (type_desc_key);


--
-- Name: idx_player_awards_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_player_awards_season ON public.player_awards USING btree (season);


--
-- Name: idx_player_awards_trophy; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_player_awards_trophy ON public.player_awards USING btree (trophy_name);


--
-- Name: idx_player_season_totals_league; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_player_season_totals_league ON public.player_season_totals USING btree (league_abbrev);


--
-- Name: idx_player_season_totals_team_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_player_season_totals_team_id ON public.player_season_totals USING btree (team_id) WHERE (team_id IS NOT NULL);


--
-- Name: idx_players_is_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_players_is_active ON public.players USING btree (is_active);


--
-- Name: idx_players_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_players_name ON public.players USING btree (last_name, first_name);


--
-- Name: idx_players_name_normalized; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_players_name_normalized ON public.players USING btree (last_name_normalized, first_name_normalized);


--
-- Name: idx_players_position; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_players_position ON public.players USING btree ("position");


--
-- Name: idx_players_team; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_players_team ON public.players USING btree (team_id);


--
-- Name: idx_season_rosters_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_season_rosters_player ON public.season_rosters USING btree (player_id);


--
-- Name: idx_season_teams_abbrev; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_season_teams_abbrev ON public.season_teams USING btree (abbrev);


--
-- Name: idx_season_teams_division; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_season_teams_division ON public.season_teams USING btree (division_name);


--
-- Name: idx_season_teams_franchise; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_season_teams_franchise ON public.season_teams USING btree (franchise_id);


--
-- Name: idx_shifts_game_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shifts_game_player ON public.shifts USING btree (game_id, player_id);


--
-- Name: idx_shifts_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shifts_player ON public.shifts USING btree (player_id);


--
-- Name: idx_shootout_attempts_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shootout_attempts_player ON public.shootout_attempts USING btree (player_id);


--
-- Name: idx_sim_agent_daily_player_stats_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_agent_daily_player_stats_player ON public.sim_agent_daily_player_stats USING btree (pool_id, player_id, date);


--
-- Name: idx_sim_agent_tool_calls_outcome; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_agent_tool_calls_outcome ON public.sim_agent_tool_calls USING btree (turn_id, outcome);


--
-- Name: idx_sim_agent_tool_calls_tx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_agent_tool_calls_tx ON public.sim_agent_tool_calls USING btree (applied_transaction_id) WHERE (applied_transaction_id IS NOT NULL);


--
-- Name: idx_sim_agent_turns_pool_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_agent_turns_pool_time ON public.sim_agent_turns USING btree (pool_id, started_at);


--
-- Name: idx_sim_rosters_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_rosters_active ON public.sim_rosters USING btree (pool_id, slot) WHERE (slot <> ALL (ARRAY['BN'::text, 'IR'::text]));


--
-- Name: idx_sim_transactions_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_transactions_lookup ON public.sim_transactions USING btree (pool_id, date, agent_id);


--
-- Name: idx_sim_waiver_claims_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sim_waiver_claims_pending ON public.sim_waiver_claims USING btree (pool_id, process_date) WHERE (status = 'pending'::text);


--
-- Name: idx_standings_snapshots_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_standings_snapshots_date ON public.standings_snapshots USING btree (date);


--
-- Name: idx_standings_snapshots_team_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_standings_snapshots_team_id ON public.standings_snapshots USING btree (team_id);


--
-- Name: idx_yahoo_league_stat_categories_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_league_stat_categories_enabled ON public.yahoo_league_stat_categories USING btree (league_id) WHERE (enabled = true);


--
-- Name: idx_yahoo_leagues_season; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_leagues_season ON public.yahoo_leagues USING btree (season);


--
-- Name: idx_yahoo_team_managers_guid; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_team_managers_guid ON public.yahoo_team_managers USING btree (guid);


--
-- Name: idx_yahoo_team_rosters_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_team_rosters_date ON public.yahoo_team_rosters USING btree (date);


--
-- Name: idx_yahoo_team_rosters_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_team_rosters_player ON public.yahoo_team_rosters USING btree (player_id);


--
-- Name: idx_yahoo_team_summaries_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_team_summaries_date ON public.yahoo_team_summaries USING btree (date);


--
-- Name: idx_yahoo_transaction_players_player; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_transaction_players_player ON public.yahoo_transaction_players USING btree (player_id);


--
-- Name: idx_yahoo_transactions_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_yahoo_transactions_type ON public.yahoo_transactions USING btree (league_id, type);


--
-- Name: ux_sim_agent_turns_no_date; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ux_sim_agent_turns_no_date ON public.sim_agent_turns USING btree (pool_id, agent_id, phase) WHERE (sim_date IS NULL);


--
-- Name: ux_sim_agent_turns_with_date; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ux_sim_agent_turns_with_date ON public.sim_agent_turns USING btree (pool_id, agent_id, sim_date, phase, pick_number) WHERE (sim_date IS NOT NULL);


--
-- Name: ux_sim_waiver_claims_pending_one_per_agent_player; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX ux_sim_waiver_claims_pending_one_per_agent_player ON public.sim_waiver_claims USING btree (pool_id, agent_id, player_id) WHERE (status = 'pending'::text);


--
-- Name: club_goalie_stats club_goalie_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_goalie_stats
    ADD CONSTRAINT club_goalie_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: club_goalie_stats club_goalie_stats_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_goalie_stats
    ADD CONSTRAINT club_goalie_stats_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: club_skater_stats club_skater_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_skater_stats
    ADD CONSTRAINT club_skater_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: club_skater_stats club_skater_stats_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.club_skater_stats
    ADD CONSTRAINT club_skater_stats_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: edge_goalie_shot_location_summary edge_goalie_shot_location_summary_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_location_summary
    ADD CONSTRAINT edge_goalie_shot_location_summary_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_goalie_shot_location_summary edge_goalie_shot_location_summary_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_location_summary
    ADD CONSTRAINT edge_goalie_shot_location_summary_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_goalie_shot_locations edge_goalie_shot_locations_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_locations
    ADD CONSTRAINT edge_goalie_shot_locations_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_goalie_shot_locations edge_goalie_shot_locations_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_shot_locations
    ADD CONSTRAINT edge_goalie_shot_locations_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_goalie_stats edge_goalie_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_stats
    ADD CONSTRAINT edge_goalie_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_goalie_stats edge_goalie_stats_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_goalie_stats
    ADD CONSTRAINT edge_goalie_stats_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_skater_shot_locations edge_skater_shot_locations_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_shot_locations
    ADD CONSTRAINT edge_skater_shot_locations_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_skater_shot_locations edge_skater_shot_locations_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_shot_locations
    ADD CONSTRAINT edge_skater_shot_locations_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_skater_sog_summary edge_skater_sog_summary_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_sog_summary
    ADD CONSTRAINT edge_skater_sog_summary_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_skater_sog_summary edge_skater_sog_summary_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_sog_summary
    ADD CONSTRAINT edge_skater_sog_summary_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_skater_stats edge_skater_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_stats
    ADD CONSTRAINT edge_skater_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: edge_skater_stats edge_skater_stats_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_skater_stats
    ADD CONSTRAINT edge_skater_stats_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_shot_differential edge_team_shot_differential_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_differential
    ADD CONSTRAINT edge_team_shot_differential_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_shot_differential edge_team_shot_differential_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_differential
    ADD CONSTRAINT edge_team_shot_differential_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: edge_team_shot_locations edge_team_shot_locations_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_locations
    ADD CONSTRAINT edge_team_shot_locations_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_shot_locations edge_team_shot_locations_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_shot_locations
    ADD CONSTRAINT edge_team_shot_locations_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: edge_team_sog_summary edge_team_sog_summary_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_sog_summary
    ADD CONSTRAINT edge_team_sog_summary_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_sog_summary edge_team_sog_summary_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_sog_summary
    ADD CONSTRAINT edge_team_sog_summary_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: edge_team_stats edge_team_stats_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_stats
    ADD CONSTRAINT edge_team_stats_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_stats edge_team_stats_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_stats
    ADD CONSTRAINT edge_team_stats_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: edge_team_zone_time_by_strength edge_team_zone_time_by_strength_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_zone_time_by_strength
    ADD CONSTRAINT edge_team_zone_time_by_strength_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: edge_team_zone_time_by_strength edge_team_zone_time_by_strength_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.edge_team_zone_time_by_strength
    ADD CONSTRAINT edge_team_zone_time_by_strength_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: game_broadcasts game_broadcasts_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_broadcasts
    ADD CONSTRAINT game_broadcasts_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_coaches game_coaches_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_coaches
    ADD CONSTRAINT game_coaches_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_goalie_stats game_goalie_stats_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_goalie_stats
    ADD CONSTRAINT game_goalie_stats_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_goalie_stats game_goalie_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_goalie_stats
    ADD CONSTRAINT game_goalie_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: game_officials game_officials_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_officials
    ADD CONSTRAINT game_officials_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_scratches game_scratches_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_scratches
    ADD CONSTRAINT game_scratches_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_scratches game_scratches_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_scratches
    ADD CONSTRAINT game_scratches_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: game_skater_stats game_skater_stats_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_skater_stats
    ADD CONSTRAINT game_skater_stats_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_skater_stats game_skater_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_skater_stats
    ADD CONSTRAINT game_skater_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: game_three_stars game_three_stars_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_three_stars
    ADD CONSTRAINT game_three_stars_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: game_three_stars game_three_stars_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.game_three_stars
    ADD CONSTRAINT game_three_stars_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: goal_highlights goal_highlights_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.goal_highlights
    ADD CONSTRAINT goal_highlights_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: goal_highlights goal_highlights_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.goal_highlights
    ADD CONSTRAINT goal_highlights_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: maurice_messages maurice_messages_conversation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.maurice_messages
    ADD CONSTRAINT maurice_messages_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.maurice_conversations(id) ON DELETE CASCADE;


--
-- Name: play_events play_events_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.play_events
    ADD CONSTRAINT play_events_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id);


--
-- Name: player_awards player_awards_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_awards
    ADD CONSTRAINT player_awards_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: player_season_totals player_season_totals_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_season_totals
    ADD CONSTRAINT player_season_totals_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: player_season_totals player_season_totals_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.player_season_totals
    ADD CONSTRAINT player_season_totals_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id) ON DELETE SET NULL (team_id);


--
-- Name: season_rosters season_rosters_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_rosters
    ADD CONSTRAINT season_rosters_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: season_rosters season_rosters_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_rosters
    ADD CONSTRAINT season_rosters_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: season_rosters season_rosters_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_rosters
    ADD CONSTRAINT season_rosters_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: season_teams season_teams_franchise_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_teams
    ADD CONSTRAINT season_teams_franchise_id_fkey FOREIGN KEY (franchise_id) REFERENCES public.franchises(id);


--
-- Name: season_teams season_teams_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.season_teams
    ADD CONSTRAINT season_teams_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: shifts shifts_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shifts
    ADD CONSTRAINT shifts_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id);


--
-- Name: shootout_attempts shootout_attempts_game_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shootout_attempts
    ADD CONSTRAINT shootout_attempts_game_id_fkey FOREIGN KEY (game_id) REFERENCES public.games(id) ON DELETE CASCADE;


--
-- Name: shootout_attempts shootout_attempts_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shootout_attempts
    ADD CONSTRAINT shootout_attempts_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_agent_daily_player_stats sim_agent_daily_player_stats_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_player_stats
    ADD CONSTRAINT sim_agent_daily_player_stats_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_agent_daily_player_stats sim_agent_daily_player_stats_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_player_stats
    ADD CONSTRAINT sim_agent_daily_player_stats_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_agent_daily_player_stats sim_agent_daily_player_stats_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_player_stats
    ADD CONSTRAINT sim_agent_daily_player_stats_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_agent_daily_stats sim_agent_daily_stats_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_stats
    ADD CONSTRAINT sim_agent_daily_stats_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_agent_daily_stats sim_agent_daily_stats_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_daily_stats
    ADD CONSTRAINT sim_agent_daily_stats_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_agent_tool_calls sim_agent_tool_calls_applied_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_tool_calls
    ADD CONSTRAINT sim_agent_tool_calls_applied_transaction_id_fkey FOREIGN KEY (applied_transaction_id) REFERENCES public.sim_transactions(id) ON DELETE SET NULL;


--
-- Name: sim_agent_tool_calls sim_agent_tool_calls_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_tool_calls
    ADD CONSTRAINT sim_agent_tool_calls_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.sim_agent_turns(id) ON DELETE CASCADE;


--
-- Name: sim_agent_totals sim_agent_totals_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_totals
    ADD CONSTRAINT sim_agent_totals_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_agent_totals sim_agent_totals_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_totals
    ADD CONSTRAINT sim_agent_totals_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_agent_turn_messages sim_agent_turn_messages_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turn_messages
    ADD CONSTRAINT sim_agent_turn_messages_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.sim_agent_turns(id) ON DELETE CASCADE;


--
-- Name: sim_agent_turn_rounds sim_agent_turn_rounds_turn_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turn_rounds
    ADD CONSTRAINT sim_agent_turn_rounds_turn_id_fkey FOREIGN KEY (turn_id) REFERENCES public.sim_agent_turns(id) ON DELETE CASCADE;


--
-- Name: sim_agent_turns sim_agent_turns_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turns
    ADD CONSTRAINT sim_agent_turns_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_agent_turns sim_agent_turns_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agent_turns
    ADD CONSTRAINT sim_agent_turns_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_agents sim_agents_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_agents
    ADD CONSTRAINT sim_agents_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_lineup_moves sim_lineup_moves_displaced_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_lineup_moves
    ADD CONSTRAINT sim_lineup_moves_displaced_player_id_fkey FOREIGN KEY (displaced_player_id) REFERENCES public.players(id);


--
-- Name: sim_lineup_moves sim_lineup_moves_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_lineup_moves
    ADD CONSTRAINT sim_lineup_moves_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_lineup_moves sim_lineup_moves_transaction_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_lineup_moves
    ADD CONSTRAINT sim_lineup_moves_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES public.sim_transactions(id) ON DELETE CASCADE;


--
-- Name: sim_pools sim_pools_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_pools
    ADD CONSTRAINT sim_pools_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: sim_rosters sim_rosters_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_rosters
    ADD CONSTRAINT sim_rosters_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_rosters sim_rosters_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_rosters
    ADD CONSTRAINT sim_rosters_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_rosters sim_rosters_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_rosters
    ADD CONSTRAINT sim_rosters_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_standings sim_standings_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_standings
    ADD CONSTRAINT sim_standings_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_standings sim_standings_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_standings
    ADD CONSTRAINT sim_standings_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_transactions sim_transactions_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions
    ADD CONSTRAINT sim_transactions_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_transactions sim_transactions_drop_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions
    ADD CONSTRAINT sim_transactions_drop_player_id_fkey FOREIGN KEY (drop_player_id) REFERENCES public.players(id);


--
-- Name: sim_transactions sim_transactions_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions
    ADD CONSTRAINT sim_transactions_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_transactions sim_transactions_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_transactions
    ADD CONSTRAINT sim_transactions_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_waiver_claims sim_waiver_claims_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims
    ADD CONSTRAINT sim_waiver_claims_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_waiver_claims sim_waiver_claims_drop_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims
    ADD CONSTRAINT sim_waiver_claims_drop_player_id_fkey FOREIGN KEY (drop_player_id) REFERENCES public.players(id);


--
-- Name: sim_waiver_claims sim_waiver_claims_player_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims
    ADD CONSTRAINT sim_waiver_claims_player_id_fkey FOREIGN KEY (player_id) REFERENCES public.players(id);


--
-- Name: sim_waiver_claims sim_waiver_claims_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_claims
    ADD CONSTRAINT sim_waiver_claims_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: sim_waiver_priority sim_waiver_priority_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_priority
    ADD CONSTRAINT sim_waiver_priority_agent_id_fkey FOREIGN KEY (agent_id) REFERENCES public.sim_agents(id) ON DELETE CASCADE;


--
-- Name: sim_waiver_priority sim_waiver_priority_pool_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sim_waiver_priority
    ADD CONSTRAINT sim_waiver_priority_pool_id_fkey FOREIGN KEY (pool_id) REFERENCES public.sim_pools(id) ON DELETE CASCADE;


--
-- Name: standings_snapshots standings_snapshots_season_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.standings_snapshots
    ADD CONSTRAINT standings_snapshots_season_fkey FOREIGN KEY (season) REFERENCES public.seasons(id);


--
-- Name: standings_snapshots standings_snapshots_season_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.standings_snapshots
    ADD CONSTRAINT standings_snapshots_season_team_id_fkey FOREIGN KEY (season, team_id) REFERENCES public.season_teams(season, team_id);


--
-- Name: yahoo_draft_results yahoo_draft_results_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_draft_results
    ADD CONSTRAINT yahoo_draft_results_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id);


--
-- Name: yahoo_draft_results yahoo_draft_results_league_id_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_draft_results
    ADD CONSTRAINT yahoo_draft_results_league_id_team_id_fkey FOREIGN KEY (league_id, team_id) REFERENCES public.yahoo_teams(league_id, id);


--
-- Name: yahoo_league_roster_positions yahoo_league_roster_positions_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_league_roster_positions
    ADD CONSTRAINT yahoo_league_roster_positions_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id) ON DELETE CASCADE;


--
-- Name: yahoo_league_stat_categories yahoo_league_stat_categories_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_league_stat_categories
    ADD CONSTRAINT yahoo_league_stat_categories_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id) ON DELETE CASCADE;


--
-- Name: yahoo_matchups yahoo_matchups_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id);


--
-- Name: yahoo_matchups yahoo_matchups_league_id_team1_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_league_id_team1_id_fkey FOREIGN KEY (league_id, team1_id) REFERENCES public.yahoo_teams(league_id, id);


--
-- Name: yahoo_matchups yahoo_matchups_league_id_team2_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_matchups
    ADD CONSTRAINT yahoo_matchups_league_id_team2_id_fkey FOREIGN KEY (league_id, team2_id) REFERENCES public.yahoo_teams(league_id, id);


--
-- Name: yahoo_team_managers yahoo_team_managers_league_id_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_managers
    ADD CONSTRAINT yahoo_team_managers_league_id_team_id_fkey FOREIGN KEY (league_id, team_id) REFERENCES public.yahoo_teams(league_id, id) ON DELETE CASCADE;


--
-- Name: yahoo_team_rosters yahoo_team_rosters_league_id_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_rosters
    ADD CONSTRAINT yahoo_team_rosters_league_id_team_id_fkey FOREIGN KEY (league_id, team_id) REFERENCES public.yahoo_teams(league_id, id) ON DELETE CASCADE;


--
-- Name: yahoo_team_summaries yahoo_team_summaries_league_id_team_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_team_summaries
    ADD CONSTRAINT yahoo_team_summaries_league_id_team_id_fkey FOREIGN KEY (league_id, team_id) REFERENCES public.yahoo_teams(league_id, id) ON DELETE CASCADE;


--
-- Name: yahoo_teams yahoo_teams_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_teams
    ADD CONSTRAINT yahoo_teams_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id) ON DELETE CASCADE;


--
-- Name: yahoo_transaction_players yahoo_transaction_players_league_id_transaction_key_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_transaction_players
    ADD CONSTRAINT yahoo_transaction_players_league_id_transaction_key_fkey FOREIGN KEY (league_id, transaction_key) REFERENCES public.yahoo_transactions(league_id, transaction_key) ON DELETE CASCADE;


--
-- Name: yahoo_transactions yahoo_transactions_league_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.yahoo_transactions
    ADD CONSTRAINT yahoo_transactions_league_id_fkey FOREIGN KEY (league_id) REFERENCES public.yahoo_leagues(id);


--
--



--
-- Seed data (from former migrations 000008_seed_data and 000015_lockout_season)
--

-- Seed data: pre-NHL western league teams for Stanley Cup records.
-- These teams competed for the Stanley Cup against NHL teams (1917-1925).
-- The NHL API records their appearances under leagueAbbrev="NHL" but provides
-- no team_id. We assign synthetic IDs 70-74 in a range unused by the NHL API.
--
-- seasons rows are inserted first to satisfy season_teams FK.
-- These will be overwritten with real dates when the NHL API manifest is fetched.

INSERT INTO seasons (id, standings_start, standings_end) VALUES
  (19171918, '1917-12-19', '1918-03-20'),
  (19181919, '1918-12-21', '1919-03-10'),
  (19191920, '1919-12-23', '1920-03-10'),
  (19201921, '1920-12-22', '1921-03-14'),
  (19211922, '1921-12-21', '1922-03-11'),
  (19221923, '1922-12-16', '1923-03-05'),
  (19231924, '1923-12-15', '1924-03-07'),
  (19241925, '1924-11-29', '1925-03-09'),
  (19251926, '1925-11-26', '1926-03-17')
ON CONFLICT (id) DO NOTHING;

-- Vancouver Millionaires (PCHA) — Cup finalist 1918, 1921, 1922
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19171918, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19201921, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL),
  (19211922, 70, 'Vancouver Millionaires', 'VMI', 'PCHA', 'PCHA', NULL, NULL);

-- Seattle Metropolitans (PCHA) — Cup finalist 1919, 1920
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19181919, 71, 'Seattle Metropolitans', 'SMT', 'PCHA', 'PCHA', NULL, NULL),
  (19191920, 71, 'Seattle Metropolitans', 'SMT', 'PCHA', 'PCHA', NULL, NULL);

-- Edmonton Eskimos (WCHL) — Cup finalist 1923
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19221923, 72, 'Edmonton Eskimos', 'EDK', 'WCHL', 'WCHL', NULL, NULL);

-- Vancouver Maroons (PCHA/WCHL) — Cup finalist 1923, 1924
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19221923, 73, 'Vancouver Maroons', 'VMR', 'PCHA', 'PCHA', NULL, NULL),
  (19231924, 73, 'Vancouver Maroons', 'VMR', 'WCHL', 'WCHL', NULL, NULL);

-- Victoria Cougars (WCHL) — won Cup 1925, finalist 1926
INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev, conference_name, conference_abbrev)
VALUES
  (19241925, 74, 'Victoria Cougars', 'VIC', 'WCHL', 'WCHL', NULL, NULL),
  (19251926, 74, 'Victoria Cougars', 'VIC', 'WHL', 'WHL', NULL, NULL);

-- 2004-05 NHL season was cancelled by lockout (no NHL games), but the
-- World Cup of Hockey 2004 happened that year. Player landings include
-- WCH 20042005 rows; the international team upsert needs the season
-- to exist to satisfy season_teams_season_fkey. Dates use the NHL's
-- originally-scheduled window for the cancelled season.
INSERT INTO seasons (id, standings_start, standings_end)
VALUES (20042005, '2004-10-13', '2005-04-17')
ON CONFLICT (id) DO NOTHING;
