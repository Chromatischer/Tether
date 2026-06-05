package discord

import (
	"context"
	"strconv"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/bwmarrin/discordgo"

	"tether/internal/store"
)

// Slash (application) command support.
//
// The DM bot already accepts text-prefixed commands like "/task add foo".
// To make those discoverable and tab-completable in Discord's client, we also
// register matching application commands and translate each interaction back
// into the equivalent text command, then route it through handleCommand so the
// behavior stays identical to the typed form (single source of truth).

// argSpec describes one option of a (sub)command.
type argSpec struct {
	name     string
	desc     string
	integer  bool
	required bool
}

// subSpec describes a subcommand and its ordered args.
type subSpec struct {
	name string
	desc string
	args []argSpec
}

// slashSpec describes a top-level command. A command has either direct args or
// subcommands, never both (a Discord constraint we also rely on).
type slashSpec struct {
	name string
	desc string
	args []argSpec
	subs []subSpec
}

// slashSpecs is the single source of truth for both registration and the
// interaction→text translation. Arg order here must match what handleCommand
// expects positionally (fields[1] = subcommand, fields[2:] = args).
var slashSpecs = []slashSpec{
	{name: "status", desc: "Show the current session status"},
	{name: "clear", desc: "Start a fresh conversation"},
	{name: "resume", desc: "Resume a previous conversation", args: []argSpec{
		{name: "code", desc: "Resume code", required: true},
	}},
	{name: "confirm", desc: "Confirm a pending action", args: []argSpec{
		{name: "token", desc: "Confirmation token", required: true},
	}},
	{name: "help", desc: "Show command help", args: []argSpec{
		{name: "command", desc: "Command to describe"},
	}},
	{name: "tools", desc: "Inspect available tools", subs: []subSpec{
		{name: "list", desc: "List tools"},
		{name: "search", desc: "Search tools", args: []argSpec{{name: "query", desc: "Search query", required: true}}},
		{name: "describe", desc: "Describe a tool", args: []argSpec{{name: "name", desc: "Tool name", required: true}}},
	}},
	{name: "signal", desc: "Manage Signal linking", subs: []subSpec{
		{name: "link", desc: "Create a Signal link code"},
		{name: "status", desc: "Show Signal link status"},
		{name: "unlink", desc: "Unlink Signal"},
	}},
	{name: "discord", desc: "Manage Discord linking", subs: []subSpec{
		{name: "status", desc: "Show Discord link status"},
		{name: "unlink", desc: "Unlink Discord"},
	}},
	{name: "memory", desc: "Manage memory items", subs: []subSpec{
		{name: "list", desc: "List memory items", args: []argSpec{{name: "kind", desc: "Filter by kind"}}},
		{name: "add", desc: "Add a memory item", args: []argSpec{
			{name: "kind", desc: "Memory kind", required: true},
			{name: "content", desc: "Memory content", required: true},
		}},
		{name: "update", desc: "Update a memory item", args: []argSpec{
			{name: "id", desc: "Memory id", integer: true, required: true},
			{name: "content", desc: "New content", required: true},
		}},
		{name: "delete", desc: "Delete a memory item", args: []argSpec{
			{name: "id", desc: "Memory id", integer: true, required: true},
		}},
	}},
	{name: "task", desc: "Manage tasks", subs: []subSpec{
		{name: "list", desc: "List tasks"},
		{name: "add", desc: "Add a task", args: []argSpec{{name: "text", desc: "Task text", required: true}}},
		{name: "edit", desc: "Edit a task", args: []argSpec{
			{name: "id", desc: "Task id", integer: true, required: true},
			{name: "text", desc: "New text", required: true},
		}},
		{name: "done", desc: "Mark a task done", args: []argSpec{
			{name: "id", desc: "Task id", integer: true, required: true},
		}},
	}},
	{name: "secret", desc: "Manage secrets", subs: []subSpec{
		{name: "add", desc: "Store a secret", args: []argSpec{
			{name: "label", desc: "Secret label", required: true},
			{name: "secret", desc: "Secret value", required: true},
		}},
		{name: "list", desc: "List secret labels"},
		{name: "delete", desc: "Delete a secret", args: []argSpec{{name: "label", desc: "Secret label", required: true}}},
		{name: "clear", desc: "Clear all secrets"},
	}},
	{name: "subagent", desc: "Manage subagents", subs: []subSpec{
		{name: "spawn", desc: "Spawn a subagent", args: []argSpec{{name: "prompt", desc: "Prompt", required: true}}},
		{name: "status", desc: "Get subagent status", args: []argSpec{{name: "id", desc: "Subagent id", required: true}}},
	}},
	{name: "proactive", desc: "Trigger proactive behavior", subs: []subSpec{
		{name: "action", desc: "Trigger a proactive action", args: []argSpec{{name: "name", desc: "Action name", required: true}}},
		{name: "agent", desc: "Trigger a proactive agent", args: []argSpec{{name: "id", desc: "Agent id", required: true}}},
	}},
}

func specByName(name string) (slashSpec, bool) {
	for _, sp := range slashSpecs {
		if sp.name == name {
			return sp, true
		}
	}
	return slashSpec{}, false
}

// buildAppCommands converts the specs into discordgo application commands.
func buildAppCommands() []*discordgo.ApplicationCommand {
	// Make commands usable in DMs with the bot, group DMs, and guilds.
	contexts := []discordgo.InteractionContextType{
		discordgo.InteractionContextBotDM,
		discordgo.InteractionContextPrivateChannel,
		discordgo.InteractionContextGuild,
	}
	integrations := []discordgo.ApplicationIntegrationType{
		discordgo.ApplicationIntegrationGuildInstall,
	}

	argOption := func(a argSpec) *discordgo.ApplicationCommandOption {
		t := discordgo.ApplicationCommandOptionString
		if a.integer {
			t = discordgo.ApplicationCommandOptionInteger
		}
		return &discordgo.ApplicationCommandOption{
			Type:        t,
			Name:        a.name,
			Description: a.desc,
			Required:    a.required,
		}
	}

	cmds := make([]*discordgo.ApplicationCommand, 0, len(slashSpecs))
	for _, sp := range slashSpecs {
		cmd := &discordgo.ApplicationCommand{
			Name:             sp.name,
			Description:      sp.desc,
			Contexts:         &contexts,
			IntegrationTypes: &integrations,
		}
		if len(sp.subs) > 0 {
			for _, sub := range sp.subs {
				opt := &discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        sub.name,
					Description: sub.desc,
				}
				for _, a := range sub.args {
					opt.Options = append(opt.Options, argOption(a))
				}
				cmd.Options = append(cmd.Options, opt)
			}
		} else {
			for _, a := range sp.args {
				cmd.Options = append(cmd.Options, argOption(a))
			}
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// registerSlashCommands bulk-overwrites the bot's global application commands.
func (g *Gateway) registerSlashCommands(s *discordgo.Session) {
	if s.State == nil || s.State.User == nil {
		log.Warn("discord: cannot register slash commands, session user unknown")
		return
	}
	cmds := buildAppCommands()
	if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", cmds); err != nil {
		log.Warn("failed to register discord slash commands", "error", err)
		return
	}
	log.Info("registered discord slash commands", "count", len(cmds))
}

// optValue formats a provided interaction option as the text the typed command
// parser expects.
func optValue(opts []*discordgo.ApplicationCommandInteractionDataOption, a argSpec) (string, bool) {
	for _, o := range opts {
		if o.Name != a.name {
			continue
		}
		if a.integer {
			return strconv.FormatInt(o.IntValue(), 10), true
		}
		return o.StringValue(), true
	}
	return "", false
}

// reconstructText turns an interaction into the equivalent "/cmd sub args"
// string so it can be handled identically to a typed command.
func reconstructText(data discordgo.ApplicationCommandInteractionData) (string, bool) {
	sp, ok := specByName(data.Name)
	if !ok {
		return "", false
	}
	parts := []string{"/" + sp.name}
	if len(sp.subs) > 0 {
		if len(data.Options) == 0 {
			return strings.Join(parts, " "), true
		}
		subOpt := data.Options[0]
		parts = append(parts, subOpt.Name)
		for _, sub := range sp.subs {
			if sub.name != subOpt.Name {
				continue
			}
			for _, a := range sub.args {
				if v, ok := optValue(subOpt.Options, a); ok {
					parts = append(parts, v)
				}
			}
			break
		}
	} else {
		for _, a := range sp.args {
			if v, ok := optValue(data.Options, a); ok {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, " "), true
}

// onInteraction handles an application command interaction by translating it to
// the text command form and routing it through handleCommand.
func (g *Gateway) onInteraction(ctx context.Context, s *discordgo.Session, ic *discordgo.InteractionCreate) {
	if ic == nil || ic.Type != discordgo.InteractionApplicationCommand {
		return
	}

	du := ic.User
	if du == nil && ic.Member != nil {
		du = ic.Member.User
	}
	if du == nil {
		return
	}
	discordUID := strings.TrimSpace(du.ID)
	if discordUID == "" {
		return
	}

	data := ic.ApplicationCommandData()
	text, ok := reconstructText(data)
	if !ok {
		_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "unknown command"},
		})
		return
	}

	// Acknowledge immediately; handleCommand (e.g. /confirm) may exceed the 3s
	// inline-response window.
	if err := s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Warn("discord: failed to defer interaction", "error", err)
		return
	}

	uid, linked, err := store.FindUserIDByDiscordUserID(g.db, discordUID)
	if err != nil {
		g.editInteraction(s, ic, "Internal error.")
		return
	}
	if !linked {
		code, err := store.CreateDiscordLinkCode(g.db, discordUID, 10*time.Minute)
		if err != nil {
			g.editInteraction(s, ic, "Internal error.")
			return
		}
		g.editInteraction(s, ic, "Your account is not linked\n"+
			"Link code: "+code+"\n"+
			"In Tether SSH run: /discord link "+code+"\n"+
			"(Expires in ~10 minutes.)")
		return
	}

	conv, err := store.GetOrCreateActiveConversation(g.db, uid)
	if err != nil {
		g.editInteraction(s, ic, "Internal error.")
		return
	}

	handled, resp, nextConv := g.handleCommand(ctx, uid, conv, text)
	if nextConv != nil {
		conv = nextConv
	}
	if !handled {
		g.editInteraction(s, ic, "unknown command")
		return
	}
	if strings.TrimSpace(resp) == "" {
		resp = "(done)"
	}
	_ = store.AddMessage(g.db, conv.ID, "assistant", resp)
	g.editInteraction(s, ic, resp)
}

// editInteraction writes the (possibly chunked) response back to a deferred
// interaction: the first chunk edits the original response, the rest are sent
// as follow-up messages.
func (g *Gateway) editInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate, msg string) {
	chunks := splitDiscordMessage(msg, discordMessageLimit)
	if len(chunks) == 0 {
		chunks = []string{"(no output)"}
	}
	first := chunks[0]
	if _, err := s.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{Content: &first}); err != nil {
		log.Warn("discord: failed to edit interaction response", "error", err)
		return
	}
	for _, c := range chunks[1:] {
		cc := c
		if _, err := s.FollowupMessageCreate(ic.Interaction, true, &discordgo.WebhookParams{Content: cc}); err != nil {
			log.Warn("discord: failed to send interaction follow-up", "error", err)
			return
		}
	}
}
