"use strict";
// Test harness for app.js: runs the real script in a Node VM against a small
// fake DOM and a fetch whose responses each test resolves by hand, so
// overlapping requests can complete in any order. Only rendering and network
// are faked; every handler under test is the shipped code.

const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { webcrypto } = require("node:crypto");

const APP_SOURCE = fs.readFileSync(path.join(__dirname, "app.js"), "utf8");
const BOARD_FIELD = "mauriceDraftBoard(";

const LEAGUE_A = { id: "5621", leagueKey: "465.l.5621", name: "Alpha League", player: "Alpha Skater", playerKey: "465.p.1001" };
const LEAGUE_B = { id: "29249", leagueKey: "465.l.29249", name: "Bravo League", player: "Bravo Skater", playerKey: "465.p.2002" };

class FakeElement {
  constructor(tagName, id = "") {
    this.tagName = tagName;
    this.id = id;
    this.children = [];
    this.listeners = new Map();
    this.attributes = new Map();
    this.dataset = {};
    this.className = "";
    this.textContent = "";
    this.value = "";
    this.title = "";
    this.hidden = false;
    this.disabled = false;
    this.open = false;
    this.tabIndex = 0;
    this.classList = { toggle() {}, add() {}, remove() {} };
  }

  get childElementCount() { return this.children.length; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this.attributes.set(name, String(value)); }
  querySelector() { return null; }
  focus() {}
  showModal() { this.open = true; }
  close() { this.open = false; }

  addEventListener(type, listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(listener);
  }

  dispatch(type, fields = {}) {
    const event = { target: this, preventDefault() {}, stopPropagation() {}, ...fields };
    for (const listener of this.listeners.get(type) ?? []) listener(event);
  }

  // text is the element's own text followed by its descendants', which is
  // what a user would read in the rendered panel.
  text() { return [this.textContent, ...this.children.map((child) => child.text())].join(" ").trim(); }
}

class FakeDocument {
  constructor() { this.elements = new Map(); }

  getElementById(id) {
    if (!this.elements.has(id)) this.elements.set(id, new FakeElement("div", id));
    return this.elements.get(id);
  }

  createElement(tagName) { return new FakeElement(tagName); }
  querySelectorAll() { return []; }
}

// PendingRequest is one fetch the app made; it stays unresolved until the
// test answers it.
class PendingRequest {
  constructor(body) {
    this.query = body.query;
    this.input = body.variables.input;
    this.promise = new Promise((resolve) => { this.resolve = resolve; });
  }

  get isBoard() { return this.query.includes(BOARD_FIELD); }

  respond(data) {
    this.resolve({ ok: true, json: async () => ({ data }) });
    return settle();
  }

  respondBoard(board) { return this.respond({ mauriceDraftBoard: board }); }

  fail(message) {
    this.resolve({ ok: true, json: async () => ({ errors: [{ message }] }) });
    return settle();
  }
}

// settle lets every promise continuation queued by a response run.
function settle() { return new Promise((resolve) => setImmediate(resolve)); }

function loadApp(initialLeague) {
  const document = new FakeDocument();
  const requests = [];
  const prompts = [];
  let scheduledPoll = null;
  const window = {
    setTimeout(callback) { scheduledPoll = callback; return 1; },
    clearTimeout() { scheduledPoll = null; },
    addEventListener() {},
    prompt() { return prompts.length ? prompts.shift() : null; },
  };
  const fetch = (url, options) => {
    const request = new PendingRequest(JSON.parse(options.body));
    requests.push(request);
    return request.promise;
  };
  document.getElementById("league").value = initialLeague.id;
  const context = vm.createContext({ document, window, fetch, crypto: webcrypto });
  vm.runInContext(APP_SOURCE, context, { filename: "app.js" });

  const el = (id) => document.getElementById(id);
  return {
    el,
    prompts,
    requests,
    state: () => vm.runInContext("state", context),
    boardRequests: () => requests.filter((request) => request.isBoard),
    mutationRequests: () => requests.filter((request) => !request.isBoard),
    lastBoardRequest() { return this.boardRequests().at(-1); },
    lastMutationRequest() { return this.mutationRequests().at(-1); },
    selectLeague(league) { el("league").value = league.id; el("league").dispatch("change"); },
    pressKey(key) { el("available").dispatch("keydown", { key }); },
    click(id) { el(id).dispatch("click"); },
    poll() {
      const callback = scheduledPoll;
      scheduledPoll = null;
      callback();
    },
  };
}

function boardOf(league, { syncVersion, stateVersion = syncVersion }) {
  return {
    league: { leagueKey: league.leagueKey, name: league.name, format: "live", scoringType: "head" },
    scenario: "BASE",
    snapshot: { asOf: "2026-10-01T12:00:00Z" },
    sync: {
      connection: "FRESH", draftStatus: "predraft", recommendationsSafe: true, complete: true,
      stateVersion, syncVersion, lastPollAt: null, lastSuccessAt: null, lastAuthoritativeAt: null, lastError: null,
      watch: { state: "STOPPED", startedAt: null, stoppedAt: null, lastError: null },
    },
    turn: {
      currentRound: null, currentPick: null, picksUntilNextTurn: null, orderKnown: false,
      draftType: "live", pickTimeSeconds: null, timingLabel: "Order unavailable",
    },
    roster: { teamId: 1, teamName: `${league.name} team`, feasible: true, warnings: [], assignments: [], openSlots: [] },
    history: [],
    available: [playerOf(league)],
    shortlist: [],
    recommendations: {
      bestValuePlayerKey: null, bestRosterFitPlayerKey: null, bestValue: null, bestRosterFit: null,
      issues: [], generatedAt: null, rankingAsOf: null,
    },
    warnings: [],
    totalAvailable: 1,
    offset: 0,
    limit: 100,
  };
}

function playerOf(league) {
  return {
    playerKey: league.playerKey, yahooPlayerId: league.playerKey.split(".").at(-1), name: league.player,
    team: "MTL", eligiblePositions: ["C"], status: null, injuryNote: null,
    baselineRank: 1, scenarioRank: 1, valueRank: 1, rosterFitRank: null,
    baselineValue: 1, scenarioValue: 1, newsDelta: 0, shortlisted: false, assignedSlot: null,
    recommendationReasons: [], news: [],
  };
}

module.exports = { LEAGUE_A, LEAGUE_B, loadApp, boardOf };
