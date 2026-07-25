package batch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"jlzhjp.dev/anki-tts"
)

func prepare(
	ctx context.Context,
	app Application,
	options Options,
	selection ankitts.NoteSelection,
) (ankitts.Plan, error) {
	return app.Prepare(ankitts.GenerationRequest{
		Notes:            app.Notes(ctx, selection, ankitts.NoteLoadOptions{}),
		SourceField:      options.FromField,
		DestinationField: options.ToField,
		Service:          options.Service,
	})
}

// RunPlain executes a batch workflow without the terminal UI.
func RunPlain(
	ctx context.Context,
	app Application,
	options Options,
	selection ankitts.NoteSelection,
	input io.Reader,
	output io.Writer,
) error {
	showNoteIDs(output, "Selected notes:", selection.IDs)
	var reader *bufio.Reader
	if !options.Yes {
		reader = bufio.NewReader(input)
		accepted, err := confirmPlain(reader, output, "Generate audio for these notes?")
		if err != nil {
			return err
		}
		if !accepted {
			fmt.Fprintln(output, "Cancelled.")
			return nil
		}
	}

	plan, err := prepare(ctx, app, options, selection)
	if err != nil {
		return err
	}
	overwrites := overwriteNoteIDs(plan.Items())
	if !options.Yes && len(overwrites) > 0 {
		showNoteIDs(output, "Notes with non-empty destination fields:", overwrites)
		accepted, err := confirmPlain(
			reader,
			output,
			fmt.Sprintf("Replace %d non-empty destination field(s)?", len(overwrites)),
		)
		if err != nil {
			return err
		}
		if !accepted {
			fmt.Fprintln(output, "Cancelled.")
			return nil
		}
	}

	result, executionErr := app.Execute(ctx, plan, ankitts.ExecuteOptions{
		Progress: &plainProgressReporter{output: output},
	})
	return reportBatchResult(output, result, executionErr)
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
	return resultError(result, executionErr)
}

func resultError(result ankitts.BatchResult, executionErr error) error {
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

func showNoteIDs(output io.Writer, title string, ids []int64) {
	fmt.Fprintln(output, title)
	for _, id := range ids {
		fmt.Fprintf(output, "  %d\n", id)
	}
}

func overwriteNoteIDs(notes []ankitts.PlannedNote) []int64 {
	ids := make([]int64, 0)
	for _, note := range notes {
		if note.WillOverwrite {
			ids = append(ids, note.NoteID)
		}
	}
	return ids
}

func red(value string) string {
	return "\x1b[1;31m" + value + "\x1b[0m"
}

type plainProgressReporter struct {
	mu           sync.Mutex
	output       io.Writer
	descriptions map[int]string
	stages       map[int]string
}

func (r *plainProgressReporter) Report(event ankitts.ProgressEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.descriptions == nil {
		r.descriptions = make(map[int]string)
		r.stages = make(map[int]string)
	}
	if event.Stage != "" {
		r.stages[event.Index] = event.Stage
	}
	if event.Kind == ankitts.ProgressStarted {
		delete(r.descriptions, event.Index)
	}
	if event.Description != "" {
		r.descriptions[event.Index] = event.Description
	}
	description := r.descriptions[event.Index]
	if description == "" {
		description = r.stages[event.Index]
	}

	switch event.Kind {
	case ankitts.ProgressRetrying:
		fmt.Fprintf(
			r.output,
			"Retrying note %d (%s, attempt %d/%d): %v\n",
			event.NoteID,
			description,
			event.Attempt+1,
			event.MaxAttempts,
			event.Err,
		)
	case ankitts.ProgressFailed:
		fmt.Fprintf(
			r.output,
			"FAILED note %d (%s): %v\n",
			event.NoteID,
			description,
			event.Err,
		)
	case ankitts.ProgressItemCompleted:
		fmt.Fprintf(r.output, "Generated note %d\n", event.NoteID)
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
