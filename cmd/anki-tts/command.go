package main

import (
	"errors"
	"io"

	"github.com/spf13/cobra"
	"jlzhjp.dev/ankitts"
)

type commandOptions struct {
	filter      string
	fromField   string
	toField     string
	service     string
	limit       int
	interactive bool
	yes         bool
}

func newRootCommand(input io.Reader, output, errorOutput io.Writer) *cobra.Command {
	var options commandOptions
	cmd := &cobra.Command{
		Use:   "anki-tts",
		Short: "Generate and attach TTS audio to Anki notes",
		Long: `Generate TTS audio for notes selected with Anki's native search
syntax. With an empty filter, batch mode considers every note.`,
		Example: `  anki-tts --filter 'deck:Japanese note:Basic Front:re:猫' \
    --from-field Front --to-field Audio \
    --service openrouter --limit 20

  anki-tts --interactive --filter 'deck:Japanese Front:re:^猫$'`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("limit") && options.limit <= 0 {
				return errors.New("--limit must be greater than zero")
			}
			app, err := buildApplication()
			if err != nil {
				return err
			}
			return runApplication(cmd.Context(), app, runOptions{
				Query:     ankitts.NoteQuery{Filter: options.filter, Limit: options.limit},
				FromField: options.fromField, ToField: options.toField,
				Service: options.service, Yes: options.yes, Interactive: options.interactive,
			}, input, output)
		},
	}
	cmd.SetIn(input)
	cmd.SetOut(output)
	cmd.SetErr(errorOutput)

	flags := cmd.Flags()
	flags.StringVar(&options.filter, "filter", "", "select notes using Anki search syntax")
	flags.StringVar(&options.fromField, "from-field", "", "field containing text to speak")
	flags.StringVar(&options.toField, "to-field", "", "field in which to store the audio tag")
	flags.StringVar(&options.service, "service", "", "configured TTS service to use")
	flags.IntVar(&options.limit, "limit", 0, "maximum number of matching notes")
	flags.BoolVar(&options.interactive, "interactive", false, "select and generate notes interactively")
	flags.BoolVar(&options.yes, "yes", false, "accept confirmation prompts")

	registerCompletions(cmd, &options)
	cmd.AddCommand(newCompletionCommand())
	return cmd
}
