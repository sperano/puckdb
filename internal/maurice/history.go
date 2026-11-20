// Package maurice orchestrates the Maurice AI chat: Service runs the
// prompt/tool-call loop (internal/llm for completions, internal/mcp for
// tool discovery and calls) and persists conversations, turns and messages
// through the DB interface, implemented by a SQLite store (the REPL,
// single local user) and a PostgreSQL store (the API, one user per login
// provider identity and API instance via internal/appuser). LLM-call and
// tool-call usage is persisted only by the PostgreSQL store; SQLite keeps
// conversations, turns and messages alone. RunREPL (repl*.go) is the
// `maurice` CLI command's REPL loop, its streams injectable through
// REPLConfig.
package maurice

// capHistory keeps at most maxHistory of the newest msgs, cut at the start
// of a turn: leading messages are dropped up to the first "user" message, so
// a turn's tool_use/tool_result pairs never split and the replay never starts
// on an assistant or tool message (Anthropic requires the first non-system
// message to be the user's). The last message is the current prompt, so at
// least it is kept.
func capHistory(msgs []*Message, maxHistory int) []*Message {
	if len(msgs) > maxHistory {
		msgs = msgs[len(msgs)-maxHistory:]
	}
	for len(msgs) > 1 && msgs[0].Role != "user" {
		msgs = msgs[1:]
	}
	return msgs
}
