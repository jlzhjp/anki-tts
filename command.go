package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"jlzhjp.dev/anki-tts/anki"
	"jlzhjp.dev/anki-tts/textutil"
	"jlzhjp.dev/anki-tts/tts"
	"jlzhjp.dev/anki-tts/tui"
	"jlzhjp.dev/anki-tts/workflow"
)

type commandOptions struct {
	decks         []string
	noteTemplates []string
	fieldMatches  []string
	fromField     string
	toField       string
	service       string
	limit         int
	interactive   bool
	yes           bool
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
					Selector:  selector,
					FromField: options.fromField,
					ToField:   options.toField,
					Service:   options.service,
					Yes:       options.yes,
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
		Decks:         uniqueStrings(o.decks),
		NoteTemplates: uniqueStrings(o.noteTemplates),
		FieldMatchers: matchers,
		Limit:         o.limit,
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

func runBatch(ctx context.Context, appWorkflow *workflow.Service, selector workflow.NoteSelector, options commandOptions, input io.Reader, output io.Writer) error {
	required := []struct{ name, value string }{
		{"--from-field", options.fromField},
		{"--to-field", options.toField},
		{"--service", options.service},
	}
	for _, flag := range required {
		if strings.TrimSpace(flag.value) == "" {
			return fmt.Errorf("%s is required in batch mode", flag.name)
		}
	}
	service, err := resolveService(appWorkflow.Services(), options.service)
	if err != nil {
		return err
	}
	notes, err := appWorkflow.SelectNotes(ctx, selector)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Fprintln(output, "No notes matched the selectors.")
		return nil
	}
	if err := validateNotes(notes, options.fromField, options.toField); err != nil {
		return err
	}

	overwrites := showNotes(output, notes, options.fromField, options.toField)
	if !options.yes {
		reader := bufio.NewReader(input)
		ok, err := confirm(reader, output, "Generate audio for these notes?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(output, "Cancelled.")
			return nil
		}
		if overwrites > 0 {
			ok, err = confirm(reader, output, fmt.Sprintf("Replace %d non-empty destination field(s)?", overwrites))
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(output, "Cancelled.")
				return nil
			}
		}
	}

	failures := make([]error, 0)
	succeeded := 0
	for _, note := range notes {
		_, err := appWorkflow.Generate(ctx, workflow.GenerateRequest{
			Note:             note,
			SourceField:      options.fromField,
			DestinationField: options.toField,
			DestinationMode:  workflow.ReplaceDestination,
			Service:          service,
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("note %d: %w", note.ID, err))
			fmt.Fprintf(output, "FAILED note %d: %v\n", note.ID, err)
			continue
		}
		succeeded++
		fmt.Fprintf(output, "Generated note %d\n", note.ID)
	}
	fmt.Fprintf(output, "\nSummary: %d succeeded, %d failed.\n", succeeded, len(failures))
	if len(failures) > 0 {
		return fmt.Errorf("audio generation failed for %d note(s): %w", len(failures), errors.Join(failures...))
	}
	return nil
}

func validateNotes(notes []anki.Note, fromField, toField string) error {
	var invalid []string
	for _, note := range notes {
		if _, ok := note.Fields[fromField]; !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing source field %q", note.ID, fromField))
			continue
		}
		if _, ok := note.Fields[toField]; !ok {
			invalid = append(invalid, fmt.Sprintf("note %d: missing destination field %q", note.ID, toField))
			continue
		}
		text, err := textutil.FromHTML(note.Fields[fromField].Value)
		if err != nil || strings.TrimSpace(text) == "" {
			invalid = append(invalid, fmt.Sprintf("note %d: source field %q has no speakable text", note.ID, fromField))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("selected notes cannot be processed:\n  %s", strings.Join(invalid, "\n  "))
	}
	return nil
}

func showNotes(output io.Writer, notes []anki.Note, fromField, toField string) int {
	fmt.Fprintln(output, "Selected notes:")
	overwrites := 0
	for _, note := range notes {
		preview, _ := textutil.FromHTML(note.Fields[fromField].Value)
		preview = strings.Join(strings.Fields(preview), " ")
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:57]) + "..."
		}
		status := "empty destination"
		if strings.TrimSpace(note.Fields[toField].Value) != "" {
			overwrites++
			status = highlight(output, "WILL OVERWRITE")
		}
		fmt.Fprintf(output, "  %d  %-20s  %s  [%s]\n", note.ID, note.ModelName, preview, status)
	}
	return overwrites
}

func highlight(output io.Writer, value string) string {
	file, ok := output.(*os.File)
	if !ok {
		return value
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return value
	}
	return "\x1b[1;31m" + value + "\x1b[0m"
}

func confirm(reader *bufio.Reader, output io.Writer, prompt string) (bool, error) {
	fmt.Fprintf(output, "%s [y/N] ", prompt)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}

func resolveService(services []tts.NamedService, name string) (tts.NamedService, error) {
	for _, service := range services {
		if service.Name == name {
			return service, nil
		}
	}
	return tts.NamedService{}, fmt.Errorf("TTS service %q is not configured", name)
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
