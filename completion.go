package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"jlzhjp.dev/anki-tts/anki"
)

func registerCompletions(cmd *cobra.Command, options *commandOptions) {
	client := anki.NewClient()
	_ = cmd.RegisterFlagCompletionFunc("deck", completeValues(func(ctx context.Context) ([]string, error) {
		return client.ListDecks(ctx)
	}, &options.decks))
	_ = cmd.RegisterFlagCompletionFunc("note-template", completeValues(func(ctx context.Context) ([]string, error) {
		return client.ListNoteTemplates(ctx)
	}, &options.noteTemplates))
	fieldCompletion := func(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if strings.Contains(toComplete, "=") {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		fields, err := completionFields(cmd.Context(), client, options.noteTemplates)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		for index := range fields {
			fields[index] += "="
		}
		return fields, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
	_ = cmd.RegisterFlagCompletionFunc("field-match", fieldCompletion)
	plainFieldCompletion := func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		fields, err := completionFields(cmd.Context(), client, options.noteTemplates)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return fields, cobra.ShellCompDirectiveNoFileComp
	}
	_ = cmd.RegisterFlagCompletionFunc("from-field", plainFieldCompletion)
	_ = cmd.RegisterFlagCompletionFunc("to-field", plainFieldCompletion)
	_ = cmd.RegisterFlagCompletionFunc("service", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		configHome, err := os.UserConfigDir()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		cfg, err := loadConfig(filepath.Join(configHome, "anki-tts", configFileName))
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		services, err := buildServices(cfg)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		names := make([]string, 0, len(services.Services()))
		for _, service := range services.Services() {
			names = append(names, service.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
}

func completeValues(load func(context.Context) ([]string, error), selected *[]string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		values, err := load(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		used := make(map[string]struct{}, len(*selected))
		for _, value := range *selected {
			used[value] = struct{}{}
		}
		result := values[:0]
		for _, value := range values {
			if _, ok := used[value]; !ok {
				result = append(result, value)
			}
		}
		sort.Strings(result)
		return result, cobra.ShellCompDirectiveNoFileComp
	}
}

func completionFields(ctx context.Context, client *anki.Client, templates []string) ([]string, error) {
	if len(templates) == 0 {
		var err error
		templates, err = client.ListNoteTemplates(ctx)
		if err != nil {
			return nil, err
		}
	}
	set := make(map[string]struct{})
	for _, template := range templates {
		fields, err := client.ListTemplateFields(ctx, template)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			set[field] = struct{}{}
		}
	}
	fields := make([]string, 0, len(set))
	for field := range set {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields, nil
}

func newCompletionCommand() *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish|powershell]",
		Short:     "Generate a shell completion script",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			default:
				return cmd.Root().GenPowerShellCompletionWithDesc(cmd.OutOrStdout())
			}
		},
	}
}
