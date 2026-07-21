package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"jlzhjp.dev/anki-tts/tui"
	"jlzhjp.dev/anki-tts/workflow"
)

type commandOptions struct {
	decks                []string
	noteTemplates        []string
	fieldMatches         []string
	fromField            string
	toField              string
	service              string
	limit                int
	interactive          bool
	yes                  bool
	synthesisConcurrency int
	audioConcurrency     int
}

func newRootCommand(input io.Reader, output, errorOutput io.Writer) *cobra.Command {
	var options commandOptions
	cmd := &cobra.Command{
		Use:   "anki-tts",
		Short: "Generate and attach TTS audio to Anki notes",
		Long: `Generate TTS audio for notes selected by deck, note template, and
field regular expressions. Repeated decks and templates are unions; all
field matchers must match. With no selectors, batch mode considers every note.`,
		Example: `  anki-tts --deck Japanese --note-template Basic \
    --field-match 'Front=猫' --from-field Front --to-field Audio \
    --service OpenRouter --limit 20

  anki-tts --interactive --deck Japanese --field-match 'Front=^猫$'`,
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("limit") && options.limit <= 0 {
				return errors.New("--limit must be greater than zero")
			}
			if options.synthesisConcurrency <= 0 {
				return errors.New("--synthesis-concurrency must be greater than zero")
			}
			if options.audioConcurrency <= 0 {
				return errors.New("--audio-concurrency must be greater than zero")
			}
			selector, err := options.selector()
			if err != nil {
				return err
			}
			appWorkflow, err := buildWorkflow()
			if err != nil {
				return err
			}
			if options.interactive {
				return runTUI(cmd.Context(), appWorkflow, tui.Options{
					Selector: selector, FromField: options.fromField, ToField: options.toField,
					Service: options.service, Yes: options.yes,
					SynthesisConcurrency: options.synthesisConcurrency,
					AudioConcurrency:     options.audioConcurrency,
				}, input, output)
			}
			return runBatch(cmd.Context(), appWorkflow, selector, options, input, output)
		},
	}
	cmd.SetIn(input)
	cmd.SetOut(output)
	cmd.SetErr(errorOutput)

	flags := cmd.Flags()
	flags.StringArrayVar(&options.decks, "deck", nil, "select an Anki deck (repeatable)")
	flags.StringArrayVar(&options.noteTemplates, "note-template", nil, "select an Anki note template (repeatable)")
	flags.StringArrayVar(&options.fieldMatches, "field-match", nil, "select notes by FIELD=REGEX (repeatable)")
	flags.StringVar(&options.fromField, "from-field", "", "field containing text to speak")
	flags.StringVar(&options.toField, "to-field", "", "field in which to store the audio tag")
	flags.StringVar(&options.service, "service", "", "configured TTS service to use")
	flags.IntVar(&options.limit, "limit", 0, "maximum number of matching notes")
	flags.BoolVar(&options.interactive, "interactive", false, "select and generate notes interactively")
	flags.BoolVar(&options.yes, "yes", false, "accept confirmation prompts")
	flags.IntVar(&options.synthesisConcurrency, "synthesis-concurrency", workflow.DefaultSynthesisConcurrency, "concurrent TTS synthesis workers")
	flags.IntVar(&options.audioConcurrency, "audio-concurrency", workflow.DefaultAudioConcurrency, "concurrent audio processing workers")

	registerCompletions(cmd, &options)
	cmd.AddCommand(newCompletionCommand())
	return cmd
}

func (o commandOptions) selector() (workflow.NoteSelector, error) {
	if o.limit < 0 {
		return workflow.NoteSelector{}, errors.New("--limit must be greater than zero")
	}
	if err := validateSelectorValues("--deck", o.decks); err != nil {
		return workflow.NoteSelector{}, err
	}
	if err := validateSelectorValues("--note-template", o.noteTemplates); err != nil {
		return workflow.NoteSelector{}, err
	}
	matchers := make([]workflow.FieldMatcher, 0, len(o.fieldMatches))
	for _, value := range o.fieldMatches {
		matcher, err := workflow.ParseFieldMatcher(value)
		if err != nil {
			return workflow.NoteSelector{}, err
		}
		matchers = append(matchers, matcher)
	}
	return workflow.NoteSelector{
		Decks: uniqueStrings(o.decks), NoteTemplates: uniqueStrings(o.noteTemplates),
		FieldMatchers: matchers, Limit: o.limit,
	}, nil
}

func validateSelectorValues(flag string, values []string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s values must not be empty", flag)
		}
	}
	return nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
