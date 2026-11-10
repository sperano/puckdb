package maurice

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/llm"
)

// modelMenuIDWidth is the column width of the model IDs in the /model menu.
const modelMenuIDWidth = 36

// untitledConversation is shown for a conversation without a title yet.
const untitledConversation = "(untitled)"

// repl is the state of one REPL run: the selected model, its service and the
// current conversation. Every method writes to out (errors to errOut) and
// never reads input, so tests drive it line by line.
type repl struct {
	models          []ModelEntry
	providerConfigs map[llm.Provider]llm.ProviderConfig
	listLimit       int
	out             io.Writer
	errOut          io.Writer
	markdown        *glamour.TermRenderer
	spin            *spinner
	// newService builds the service of a model, at startup and on /model.
	newService func(ModelEntry) Service

	entry ModelEntry
	svc   Service
	// conversationID is nil until the first answer or /load.
	conversationID *string
}

// handle runs one trimmed input line, a command or else a chat prompt, and
// reports whether the REPL must exit.
func (r *repl) handle(ctx context.Context, input string) (quit bool) {
	switch {
	case input == "":
	case input == "/quit" || input == "/exit":
		return true
	case input == "/new":
		r.conversationID = nil
		fmt.Fprintln(r.out, styleHint.Render("  Started new conversation."))
	case input == "/history":
		r.history(ctx)
	case isCommand(input, "/model"):
		r.selectModel(commandArg(input, "/model"))
	case isCommand(input, "/load"):
		r.load(ctx, commandArg(input, "/load"))
	default:
		r.chat(ctx, input)
	}
	return false
}

// isCommand reports whether input is the command name, alone or followed by
// whitespace and an argument (any whitespace, as commandArg trims).
func isCommand(input, name string) bool {
	rest, ok := strings.CutPrefix(input, name)
	if !ok {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || unicode.IsSpace(next)
}

// commandArg returns the trimmed argument after the command name.
func commandArg(input, name string) string {
	return strings.TrimSpace(strings.TrimPrefix(input, name))
}

func (r *repl) printBanner() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, styleHeader.Render("  Maurice — Hockey AI Chat"))
	fmt.Fprintln(r.out, styleHint.Render("  /quit  /new  /history  /load <id>  /model"))
	fmt.Fprintln(r.out)
}

func (r *repl) printError(err error) {
	fmt.Fprintln(r.errOut, styleError.Render("  Error: "+err.Error()))
}

// history lists the local user's recent conversations (/history).
func (r *repl) history(ctx context.Context) {
	convs, err := r.svc.ListConversations(ctx, LocalUserID, r.listLimit)
	if err != nil {
		r.printError(err)
		return
	}
	if len(convs) == 0 {
		fmt.Fprintln(r.out, styleHint.Render("  No conversations yet."))
		return
	}
	fmt.Fprintln(r.out)
	for _, c := range convs {
		fmt.Fprintf(r.out, "  %s  %s  %s\n",
			styleConvID.Render(c.ID),
			styleConvDate.Render(c.UpdatedAt.Format("2006-01-02 15:04")),
			styleConvTitle.Render(conversationTitle(c)),
		)
	}
	fmt.Fprintln(r.out)
}

// load resumes the conversation id (/load <id>).
func (r *repl) load(ctx context.Context, id string) {
	if id == "" {
		fmt.Fprintln(r.out, styleHint.Render("  Usage: /load <conversation-id>"))
		return
	}
	conv, _, err := r.svc.GetConversation(ctx, LocalUserID, id)
	if err != nil {
		r.printError(err)
		return
	}
	r.conversationID = &conv.ID
	fmt.Fprintln(r.out, styleHint.Render("  Resumed: "+conversationTitle(conv)))
}

func conversationTitle(c *Conversation) string {
	if c.Title == nil {
		return untitledConversation
	}
	return *c.Title
}

// selectModel shows the model menu (/model) or switches to the model at the
// 1-based menu position arg (/model <number>).
func (r *repl) selectModel(arg string) {
	if arg == "" {
		r.printModelMenu()
		return
	}
	num, err := strconv.Atoi(arg)
	if err != nil || num < 1 || num > len(r.models) {
		fmt.Fprintln(r.out, styleError.Render("  Invalid selection: "+arg))
		r.printModelMenu()
		return
	}
	r.switchModel(r.models[num-1])
}

// switchModel replaces the service with one for entry; the current
// conversation continues on the new model.
func (r *repl) switchModel(entry ModelEntry) {
	r.closeService()
	r.entry = entry
	r.svc = r.newService(entry)
	fmt.Fprintf(r.out, "  Switched to %s %s\n",
		styleMenuCurrent.Render(entry.ID),
		styleHint.Render("("+entry.Provider.String()+")"),
	)
	if entry.Provider != llm.ProviderOllama && r.providerConfigs[entry.Provider].APIKey == "" {
		fmt.Fprintln(r.out, styleWarning.Render("  Warning: no API key configured for "+entry.Provider.String()))
	}
}

func (r *repl) closeService() {
	if err := r.svc.Close(); err != nil {
		log.Warn().Err(err).Msg("maurice service close")
	}
}

// printModelMenu prints the model selection menu, the current model
// highlighted.
func (r *repl) printModelMenu() {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, styleHint.Render("  Current: "+r.entry.ID))
	fmt.Fprintln(r.out)
	for i, m := range r.models {
		num := styleMenuNumber.Render(fmt.Sprintf("%d)", i+1))
		name := m.ID
		if m.ID == r.entry.ID {
			name = styleMenuCurrent.Render(m.ID)
		}
		fmt.Fprintf(r.out, "  %s %-*s %s\n", num, modelMenuIDWidth, name, styleHint.Render(m.Description))
	}
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, styleHint.Render("  /model <number>"))
	fmt.Fprintln(r.out)
}

// chat sends input to the current conversation (a new one when none is
// selected) and prints the answer as rendered Markdown.
func (r *repl) chat(ctx context.Context, input string) {
	r.spin.Start("thinking...")
	resp, err := r.svc.Chat(ctx, ChatRequest{UserID: LocalUserID, ConversationID: r.conversationID, Message: input})
	r.spin.Stop()
	if err != nil {
		r.printError(err)
		return
	}

	r.conversationID = &resp.ConversationID
	for _, warning := range resp.Warnings {
		fmt.Fprintln(r.out, styleWarning.Render("  Warning: "+warning))
	}
	if len(resp.ToolsUsed) > 0 {
		fmt.Fprintln(r.out, styleToolTag.Render("  tools: "+strings.Join(resp.ToolsUsed, ", ")))
	}
	rendered, err := r.markdown.Render(resp.Content)
	if err != nil {
		fmt.Fprintln(r.out, resp.Content)
		return
	}
	fmt.Fprint(r.out, rendered)
}
