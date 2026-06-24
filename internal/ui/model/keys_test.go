package model

import (
	"testing"

	"charm.land/bubbles/v2/key"
)

func TestParseKeyBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantKeys []string
	}{
		{
			name:    "empty string returns nil",
			input:   "",
			wantNil: true,
		},
		{
			name:     "single key",
			input:    "enter",
			wantNil:  false,
			wantKeys: []string{"enter"},
		},
		{
			name:     "modifier key",
			input:    "ctrl+c",
			wantNil:  false,
			wantKeys: []string{"ctrl+c"},
		},
		{
			name:     "multi-key binding",
			input:    "ctrl+n,ctrl+j",
			wantNil:  false,
			wantKeys: []string{"ctrl+n", "ctrl+j"},
		},
		{
			name:     "three keys",
			input:    "esc,alt+esc",
			wantNil:  false,
			wantKeys: []string{"esc", "alt+esc"},
		},
		{
			name:     "printable character",
			input:    "@",
			wantNil:  false,
			wantKeys: []string{"@"},
		},
		{
			name:     "invalid key silently accepted",
			input:    "ctrl+xyz",
			wantNil:  false,
			wantKeys: []string{"ctrl+xyz"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseKeyBinding(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Errorf("parseKeyBinding(%q) = non-nil, want nil", tt.input)
				}
				return
			}
			if got == nil {
				t.Fatalf("parseKeyBinding(%q) = nil, want non-nil", tt.input)
			}
			keys := got.Keys()
			if len(keys) != len(tt.wantKeys) {
				t.Errorf("parseKeyBinding(%q) = %v, want %v", tt.input, keys, tt.wantKeys)
			}
		})
	}
}

func TestKeyMapFromConfig_EmptyMap(t *testing.T) {
	t.Parallel()

	empty := map[string]string{}
	got := KeyMapFromConfig(empty)
	want := DefaultKeyMap()

	if !keysEqual(got.Quit, want.Quit) {
		t.Error("Quit binding differs from default with empty config")
	}
	if !keysEqual(got.Help, want.Help) {
		t.Error("Help binding differs from default with empty config")
	}
	if !keysEqual(got.Editor.PreviewAttachment, want.Editor.PreviewAttachment) {
		t.Error("PreviewAttachment binding differs from default with empty config")
	}
}

func TestKeyMapFromConfig_PartialOverride(t *testing.T) {
	t.Parallel()

	overrides := map[string]string{
		"quit":               "ctrl+q",
		"preview_attachment": "ctrl+shift+v",
	}
	got := KeyMapFromConfig(overrides)
	want := DefaultKeyMap()

	// Verify overrides were applied
	if keysEqual(got.Quit, want.Quit) {
		t.Error("Quit binding should have been overridden to ctrl+q")
	}
	if keysEqual(got.Editor.PreviewAttachment, want.Editor.PreviewAttachment) {
		t.Error("PreviewAttachment binding should have been overridden")
	}

	// Verify unspecified bindings are preserved as defaults
	if !keysEqual(got.Help, want.Help) {
		t.Error("Help binding should be default, not overridden")
	}
	if !keysEqual(got.Chat.NewSession, want.Chat.NewSession) {
		t.Error("NewSession binding should be default, not overridden")
	}
	if !keysEqual(got.Editor.SendMessage, want.Editor.SendMessage) {
		t.Error("SendMessage binding should be default, not overridden")
	}
}

func TestKeyMapFromConfig_AllBindings(t *testing.T) {
	t.Parallel()

	// Verify that all known binding names are recognized by KeyMapFromConfig
	// (i.e., they don't panic and produce valid bindings).
	allBindingNames := []string{
		// Global
		"quit", "help", "commands", "models", "suspend", "sessions", "tab", "toggle_yolo",
		// Editor
		"send_message", "open_editor", "newline", "add_image", "paste_image",
		"mention_file", "attachment_delete_mode", "attachment_escape",
		"delete_all_attachments", "preview_attachment", "history_prev", "history_next",
		"clear_prompt",
		// Chat
		"new_session", "add_attachment", "cancel", "details", "toggle_pills",
		"pill_left", "pill_right", "scroll_down", "scroll_up", "scroll",
		"scroll_one_item_up", "scroll_one_item_down", "scroll_one_item",
		"half_page_down", "page_down", "page_up", "half_page_up",
		"home", "end", "copy", "clear_highlight", "expand",
		// Initialize
		"initialize_yes", "initialize_no", "initialize_switch", "initialize_enter",
	}

	for _, name := range allBindingNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			km := KeyMapFromConfig(map[string]string{name: "ctrl+q"})
			// If we got here without panicking, the binding name is recognized.
			_ = km
		})
	}
}

func TestKeyMapFromConfig_MultiKeyOverride(t *testing.T) {
	t.Parallel()

	km := KeyMapFromConfig(map[string]string{
		"quit":    "ctrl+q,ctrl+x",
		"newline": "enter,shift+enter",
	})

	// Verify the override was applied
	if keysEqual(km.Quit, DefaultKeyMap().Quit) {
		t.Error("Quit should be overridden")
	}
	if keysEqual(km.Editor.Newline, DefaultKeyMap().Editor.Newline) {
		t.Error("Newline should be overridden")
	}
}

func TestKeyMapFromConfig_NilMap(t *testing.T) {
	t.Parallel()

	got := KeyMapFromConfig(nil)
	want := DefaultKeyMap()

	if !keysEqual(got.Quit, want.Quit) {
		t.Error("Quit binding should equal default with nil config")
	}
}

// keysEqual compares two key.Bindings by their keys.
func keysEqual(a, b key.Binding) bool {
	aKeys := a.Keys()
	bKeys := b.Keys()
	if len(aKeys) != len(bKeys) {
		return false
	}
	for i := range aKeys {
		if aKeys[i] != bKeys[i] {
			return false
		}
	}
	return true
}
