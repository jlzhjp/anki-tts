package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/x/term"

	"jlzhjp.dev/anki-tts"
)

func prepareBatch(
	ctx context.Context,
	app application,
	options runOptions,
) (ankitts.Plan, bool, error) {
	notes, err := app.SelectNotes(ctx, options.Selector)
	if err != nil {
		return ankitts.Plan{}, false, err
	}
	if len(notes) == 0 {
		return ankitts.Plan{}, false, nil
	}

	plan, err := app.Prepare(ankitts.GenerationRequest{
		Notes:            notes,
		SourceField:      options.FromField,
		DestinationField: options.ToField,
		Service:          options.Service,
	})
	if err != nil {
		return ankitts.Plan{}, false, err
	}
	return plan, true, nil
}

func runPlainBatch(
	ctx context.Context,
	app application,
	options runOptions,
	plan ankitts.Plan,
	input io.Reader,
	output io.Writer,
) error {
	overwrites := showNotes(output, plan.Items())
	if !options.Yes {
		confirmations := []plainConfirmation{
			{required: true, prompt: "Generate audio for these notes?"},
			{
				required: overwrites > 0,
				prompt: fmt.Sprintf(
					"Replace %d non-empty destination field(s)?",
					overwrites,
				),
			},
		}
		reader := bufio.NewReader(input)
		for _, confirmation := range confirmations {
			if !confirmation.required {
				continue
			}
			accepted, err := confirmPlain(reader, output, confirmation.prompt)
			if err != nil {
				return err
			}
			if !accepted {
				fmt.Fprintln(output, "Cancelled.")
				return nil
			}
		}
	}

	result, executionErr := app.Execute(ctx, plan, ankitts.ExecuteOptions{
		Progress: &plainProgressReporter{output: output},
	})
	return reportBatchResult(output, result, executionErr)
}

type plainConfirmation struct {
	required bool
	prompt   string
}

func reportBatchResult(
	output io.Writer,
	result ankitts.BatchResult,
	executionErr error,
) error {
	failures := make([]error, 0)
	succeeded := 0
	for _, item := range result.Items {
		if item.Err != nil {
			failures = append(failures, item.Err)
		} else {
			succeeded++
		}
	}

	fmt.Fprintf(output, "\nSummary: %d succeeded, %d failed.\n", succeeded, len(failures))
	ordered := append([]ankitts.ItemResult(nil), result.Items...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].NoteID < ordered[j].NoteID
	})
	for _, item := range ordered {
		if item.Err != nil {
			fmt.Fprintf(output, "  note %d: %v\n", item.NoteID, item.Err)
		}
	}
	return batchResultError(result, executionErr)
}

func batchResultError(result ankitts.BatchResult, executionErr error) error {
	if executionErr != nil {
		return executionErr
	}

	failures := make([]error, 0)
	for _, item := range result.Items {
		if item.Err != nil {
			failures = append(failures, item.Err)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf(
		"audio generation failed for %d note(s): %w",
		len(failures),
		errors.Join(failures...),
	)
}

func showNotes(output io.Writer, notes []ankitts.PlannedNote) int {
	fmt.Fprintln(output, "Selected notes:")
	overwrites := 0
	for _, note := range notes {
		preview := compactPreview(note.SourceText, 60)
		status := "empty destination"
		if note.WillOverwrite {
			overwrites++
			status = highlight(output, "WILL OVERWRITE")
		}
		fmt.Fprintf(
			output,
			"  %d  %-20s  %s  [%s]\n",
			note.Note.ID,
			note.Note.ModelName,
			preview,
			status,
		)
	}
	return overwrites
}

func compactPreview(value string, limit int) string {
	return truncate(strings.Join(strings.Fields(value), " "), limit)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
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
	return red(value)
}

func red(value string) string {
	return "\x1b[1;31m" + value + "\x1b[0m"
}

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}

type plainProgressReporter struct {
	mu     sync.Mutex
	output io.Writer
}

func (r *plainProgressReporter) Report(event ankitts.ProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch event.Kind {
	case ankitts.ProgressRetrying:
		fmt.Fprintf(
			r.output,
			"Retrying note %d (%s, attempt %d/%d): %v\n",
			event.NoteID,
			event.Operation,
			event.Attempt,
			event.MaxAttempts,
			event.Err,
		)
	case ankitts.ProgressFailed:
		fmt.Fprintf(
			r.output,
			"FAILED note %d (%s): %v\n",
			event.NoteID,
			event.Operation,
			event.Err,
		)
	case ankitts.ProgressCompleted:
		if event.Operation == ankitts.OperationUpdateNote {
			fmt.Fprintf(r.output, "Generated note %d\n", event.NoteID)
		}
	}
}

func confirmPlain(
	reader *bufio.Reader,
	output io.Writer,
	prompt string,
) (bool, error) {
	fmt.Fprintf(output, "%s [y/N] ", prompt)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
