"use strict";
// Tests for the draft board client: the league selector filled from
// draftLeagues (seasons, empty and failed lists), and overlapping requests:
// league switches with responses completing in either order, unequal version
// counters between leagues, failed requests for the new league, and mutations
// while switching.

const test = require("node:test");
const assert = require("node:assert/strict");
const { SEASON, LEAGUE_A, LEAGUE_B, startApp, loadApp, boardOf } = require("./harness_test.js");

const OLD_LEAGUE_SYNC_VERSION = 100;
const NEW_LEAGUE_SYNC_VERSION = 1;
const OLD_LEAGUE_STATE_VERSION = 7;
const NEW_LEAGUE_STATE_VERSION = 2;

function assertShows(app, league, syncVersion) {
  const board = app.state().board;
  assert.ok(board, `expected the ${league.name} board, got none`);
  assert.equal(board.league.leagueKey, league.leagueKey);
  assert.equal(board.sync.syncVersion, syncVersion);
  assert.match(app.el("board-league").textContent, new RegExp(league.name));
  assert.match(app.el("available").text(), new RegExp(league.player));
}

function assertShowsNoBoard(app) {
  assert.equal(app.state().board, null);
  assert.doesNotMatch(app.el("available").text(), new RegExp(LEAGUE_A.player));
  assert.doesNotMatch(app.el("board-league").textContent, new RegExp(LEAGUE_A.name));
}

const OTHER_SEASON = SEASON - 1;
const SEASON_CONTROLS = ["watch-start", "watch-stop", "refresh"];

// assertNoLeagueSelected checks that nothing targets a league: no board
// request, and session controls and filters send nothing.
function assertNoLeagueSelected(app) {
  SEASON_CONTROLS.forEach((control) => app.click(control));
  app.el("scenario").dispatch("change", { target: { value: "CONSERVATIVE" } });
  assert.equal(app.boardRequests().length, 0);
  assert.equal(app.mutationRequests().length, 0);
  assert.equal(app.state().selection.league, "");
}

test("the league selector lists draftLeagues and boards use each league's season", async () => {
  const app = startApp();
  const leagues = app.lastLeagueRequest();
  assert.ok(leagues, "no draftLeagues request");
  assert.equal(leagues.variables.season, null, "without ?season= the server picks the season");
  assert.equal(app.boardRequests().length, 0, "no board before a league is known");

  await leagues.respondLeagues([LEAGUE_A, LEAGUE_B]);
  const options = app.el("league").children;
  assert.deepEqual(options.map((option) => option.value), [LEAGUE_A.id, LEAGUE_B.id]);
  assert.match(options[1].textContent, new RegExp(`${SEASON}.*${LEAGUE_B.name}.*${LEAGUE_B.id}`));
  assert.equal(app.el("league").disabled, false);
  assert.deepEqual(
    { league: app.lastBoardRequest().input.league, season: app.lastBoardRequest().input.season },
    { league: LEAGUE_A.id, season: SEASON },
  );

  app.selectLeague(LEAGUE_B);
  assert.deepEqual(
    { league: app.lastBoardRequest().input.league, season: app.lastBoardRequest().input.season },
    { league: LEAGUE_B.id, season: SEASON },
  );
});

test("?season= lists and opens that season's leagues", async () => {
  const app = startApp(`?season=${OTHER_SEASON}`);
  assert.equal(app.lastLeagueRequest().variables.season, OTHER_SEASON);
  await app.lastLeagueRequest().respondLeagues([{ ...LEAGUE_A, season: OTHER_SEASON }]);
  assert.equal(app.lastBoardRequest().input.season, OTHER_SEASON);
  app.click("watch-start");
  assert.deepEqual(app.lastMutationRequest().input, { league: LEAGUE_A.id, season: OTHER_SEASON });
});

test("an invalid ?season= is reported without asking for leagues", () => {
  const app = startApp("?season=next");
  assert.equal(app.requests.length, 0);
  assert.match(app.el("connection").textContent, /\?season= parameter must be a season start year/);
  assert.equal(app.pendingTimers(), 0, "an invalid parameter cannot fix itself, so nothing retries");
  assertNoLeagueSelected(app);
});

// MAX_GRAPHQL_INT is the largest season a GraphQL Int variable can carry.
const MAX_GRAPHQL_INT = 2147483647;

test("a ?season= beyond a GraphQL Int is reported without asking for leagues", () => {
  const app = startApp(`?season=${MAX_GRAPHQL_INT + 1}`);
  assert.equal(app.requests.length, 0);
  assert.match(app.el("connection").textContent, /\?season= parameter must be a season start year/);
  assert.equal(app.pendingTimers(), 0);
  assertNoLeagueSelected(app);
});

test("the largest GraphQL Int is still sent as ?season=", () => {
  const app = startApp(`?season=${MAX_GRAPHQL_INT}`);
  assert.equal(app.lastLeagueRequest().variables.season, MAX_GRAPHQL_INT);
});

test("the first ranked league opens and unranked ones stay listed with their status", async () => {
  const app = startApp();
  await app.lastLeagueRequest().respondLeagues([{ ...LEAGUE_A, status: "NOT_COMPUTED" }, LEAGUE_B]);
  const options = app.el("league").children;
  assert.deepEqual(options.map((option) => option.value), [LEAGUE_A.id, LEAGUE_B.id]);
  assert.match(options[0].textContent, /NOT_COMPUTED$/);
  assert.doesNotMatch(options[1].textContent, /READY/);
  assert.equal(app.el("league").value, LEAGUE_B.id);
  assert.equal(app.boardRequests().length, 1);
  assert.equal(app.lastBoardRequest().input.league, LEAGUE_B.id);

  app.selectLeague(LEAGUE_A);
  assert.equal(app.lastBoardRequest().input.league, LEAGUE_A.id);
});

test("without a ranked league the first league opens", async () => {
  const app = startApp();
  await app.lastLeagueRequest().respondLeagues([{ ...LEAGUE_A, status: "FAILED" }, { ...LEAGUE_B, status: "REFRESHING" }]);
  assert.equal(app.lastBoardRequest().input.league, LEAGUE_A.id);
});

test("an empty league list is reported, targets nothing, and is retried", async () => {
  const app = startApp();
  await app.lastLeagueRequest().respondLeagues([]);
  assert.match(app.el("connection").textContent, /No draft leagues for the current season/);
  assert.match(app.el("league").text(), /No leagues/);
  assertNoLeagueSelected(app);

  app.fireTimer();
  assert.equal(app.leagueRequests().length, 2);
  await app.lastLeagueRequest().respondLeagues([LEAGUE_B]);
  assert.equal(app.lastBoardRequest().input.league, LEAGUE_B.id);
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});

test("an empty list for a named season names it", async () => {
  const app = startApp(`?season=${OTHER_SEASON}`);
  await app.lastLeagueRequest().respondLeagues([]);
  assert.match(app.el("connection").textContent, new RegExp(`No draft leagues for the ${OTHER_SEASON} season`));
});

test("a failed league list is reported, targets nothing, and is retried", async () => {
  const app = startApp();
  await app.lastLeagueRequest().fail("draft rankings are not available");
  assert.match(app.el("connection").textContent, /Draft leagues unavailable · draft rankings are not available/);
  assertNoLeagueSelected(app);

  app.fireTimer();
  await app.lastLeagueRequest().respondLeagues([LEAGUE_A]);
  assert.equal(app.lastBoardRequest().input.league, LEAGUE_A.id);
});

// showingLeagueA returns an app that displays league A at the old versions.
async function showingLeagueA() {
  const app = await loadApp();
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_A, {
    syncVersion: OLD_LEAGUE_SYNC_VERSION, stateVersion: OLD_LEAGUE_STATE_VERSION,
  }));
  assertShows(app, LEAGUE_A, OLD_LEAGUE_SYNC_VERSION);
  return app;
}

test("old league response completing first cannot replace the new league board", async () => {
  const app = await loadApp();
  const requestA = app.lastBoardRequest();
  app.selectLeague(LEAGUE_B);
  const requestB = app.lastBoardRequest();
  assert.equal(requestA.input.league, LEAGUE_A.id);
  assert.equal(requestB.input.league, LEAGUE_B.id);

  await requestA.respondBoard(boardOf(LEAGUE_A, { syncVersion: OLD_LEAGUE_SYNC_VERSION }));
  assertShowsNoBoard(app);

  await requestB.respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);

  app.fireTimer();
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION + 1 }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION + 1);
});

test("old league response completing last cannot replace the new league board", async () => {
  const app = await loadApp();
  const requestA = app.lastBoardRequest();
  app.selectLeague(LEAGUE_B);
  const requestB = app.lastBoardRequest();

  await requestB.respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);

  await requestA.respondBoard(boardOf(LEAGUE_A, { syncVersion: OLD_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});

test("old league failure after the switch does not mark the new board disconnected", async () => {
  const app = await loadApp();
  const requestA = app.lastBoardRequest();
  app.selectLeague(LEAGUE_B);
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));

  await requestA.fail("league A unavailable");
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
  assert.doesNotMatch(app.el("connection").textContent, /Disconnected/);
});

test("switching clears the old board at once and accepts a lower version counter", async () => {
  const app = await showingLeagueA();
  app.selectLeague(LEAGUE_B);
  assertShowsNoBoard(app);
  assert.match(app.el("connection").textContent, new RegExp(LEAGUE_B.id));

  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});

test("switching back and forth keeps each league's counters apart", async () => {
  const app = await showingLeagueA();
  app.selectLeague(LEAGUE_B);
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  app.selectLeague(LEAGUE_A);
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_A, { syncVersion: OLD_LEAGUE_SYNC_VERSION + 1 }));
  assertShows(app, LEAGUE_A, OLD_LEAGUE_SYNC_VERSION + 1);
});

test("an older sync version of the same league is still discarded", async () => {
  const app = await showingLeagueA();
  app.fireTimer();
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_A, { syncVersion: OLD_LEAGUE_SYNC_VERSION - 1 }));
  assertShows(app, LEAGUE_A, OLD_LEAGUE_SYNC_VERSION);
});

test("a failed new league request never shows or mutates the old board", async () => {
  const app = await showingLeagueA();
  app.selectLeague(LEAGUE_B);
  await app.lastBoardRequest().fail("league B unavailable");
  assertShowsNoBoard(app);
  assert.match(app.el("connection").textContent, /Disconnected · league B unavailable/);

  app.pressKey("s");
  app.click("manual");
  app.click("undo");
  assert.equal(app.mutationRequests().length, 0);
  assert.match(app.el("alerts").text(), /Wait for the selected league's board/);

  app.fireTimer();
  const retry = app.lastBoardRequest();
  assert.equal(retry.input.league, LEAGUE_B.id);
  await retry.respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});

test("versioned mutations are refused while the new league loads, then use its board", async () => {
  const app = await showingLeagueA();
  app.selectLeague(LEAGUE_B);
  app.prompts.push("1", "1", `${LEAGUE_B.leagueKey}.t.1`, LEAGUE_A.playerKey);
  app.pressKey("s");
  app.click("manual");
  assert.equal(app.mutationRequests().length, 0);
  assert.equal(app.prompts.length, 4, "a refused pick must not prompt for its slot");

  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, {
    syncVersion: NEW_LEAGUE_SYNC_VERSION, stateVersion: NEW_LEAGUE_STATE_VERSION,
  }));
  app.pressKey("s");
  const shortlist = app.lastMutationRequest();
  assert.match(shortlist.query, /setMauriceDraftShortlist/);
  assert.deepEqual(
    { league: shortlist.input.league, playerKey: shortlist.input.playerKey, version: shortlist.input.expectedStateVersion },
    { league: LEAGUE_B.id, playerKey: LEAGUE_B.playerKey, version: NEW_LEAGUE_STATE_VERSION },
  );
});

test("shortlisting from an empty player list sends nothing and claims no loading", async () => {
  const app = await loadApp();
  await app.lastBoardRequest().respondBoard({ ...boardOf(LEAGUE_A, { syncVersion: OLD_LEAGUE_SYNC_VERSION }), available: [] });
  app.pressKey("s");
  assert.equal(app.mutationRequests().length, 0);
  assert.doesNotMatch(app.el("alerts").text(), /Wait for the selected league's board/);
});

// SESSION_ACTIONS maps each session control to the mutation it sends; refresh
// has its own handler, so every control is checked separately.
const SESSION_ACTIONS = [
  ["watch-start", /startMauriceDraftWatch/],
  ["watch-stop", /stopMauriceDraftWatch/],
  ["refresh", /refreshMauriceDraftBoard/],
];

for (const [control, mutation] of SESSION_ACTIONS) {
  test(`${control} while switching targets the selected league`, async () => {
    const app = await showingLeagueA();
    app.selectLeague(LEAGUE_B);
    app.click(control);
    const request = app.lastMutationRequest();
    assert.ok(request, `${control} sent no mutation`);
    assert.match(request.query, mutation);
    assert.equal(request.input.league, LEAGUE_B.id);
  });
}

test("switching closes the old league's player details", async () => {
  const app = await showingLeagueA();
  app.pressKey("Enter");
  assert.ok(app.el("details").open);
  assert.match(app.el("details-body").text(), new RegExp(LEAGUE_A.player));

  app.selectLeague(LEAGUE_B);
  assert.equal(app.el("details").open, false);
  assert.doesNotMatch(app.el("details-body").text(), new RegExp(LEAGUE_A.player));
});

test("a mutation in flight during the switch reloads only the new league", async () => {
  const app = await showingLeagueA();
  app.pressKey("s");
  const shortlist = app.lastMutationRequest();
  assert.deepEqual(
    { league: shortlist.input.league, playerKey: shortlist.input.playerKey, version: shortlist.input.expectedStateVersion },
    { league: LEAGUE_A.id, playerKey: LEAGUE_A.playerKey, version: OLD_LEAGUE_STATE_VERSION },
  );
  app.selectLeague(LEAGUE_B);
  const requestB = app.lastBoardRequest();

  await shortlist.respond({ setMauriceDraftShortlist: { syncVersion: OLD_LEAGUE_SYNC_VERSION + 1 } });
  const reload = app.lastBoardRequest();
  assert.notEqual(reload, requestB);
  assert.equal(reload.input.league, LEAGUE_B.id);

  await reload.respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  await requestB.respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});

test("a mutation failing after the switch names the league it was for", async () => {
  const app = await showingLeagueA();
  app.pressKey("s");
  const shortlist = app.lastMutationRequest();
  app.selectLeague(LEAGUE_B);

  await shortlist.fail("stale state version");
  assert.match(app.el("alerts").text(), new RegExp(`League ${LEAGUE_A.id}: stale state version`));
  await app.lastBoardRequest().respondBoard(boardOf(LEAGUE_B, { syncVersion: NEW_LEAGUE_SYNC_VERSION }));
  assertShows(app, LEAGUE_B, NEW_LEAGUE_SYNC_VERSION);
});
