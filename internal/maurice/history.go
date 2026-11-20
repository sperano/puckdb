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
