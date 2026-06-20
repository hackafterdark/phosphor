package cmd

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/charmbracelet/x/term"
	"github.com/hackafterdark/phosphor/internal/ui/model"
	"github.com/spf13/cobra"
)

var bindingsCmd = &cobra.Command{
	Use:   "bindings",
	Short: "List all keybindings",
	Long:  `List all available keybindings in Phosphor, grouped by section.`,
	Run: func(cmd *cobra.Command, _ []string) {
		km := model.DefaultKeyMap()
		bindings := km.Bindings()

		if term.IsTerminal(os.Stdout.Fd()) {
			printBindings(cmd, bindings)
			return
		}
		for _, b := range bindings {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", b.Section, b.Key, b.Help)
		}
	},
}

func printBindings(_ *cobra.Command, bindings []model.KeyBinding) {
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(charmtone.Charple)

	var currentSection string
	for _, b := range bindings {
		if b.Section != currentSection {
			if currentSection != "" {
				fmt.Println()
			}
			fmt.Println(sectionStyle.Render(b.Section))
			currentSection = b.Section
		}
		keys := lipgloss.NewStyle().Bold(true).Render(b.Key)
		help := lipgloss.NewStyle().Foreground(charmtone.Squid).Render(b.Help)
		pad := strings.Repeat(" ", 2)
		fmt.Println(pad + keys + ": " + help)
	}
}
