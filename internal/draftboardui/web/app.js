"use strict";

const GRAPHQL_URL = "/graphql/query";
const BOARD_SEASON = 2026;
const POLL_INTERVAL_MS = 2000;
const SEARCH_DELAY_MS = 250;
const BOARD_PAGE_SIZE = 100;
const POSITION_OPTIONS = ["C", "LW", "RW", "D", "G"];
const NOTICE_DURATION_MS = 15000;
const WATCH_RUNNING = "RUNNING";
const BOARD_NOT_READY = "Wait for the selected league's board to load before changing it.";
const BOARD_TEXT_IDS = ["board-league", "current-pick", "next-turn", "draft-format", "last-sync", "versions", "scenario-age"];
const BOARD_LIST_IDS = ["available", "recommendations", "roster", "shortlist", "history"];

const state = {
  // selection is the league/season the user picked. Every change starts a new
  // generation; board responses and mutation results carry the selection they
  // were made for, so nothing from an earlier one reaches the current board.
  selection: { league: "", season: BOARD_SEASON, generation: 0 },
  board: null,
  boardSelection: null,
  selectedIndex: -1,
  positions: new Set(),
  search: "",
  scenario: "BASE",
  pending: false,
  timer: null,
  searchTimer: null,
  requestSequence: 0,
  appliedRequestSequence: 0,
  notices: [],
};

const byId = (id) => document.getElementById(id);
const node = (tag, className, text) => {
  const result = document.createElement(tag);
  if (className) result.className = className;
  if (text !== undefined) result.textContent = text;
  return result;
};

const BOARD_QUERY = `query DraftBoard($input: MauriceDraftBoardInput!) {
  mauriceDraftBoard(input: $input) {
    league { leagueKey name format scoringType }
    scenario snapshot { asOf }
    sync { connection draftStatus recommendationsSafe complete stateVersion syncVersion
      lastPollAt lastSuccessAt lastAuthoritativeAt lastError watch { state startedAt stoppedAt lastError } }
    turn { currentRound currentPick picksUntilNextTurn orderKnown draftType pickTimeSeconds timingLabel }
    roster { teamId teamName feasible warnings assignments { slot index playerId playerKey playerName } openSlots { slot count } }
    history { round pick teamId teamName playerId playerKey playerName source conflict undone cost }
    available { playerKey yahooPlayerId name team eligiblePositions status injuryNote baselineRank scenarioRank
      valueRank rosterFitRank baselineValue scenarioValue newsDelta shortlisted assignedSlot recommendationReasons
      news { detail reportedAt status scenarios } }
    shortlist { playerKey name team eligiblePositions baselineRank scenarioRank shortlisted recommendationReasons news { detail reportedAt status scenarios } }
    recommendations { bestValuePlayerKey bestRosterFitPlayerKey
      bestValue { playerKey name recommendationReasons }
      bestRosterFit { playerKey name recommendationReasons }
      issues generatedAt rankingAsOf }
    warnings totalAvailable offset limit
  }
}`;

async function graphql(query, variables) {
  const response = await fetch(GRAPHQL_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, variables }),
  });
  if (!response.ok) throw new Error(`GraphQL HTTP ${response.status}`);
  const payload = await response.json();
  if (payload.errors?.length) throw new Error(payload.errors.map((item) => item.message).join("; "));
  return payload.data;
}

function boardInput(selection) {
  return {
    league: selection.league,
    season: selection.season,
    positions: [...state.positions],
    search: state.search,
    scenario: state.scenario,
    limit: BOARD_PAGE_SIZE,
  };
}

async function loadBoard() {
  const request = { selection: state.selection, sequence: ++state.requestSequence };
  try {
    const data = await graphql(BOARD_QUERY, { input: boardInput(request.selection) });
    applyBoard(request, data.mauriceDraftBoard);
  } catch (error) {
    if (isCurrent(request.selection)) setConnection("error", `Disconnected · ${error.message}`);
  } finally {
    schedulePoll();
  }
}

// applyBoard shows a response only if it answers the current selection.
// Session versions count one league's session, so they are compared only with
// a board of the same league key; another league's counters mean nothing here.
function applyBoard(request, next) {
  if (!isCurrent(request.selection) || request.sequence < state.appliedRequestSequence) return;
  const previous = state.board?.league.leagueKey === next.league.leagueKey ? state.board : null;
  if (previous && Number(next.sync.syncVersion) < Number(previous.sync.syncVersion)) return;
  if (previous && Number(next.sync.stateVersion) > Number(previous.sync.stateVersion) + 1) {
    showTransient("Session advanced while disconnected; the complete board was reloaded.");
  }
  state.appliedRequestSequence = request.sequence;
  state.board = next;
  state.boardSelection = request.selection;
  render();
}

function isCurrent(selection) { return selection?.generation === state.selection.generation; }

// selectBoard starts a new generation for a league/season pair and clears the
// previous board at once, so it is neither shown nor usable for mutations
// while the new one loads or fails to load.
function selectBoard(league, season) {
  state.selection = { league, season, generation: state.selection.generation + 1 };
  clearBoard();
  void loadBoard();
}

function clearBoard() {
  state.board = null;
  state.boardSelection = null;
  state.selectedIndex = -1;
  state.notices = [];
  BOARD_TEXT_IDS.forEach((id) => { byId(id).textContent = "—"; });
  BOARD_LIST_IDS.forEach((id) => byId(id).replaceChildren());
  byId("empty").hidden = true;
  renderAlerts(null);
  setConnection("never_synced", `Loading league ${state.selection.league}…`);
}

// displayedBoard is the visible board while it belongs to the current
// selection, else null.
function displayedBoard() { return isCurrent(state.boardSelection) ? state.board : null; }

function schedulePoll() {
  window.clearTimeout(state.timer);
  state.timer = window.setTimeout(loadBoard, POLL_INTERVAL_MS);
}

function setConnection(kind, text) {
  const target = byId("connection");
  target.className = `status ${kind.toLowerCase()}`;
  target.textContent = text;
}

function render() {
  if (!state.board) return;
  const board = state.board;
  const sync = board.sync;
  setConnection(sync.connection, `${sync.connection.replace("_", " ")} · watch ${sync.watch.state.toLowerCase()}`);
  byId("board-league").textContent = `${board.league.name} · ${board.league.leagueKey}`;
  byId("refresh").title = sync.watch.state === WATCH_RUNNING
    ? "Asks this server's running watch to poll Yahoo now."
    : "Polls Yahoo once now. Unavailable while a CLI or another server watches this league.";
  const estimate = board.turn.orderKnown ? "" : " · estimate";
  byId("current-pick").textContent = board.turn.currentPick ? `R${board.turn.currentRound} · #${board.turn.currentPick}${estimate}` : "Order unavailable";
  byId("next-turn").textContent = board.turn.picksUntilNextTurn === null ? board.turn.timingLabel : `${board.turn.picksUntilNextTurn} picks${estimate}`;
  byId("draft-format").textContent = `${board.turn.draftType || board.league.format || "Unknown"}${board.turn.pickTimeSeconds ? ` · ${board.turn.pickTimeSeconds}s limit` : ""}`;
  byId("last-sync").textContent = formatTime(sync.lastSuccessAt);
  byId("versions").textContent = `${sync.stateVersion} / ${sync.syncVersion}`;
  byId("scenario-age").textContent = `${board.scenario} snapshot · ${formatTime(board.snapshot.asOf)}`;
  renderAlerts(board);
  renderPlayers(board.available);
  renderRecommendations(board);
  renderRoster(board.roster);
  renderShortlist(board.shortlist);
  renderHistory(board.history);
}

function renderAlerts(board) {
  const target = byId("alerts");
  target.replaceChildren();
  const warnings = board ? [...board.warnings] : [];
  if (board?.sync.lastError) warnings.unshift(board.sync.lastError);
  if (board && !board.sync.recommendationsSafe) warnings.unshift("Recommendations are paused until the board is complete and conflicts are resolved.");
  state.notices = activeNotices();
  warnings.unshift(...state.notices.map((notice) => notice.message));
  for (const warning of [...new Set(warnings)]) target.append(node("div", "alert", warning));
}

function activeNotices() {
  const now = Date.now();
  return state.notices.filter((notice) => notice.expiresAt > now);
}

function renderPlayers(players) {
  const target = byId("available");
  target.replaceChildren();
  players.forEach((player, index) => {
    const row = node("tr", index === state.selectedIndex ? "selected" : "");
    row.tabIndex = index === state.selectedIndex ? 0 : -1;
    row.dataset.index = String(index);
    row.addEventListener("click", () => selectPlayer(index));
    row.addEventListener("dblclick", () => showDetails(player));
    const playerCell = node("td");
    playerCell.append(node("strong", "", player.name), node("div", "muted", `${player.team}${player.status ? ` · ${player.status}` : ""}`));
    row.append(playerCell);
    const positions = node("td");
    player.eligiblePositions.forEach((position) => positions.append(node("span", "tag", position)));
    row.append(positions, node("td", "", `#${player.baselineRank}`), node("td", "", rankLabel(player)));
    row.append(node("td", "", player.scenarioValue.toFixed(2)));
    row.append(node("td", deltaClass(player.newsDelta), signed(player.newsDelta)));
    const starCell = node("td");
    const star = node("button", `star${player.shortlisted ? " active" : ""}`, player.shortlisted ? "★" : "☆");
    star.type = "button";
    star.setAttribute("aria-label", `${player.shortlisted ? "Remove" : "Add"} ${player.name} ${player.shortlisted ? "from" : "to"} shortlist`);
    star.addEventListener("click", (event) => { event.stopPropagation(); void setShortlist(player); });
    starCell.append(star); row.append(starCell); target.append(row);
  });
  byId("empty").hidden = players.length !== 0;
  if (players.length && (state.selectedIndex < 0 || state.selectedIndex >= players.length)) state.selectedIndex = 0;
}

function renderRecommendations(board) {
  const target = byId("recommendations"); target.replaceChildren();
  const keys = [
    ["Best value", board.recommendations.bestValue],
    ["Best roster fit", board.recommendations.bestRosterFit],
  ];
  for (const [label, player] of keys) {
    if (!player) continue;
    const article = node("article"); article.append(node("div", "eyebrow", label), node("h3", "", player.name));
    article.append(node("p", "muted", player.recommendationReasons.slice(0, 2).join(" · ")));
    target.append(article);
  }
  if (!target.childElementCount) target.append(node("p", "muted", board.recommendations.issues.join(" · ") || "No safe recommendation yet."));
}

function renderRoster(roster) {
  const target = byId("roster"); target.replaceChildren(node("p", roster.feasible ? "muted" : "conflict", `${roster.teamName} · ${roster.feasible ? "feasible" : "needs attention"}`));
  const list = node("ol");
  roster.assignments.forEach((item) => list.append(node("li", "", `${item.slot}${item.index + 1} · ${item.playerName}`)));
  roster.openSlots.forEach((item) => list.append(node("li", "muted", `${item.slot} · ${item.count} open`)));
  target.append(list);
}

function renderShortlist(players) {
  const target = byId("shortlist"); target.replaceChildren();
  players.forEach((player) => target.append(node("li", "", `${player.name} · #${player.scenarioRank}`)));
  if (!players.length) target.append(node("li", "muted", "No saved players"));
}

function renderHistory(picks) {
  const target = byId("history"); target.replaceChildren();
  picks.slice().reverse().forEach((pick) => {
    const item = node("li");
    const prefix = pick.undone ? "Undo pending: " : "";
    item.append(node("span", "", `#${pick.pick} ${prefix}${pick.playerName} · ${pick.teamName} `));
    item.append(node("span", `source ${pick.source.toLowerCase()}`, pick.source.toLowerCase()));
    if (pick.conflict) {
      item.append(node("span", "conflict", " · conflict "));
      const keep = node("button", "secondary", "Keep local");
      const accept = node("button", "secondary", "Accept Yahoo");
      keep.addEventListener("click", () => resolveConflict(pick, "KEEP_MANUAL"));
      accept.addEventListener("click", () => resolveConflict(pick, "ACCEPT_UPSTREAM"));
      item.append(keep, accept);
    }
    target.append(item);
  });
}

function selectPlayer(index) {
  if (!displayedBoard()) return;
  state.selectedIndex = Math.max(0, Math.min(index, state.board.available.length - 1));
  renderPlayers(state.board.available);
  byId("available").querySelector(`[data-index="${state.selectedIndex}"]`)?.focus();
}

function showDetails(player) {
  const body = byId("details-body"); body.replaceChildren(node("p", "eyebrow", player.eligiblePositions.join(" · ")), node("h2", "", player.name));
  body.append(node("p", "", `Baseline #${player.baselineRank}; ${state.board.scenario.toLowerCase()} #${player.scenarioRank}; roster-fit ${rankLabel(player)}.`));
  player.recommendationReasons.forEach((reason) => body.append(node("p", "", reason)));
  player.news.forEach((news) => body.append(node("p", "muted", `${formatTime(news.reportedAt)} · ${news.status} · ${news.scenarios.map((item) => item.toLowerCase()).join("/")} · ${news.detail}`)));
  byId("details").showModal();
}

async function setShortlist(player) {
  const query = `mutation SetShortlist($input: MauriceDraftShortlistInput!) { setMauriceDraftShortlist(input: $input) { syncVersion } }`;
  await boardMutation(query, (board) => board.available.includes(player) && { playerKey: player.playerKey, selected: !player.shortlisted });
}

// Session actions carry no board data, so they target the selected league
// even while its board is loading (starting a watch is how a failing board
// recovers).
async function sessionAction(name) {
  const query = `mutation SessionAction($input: MauriceDraftSessionRefInput!) { ${name}(input: $input) { state } }`;
  await mutate(query, sessionRef());
}

async function refreshBoard() {
  const query = `mutation Refresh($input: MauriceDraftSessionRefInput!) { refreshMauriceDraftBoard(input: $input) { syncVersion } }`;
  await mutate(query, sessionRef());
}

async function manualPick(action) {
  if (!boardReady()) return;
  const values = promptSlot(true); if (!values) return;
  const query = `mutation Manual($input: MauriceDraftManualPickInput!) { applyMauriceDraftPick(input: $input) { stateVersion syncVersion } }`;
  await boardMutation(query, () => ({ action, ...values, clientMutationId: crypto.randomUUID() }));
}

async function undoPick() {
  if (!boardReady()) return;
  const values = promptSlot(false); if (!values) return;
  const query = `mutation Undo($input: MauriceDraftSlotInput!) { undoMauriceDraftPick(input: $input) { stateVersion syncVersion } }`;
  await boardMutation(query, () => ({ round: values.round, pick: values.pick, clientMutationId: crypto.randomUUID() }));
}

async function resolveConflict(pick, choice) {
  const query = `mutation Resolve($input: MauriceDraftResolveInput!) { resolveMauriceDraftConflict(input: $input) { stateVersion syncVersion } }`;
  await boardMutation(query, (board) => board.history.includes(pick) && { round: pick.round, pick: pick.pick, choice, clientMutationId: crypto.randomUUID() });
}

// boardMutation sends a versioned mutation built from the displayed board:
// its league, season and state version, plus the fields fieldsOf derives from
// it. fieldsOf returns a falsy value when the clicked row is not on that
// board. Without a current board the mutation is refused, so a write never
// pairs the selected league with players or versions of the previous one.
async function boardMutation(query, fieldsOf) {
  const board = displayedBoard();
  const fields = board && fieldsOf(board);
  if (!fields) { showTransient(BOARD_NOT_READY); return; }
  const { league, season } = state.boardSelection;
  await mutate(query, { league, season, ...fields, expectedStateVersion: Number(board.sync.stateVersion) });
}

function boardReady() {
  if (displayedBoard()) return true;
  showTransient(BOARD_NOT_READY);
  return false;
}

function promptSlot(includePlayer) {
  const round = positivePrompt("Round"); if (!round) return null;
  const pick = positivePrompt("Overall pick number"); if (!pick) return null;
  if (!includePlayer) return { round, pick };
  const teamKey = window.prompt("Full Yahoo team key"); if (!teamKey) return null;
  const playerKey = window.prompt("Full Yahoo player key"); if (!playerKey) return null;
  return { round, pick, teamKey, playerKey };
}

function positivePrompt(label) {
  const value = Number(window.prompt(label));
  return Number.isInteger(value) && value > 0 ? value : null;
}

async function mutate(query, input) {
  if (state.pending) return;
  const selection = state.selection;
  state.pending = true; setControlsDisabled(true);
  try { await graphql(query, { input }); await loadBoard(); }
  catch (error) { showTransient(isCurrent(selection) ? error.message : `League ${input.league}: ${error.message}`); }
  finally { state.pending = false; setControlsDisabled(false); }
}

function setControlsDisabled(disabled) { document.querySelectorAll("button").forEach((button) => { button.disabled = disabled; }); }
function sessionRef() { return { league: state.selection.league, season: state.selection.season }; }
function formatTime(value) { return value ? new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "Never"; }
function signed(value) { return value === 0 ? "—" : `${value > 0 ? "+" : ""}${value.toFixed(2)}`; }
function deltaClass(value) { return `delta ${value > 0 ? "up" : value < 0 ? "down" : ""}`; }
function rankLabel(player) { return player.rosterFitRank ? `#${player.rosterFitRank}${player.assignedSlot ? ` · ${player.assignedSlot}` : ""}` : "—"; }
// Notices outlive the next board poll, which redraws the alerts every POLL_INTERVAL_MS.
function showTransient(message) {
  state.notices = [...activeNotices(), { message, expiresAt: Date.now() + NOTICE_DURATION_MS }];
  renderAlerts(state.board);
}

function bind() {
  POSITION_OPTIONS.forEach((position) => {
    const button = node("button", "position", position); button.type = "button"; button.setAttribute("aria-pressed", "false");
    button.addEventListener("click", () => { state.positions.has(position) ? state.positions.delete(position) : state.positions.add(position); button.classList.toggle("active"); button.setAttribute("aria-pressed", String(state.positions.has(position))); void loadBoard(); });
    byId("positions").append(button);
  });
  byId("league").addEventListener("change", (event) => selectBoard(event.target.value, BOARD_SEASON));
  byId("scenario").addEventListener("change", (event) => { state.scenario = event.target.value; void loadBoard(); });
  byId("search").addEventListener("input", (event) => { window.clearTimeout(state.searchTimer); state.searchTimer = window.setTimeout(() => { state.search = event.target.value; void loadBoard(); }, SEARCH_DELAY_MS); });
  byId("watch-start").addEventListener("click", () => sessionAction("startMauriceDraftWatch"));
  byId("watch-stop").addEventListener("click", () => sessionAction("stopMauriceDraftWatch"));
  byId("refresh").addEventListener("click", refreshBoard);
  byId("manual").addEventListener("click", () => manualPick("ADD"));
  byId("correct").addEventListener("click", () => manualPick("CORRECT"));
  byId("undo").addEventListener("click", undoPick);
  byId("details-close").addEventListener("click", () => byId("details").close());
  window.addEventListener("online", () => loadBoard());
  byId("available").addEventListener("keydown", onPlayerKey);
}

function onPlayerKey(event) {
  const player = displayedBoard()?.available[state.selectedIndex];
  if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); selectPlayer(state.selectedIndex + (event.key === "ArrowDown" ? 1 : -1)); }
  if (event.key.toLowerCase() === "s") { event.preventDefault(); void setShortlist(player); }
  if (event.key === "Enter" && player) showDetails(player);
}

bind();
selectBoard(byId("league").value, BOARD_SEASON);
