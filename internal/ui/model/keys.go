package model

import (
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
)

// KeyBinding represents a single keybinding with its section, keys, and help text.
type KeyBinding struct {
	Section string
	Key     string
	Help    string
}

// Bindings returns all keybindings in the keymap as a sorted list.
func (k KeyMap) Bindings() []KeyBinding {
	var bindings []KeyBinding

	add := func(section string, b key.Binding, help string) {
		if len(b.Keys()) == 0 {
			return
		}
		bindings = append(bindings, KeyBinding{
			Section: section,
			Key:     strings.Join(b.Keys(), ","),
			Help:    help,
		})
	}

	// Global bindings
	add("Global", k.Quit, "quit")
	add("Global", k.Help, "more")
	add("Global", k.Commands, "commands")
	add("Global", k.Models, "models")
	add("Global", k.Suspend, "suspend")
	add("Global", k.Sessions, "sessions")
	add("Global", k.Tab, "change focus")
	add("Global", k.ToggleYolo, "toggle yolo")

	// Editor bindings
	add("Editor", k.Editor.AddFile, "add file")
	add("Editor", k.Editor.SendMessage, "send")
	add("Editor", k.Editor.OpenEditor, "open editor")
	add("Editor", k.Editor.Newline, "newline")
	add("Editor", k.Editor.AddImage, "add image")
	add("Editor", k.Editor.PasteImage, "paste image from clipboard")
	add("Editor", k.Editor.MentionFile, "mention file")
	add("Editor", k.Editor.Commands, "commands")
	add("Editor", k.Editor.AttachmentDeleteMode, "delete attachment at index i")
	add("Editor", k.Editor.Escape, "cancel delete mode")
	add("Editor", k.Editor.DeleteAllAttachments, "delete all attachments")
	add("Editor", k.Editor.PreviewAttachment, "preview attachment")
	add("Editor", k.Editor.HistoryPrev, "previous message")
	add("Editor", k.Editor.HistoryNext, "next message")
	add("Editor", k.Editor.ClearPrompt, "clear prompt")

	// Chat bindings
	add("Chat", k.Chat.NewSession, "new session")
	add("Chat", k.Chat.AddAttachment, "add attachment")
	add("Chat", k.Chat.Cancel, "cancel")
	add("Chat", k.Chat.Tab, "change focus")
	add("Chat", k.Chat.Details, "toggle details")
	add("Chat", k.Chat.TogglePills, "toggle tasks")
	add("Chat", k.Chat.PillLeft, "switch section")
	add("Chat", k.Chat.PillRight, "switch section")
	add("Chat", k.Chat.Down, "down")
	add("Chat", k.Chat.Up, "up")
	add("Chat", k.Chat.UpDown, "scroll")
	add("Chat", k.Chat.UpOneItem, "up one item")
	add("Chat", k.Chat.DownOneItem, "down one item")
	add("Chat", k.Chat.UpDownOneItem, "scroll one item")
	add("Chat", k.Chat.HalfPageDown, "half page down")
	add("Chat", k.Chat.PageDown, "page down")
	add("Chat", k.Chat.PageUp, "page up")
	add("Chat", k.Chat.HalfPageUp, "half page up")
	add("Chat", k.Chat.Home, "home")
	add("Chat", k.Chat.End, "end")
	add("Chat", k.Chat.Copy, "copy")
	add("Chat", k.Chat.ClearHighlight, "clear selection")
	add("Chat", k.Chat.Expand, "expand/collapse")

	// Initialize bindings
	add("Initialize", k.Initialize.Yes, "yes")
	add("Initialize", k.Initialize.No, "no")
	add("Initialize", k.Initialize.Switch, "switch")
	add("Initialize", k.Initialize.Enter, "select")

	// Sort by section then by key
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].Section != bindings[j].Section {
			return bindings[i].Section < bindings[j].Section
		}
		return bindings[i].Key < bindings[j].Key
	})

	// Deduplicate by key within each section
	return slices.CompactFunc(bindings, func(a, b KeyBinding) bool {
		return a.Section == b.Section && a.Key == b.Key
	})
}

type KeyMap struct {
	Editor struct {
		AddFile     key.Binding
		SendMessage key.Binding
		OpenEditor  key.Binding
		Newline     key.Binding
		AddImage    key.Binding
		PasteImage  key.Binding
		MentionFile key.Binding
		Commands    key.Binding

		// Attachments key maps
		AttachmentDeleteMode key.Binding
		Escape               key.Binding
		DeleteAllAttachments key.Binding
		PreviewAttachment    key.Binding

		// History navigation
		HistoryPrev key.Binding
		HistoryNext key.Binding

		// Clear prompt
		ClearPrompt key.Binding
	}

	Chat struct {
		NewSession     key.Binding
		AddAttachment  key.Binding
		Cancel         key.Binding
		Tab            key.Binding
		Details        key.Binding
		TogglePills    key.Binding
		PillLeft       key.Binding
		PillRight      key.Binding
		Down           key.Binding
		Up             key.Binding
		UpDown         key.Binding
		DownOneItem    key.Binding
		UpOneItem      key.Binding
		UpDownOneItem  key.Binding
		PageDown       key.Binding
		PageUp         key.Binding
		HalfPageDown   key.Binding
		HalfPageUp     key.Binding
		Home           key.Binding
		End            key.Binding
		Copy           key.Binding
		ClearHighlight key.Binding
		Expand         key.Binding
		ScrollLeft     key.Binding
		ScrollRight    key.Binding
	}

	Initialize struct {
		Yes,
		No,
		Enter,
		Switch key.Binding
	}

	// Global key maps
	Quit       key.Binding
	Help       key.Binding
	Commands   key.Binding
	Models     key.Binding
	Suspend    key.Binding
	Sessions   key.Binding
	Tab        key.Binding
	ToggleYolo key.Binding
}

func DefaultKeyMap() KeyMap {
	km := KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("ctrl+g"),
			key.WithHelp("ctrl+g", "more"),
		),
		Commands: key.NewBinding(
			key.WithKeys("ctrl+p"),
			key.WithHelp("ctrl+p", "commands"),
		),
		Models: key.NewBinding(
			key.WithKeys("ctrl+m", "ctrl+l"),
			key.WithHelp("ctrl+l", "models"),
		),
		Suspend: key.NewBinding(
			key.WithKeys("ctrl+z"),
			key.WithHelp("ctrl+z", "suspend"),
		),
		Sessions: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "sessions"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "change focus"),
		),
		ToggleYolo: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "toggle yolo"),
		),
	}

	km.Editor.AddFile = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "add file"),
	)
	km.Editor.SendMessage = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "send"),
	)
	km.Editor.OpenEditor = key.NewBinding(
		key.WithKeys("ctrl+o"),
		key.WithHelp("ctrl+o", "open editor"),
	)
	km.Editor.Newline = key.NewBinding(
		key.WithKeys("shift+enter", "ctrl+j"),
		// "ctrl+j" is a common keybinding for newline in many editors. If
		// the terminal supports "shift+enter", we substitute the help tex
		// to reflect that.
		key.WithHelp("ctrl+j", "newline"),
	)
	km.Editor.AddImage = key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "add image"),
	)
	km.Editor.PasteImage = key.NewBinding(
		key.WithKeys("ctrl+v", "super+v"),
		key.WithHelp("ctrl+v", "paste image from clipboard"),
	)
	km.Editor.MentionFile = key.NewBinding(
		key.WithKeys("@"),
		key.WithHelp("@", "mention file"),
	)
	km.Editor.Commands = key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "commands"),
	)
	km.Editor.AttachmentDeleteMode = key.NewBinding(
		key.WithKeys("ctrl+r"),
		key.WithHelp("ctrl+r+{i}", "delete attachment at index i"),
	)
	km.Editor.Escape = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel delete mode"),
	)
	km.Editor.DeleteAllAttachments = key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("ctrl+r+r", "delete all attachments"),
	)
	km.Editor.PreviewAttachment = key.NewBinding(
		key.WithKeys("ctrl+alt+v"),
		key.WithHelp("ctrl+alt+v", "preview attachment"),
	)
	km.Editor.HistoryPrev = key.NewBinding(
		key.WithKeys("up"),
	)
	km.Editor.HistoryNext = key.NewBinding(
		key.WithKeys("down"),
	)
	km.Editor.ClearPrompt = key.NewBinding(
		key.WithKeys("ctrl+x"),
		key.WithHelp("ctrl+x", "clear prompt"),
	)
	km.Chat.NewSession = key.NewBinding(
		key.WithKeys("ctrl+n"),
		key.WithHelp("ctrl+n", "new session"),
	)
	km.Chat.AddAttachment = key.NewBinding(
		key.WithKeys("ctrl+f"),
		key.WithHelp("ctrl+f", "add attachment"),
	)
	km.Chat.Cancel = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "cancel"),
	)
	km.Chat.Tab = key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "change focus"),
	)
	km.Chat.Details = key.NewBinding(
		key.WithKeys("ctrl+d"),
		key.WithHelp("ctrl+d", "toggle details"),
	)
	km.Chat.TogglePills = key.NewBinding(
		key.WithKeys("ctrl+t", "ctrl+space"),
		key.WithHelp("ctrl+t", "toggle tasks"),
	)
	km.Chat.PillLeft = key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←/→", "switch section"),
	)
	km.Chat.PillRight = key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("←/→", "switch section"),
	)

	km.Chat.Down = key.NewBinding(
		key.WithKeys("down", "ctrl+j", "j"),
		key.WithHelp("↓", "down"),
	)
	km.Chat.Up = key.NewBinding(
		key.WithKeys("up", "ctrl+k", "k"),
		key.WithHelp("↑", "up"),
	)
	km.Chat.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑↓", "scroll"),
	)
	km.Chat.UpOneItem = key.NewBinding(
		key.WithKeys("shift+up", "K"),
		key.WithHelp("shift+↑", "up one item"),
	)
	km.Chat.DownOneItem = key.NewBinding(
		key.WithKeys("shift+down", "J"),
		key.WithHelp("shift+↓", "down one item"),
	)
	km.Chat.UpDownOneItem = key.NewBinding(
		key.WithKeys("shift+up", "shift+down"),
		key.WithHelp("shift+↑↓", "scroll one item"),
	)
	km.Chat.HalfPageDown = key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "half page down"),
	)
	km.Chat.PageDown = key.NewBinding(
		key.WithKeys("pgdown", " ", "f"),
		key.WithHelp("f/pgdn", "page down"),
	)
	km.Chat.PageUp = key.NewBinding(
		key.WithKeys("pgup", "b"),
		key.WithHelp("b/pgup", "page up"),
	)
	km.Chat.HalfPageUp = key.NewBinding(
		key.WithKeys("u"),
		key.WithHelp("u", "half page up"),
	)
	km.Chat.Home = key.NewBinding(
		key.WithKeys("g", "home"),
		key.WithHelp("g", "home"),
	)
	km.Chat.End = key.NewBinding(
		key.WithKeys("G", "end"),
		key.WithHelp("G", "end"),
	)
	km.Chat.Copy = key.NewBinding(
		key.WithKeys("c", "y", "C", "Y"),
		key.WithHelp("c/y", "copy"),
	)
	km.Chat.ClearHighlight = key.NewBinding(
		key.WithKeys("esc", "alt+esc"),
		key.WithHelp("esc", "clear selection"),
	)
	km.Chat.Expand = key.NewBinding(
		key.WithKeys("space"),
		key.WithHelp("space", "expand/collapse"),
	)
	km.Chat.ScrollLeft = key.NewBinding(
		key.WithKeys("shift+left", "H"),
		key.WithHelp("shift+←/H", "scroll left"),
	)
	km.Chat.ScrollRight = key.NewBinding(
		key.WithKeys("shift+right", "L"),
		key.WithHelp("shift+→/L", "scroll right"),
	)
	km.Initialize.Yes = key.NewBinding(
		key.WithKeys("y", "Y"),
		key.WithHelp("y", "yes"),
	)
	km.Initialize.No = key.NewBinding(
		key.WithKeys("n", "N", "esc", "alt+esc"),
		key.WithHelp("n", "no"),
	)
	km.Initialize.Switch = key.NewBinding(
		key.WithKeys("left", "right", "tab"),
		key.WithHelp("tab", "switch"),
	)
	km.Initialize.Enter = key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	)

	return km
}

// parseKeyBinding parses a comma-separated key string (e.g., "ctrl+alt+v")
// and returns a *key.Binding, or nil if the string is empty.
func parseKeyBinding(keys string) *key.Binding {
	if keys == "" {
		return nil
	}
	b := key.NewBinding(key.WithKeys(strings.Split(keys, ",")...))
	return &b
}

// KeyMapFromConfig builds a KeyMap using config overrides where provided,
// falling back to DefaultKeyMap() for any binding not specified.
func KeyMapFromConfig(keybindings map[string]string) KeyMap {
	km := DefaultKeyMap()

	// Global bindings
	if v, ok := keybindings["quit"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Quit = *b
		}
	}
	if v, ok := keybindings["help"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Help = *b
		}
	}
	if v, ok := keybindings["commands"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Commands = *b
		}
	}
	if v, ok := keybindings["models"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Models = *b
		}
	}
	if v, ok := keybindings["suspend"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Suspend = *b
		}
	}
	if v, ok := keybindings["sessions"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Sessions = *b
		}
	}
	if v, ok := keybindings["tab"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Tab = *b
		}
	}
	if v, ok := keybindings["toggle_yolo"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.ToggleYolo = *b
		}
	}

	// Editor bindings
	if v, ok := keybindings["send_message"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.SendMessage = *b
		}
	}
	if v, ok := keybindings["open_editor"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.OpenEditor = *b
		}
	}
	if v, ok := keybindings["newline"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.Newline = *b
		}
	}
	if v, ok := keybindings["add_image"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.AddImage = *b
		}
	}
	if v, ok := keybindings["paste_image"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.PasteImage = *b
		}
	}
	if v, ok := keybindings["mention_file"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.MentionFile = *b
		}
	}
	if v, ok := keybindings["add_file"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.AddFile = *b
		}
	}
	if v, ok := keybindings["attachment_delete_mode"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.AttachmentDeleteMode = *b
		}
	}
	if v, ok := keybindings["attachment_escape"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.Escape = *b
		}
	}
	if v, ok := keybindings["delete_all_attachments"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.DeleteAllAttachments = *b
		}
	}
	if v, ok := keybindings["preview_attachment"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.PreviewAttachment = *b
		}
	}
	if v, ok := keybindings["history_prev"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.HistoryPrev = *b
		}
	}
	if v, ok := keybindings["history_next"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.HistoryNext = *b
		}
	}
	if v, ok := keybindings["clear_prompt"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Editor.ClearPrompt = *b
		}
	}

	// Chat bindings
	if v, ok := keybindings["new_session"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.NewSession = *b
		}
	}
	if v, ok := keybindings["add_attachment"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.AddAttachment = *b
		}
	}
	if v, ok := keybindings["cancel"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Cancel = *b
		}
	}
	if v, ok := keybindings["details"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Details = *b
		}
	}
	if v, ok := keybindings["toggle_pills"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.TogglePills = *b
		}
	}
	if v, ok := keybindings["pill_left"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.PillLeft = *b
		}
	}
	if v, ok := keybindings["pill_right"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.PillRight = *b
		}
	}
	if v, ok := keybindings["scroll_down"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Down = *b
		}
	}
	if v, ok := keybindings["scroll_up"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Up = *b
		}
	}
	if v, ok := keybindings["scroll"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.UpDown = *b
		}
	}
	if v, ok := keybindings["scroll_one_item_up"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.UpOneItem = *b
		}
	}
	if v, ok := keybindings["scroll_one_item_down"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.DownOneItem = *b
		}
	}
	if v, ok := keybindings["scroll_one_item"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.UpDownOneItem = *b
		}
	}
	if v, ok := keybindings["half_page_down"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.HalfPageDown = *b
		}
	}
	if v, ok := keybindings["page_down"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.PageDown = *b
		}
	}
	if v, ok := keybindings["page_up"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.PageUp = *b
		}
	}
	if v, ok := keybindings["half_page_up"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.HalfPageUp = *b
		}
	}
	if v, ok := keybindings["home"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Home = *b
		}
	}
	if v, ok := keybindings["end"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.End = *b
		}
	}
	if v, ok := keybindings["copy"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Copy = *b
		}
	}
	if v, ok := keybindings["clear_highlight"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.ClearHighlight = *b
		}
	}
	if v, ok := keybindings["expand"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Chat.Expand = *b
		}
	}

	// Initialize bindings
	if v, ok := keybindings["initialize_yes"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Initialize.Yes = *b
		}
	}
	if v, ok := keybindings["initialize_no"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Initialize.No = *b
		}
	}
	if v, ok := keybindings["initialize_switch"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Initialize.Switch = *b
		}
	}
	if v, ok := keybindings["initialize_enter"]; ok {
		if b := parseKeyBinding(v); b != nil {
			km.Initialize.Enter = *b
		}
	}

	return km
}
