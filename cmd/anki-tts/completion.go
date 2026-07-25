package main

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func registerCompletions(cmd *cobra.Command, _ *commandOptions) {
	_ = cmd.RegisterFlagCompletionFunc("filter", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	})
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
		return services.Names(), cobra.ShellCompDirectiveNoFileComp
	})
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
