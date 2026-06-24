package commands

// SlashCommand represents a user-typeable slash command.
type SlashCommand struct {
	Name        string
	Description string
	Arguments   []string
}

// SlashCommands is a registry of available slash commands.
var SlashCommands = []SlashCommand{
	{
		Name:        "goal",
		Description: "Manage session goal",
		Arguments:   []string{"clear"},
	},
	{
		Name:        "menu",
		Description: "Open the command menu",
	},
	{
		Name:        "stats",
		Description: "Open the usage statistics dialog",
	},
	{
		Name:        "learn",
		Description: "Teach the agent a new skill from a URL, directory, or text",
		Arguments:   []string{"<url, path, or description>"},
	},
	{
		Name:        "quit",
		Description: "Quit the application",
	},
}

// GetSlashCommands returns all registered slash commands.
func GetSlashCommands() []SlashCommand {
	return SlashCommands
}

// GetSlashCommandNames returns the names of all registered slash commands.
func GetSlashCommandNames() []string {
	names := make([]string, len(SlashCommands))
	for i, cmd := range SlashCommands {
		names[i] = cmd.Name
	}
	return names
}
