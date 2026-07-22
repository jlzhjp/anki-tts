package batch

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

	ankitts "jlzhjp.dev/anki-tts"
)

func run(ctx context.Context, app Application, options Options, input io.Reader, output io.Writer) error {
	required := []struct{ name, value string }{
		{"--from-field", options.FromField},
		{"--to-field", options.ToField},
		{"--service", options.Service},
	}
	for _, flag := range required {
		if strings.TrimSpace(flag.value) == "" {
			return fmt.Errorf("%s is required in batch mode", flag.name)
		}
	}
	notes, err := app.SelectNotes(ctx, options.Selector)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Fprintln(output, "No notes matched the selectors.")
		return nil
	}
	plan, err := app.Prepare(ankitts.GenerationRequest{
		Notes: notes, SourceField: options.FromField, DestinationField: options.ToField, Service: options.Service,
	})
	if err != nil {
		return err
	}
	if isTerminal(input) && isTerminal(output) {
		result, executionErr, processed := runBatchTUI(ctx, app, plan, options.Yes, input, output)
		if !processed {
			return nil
		}
		return batchResultError(result, executionErr)
	}

	overwrites := showNotes(output, plan.Items())
	if !options.Yes {
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

	reporter := &plainProgressReporter{output: output}
	result, executionErr := app.Execute(ctx, plan, ankitts.ExecuteOptions{
		Progress: reporter,
	})
	return reportBatchResult(output, result, executionErr)
}

func reportBatchResult(output io.Writer, result ankitts.BatchResult, executionErr error) error {
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
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].NoteID < ordered[j].NoteID })
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
	return fmt.Errorf("audio generation failed for %d note(s): %w", len(failures), errors.Join(failures...))
}

func showNotes(output io.Writer, notes []ankitts.PlannedNote) int {
	fmt.Fprintln(output, "Selected notes:")
	overwrites := 0
	for _, note := range notes {
		preview := strings.Join(strings.Fields(note.SourceText), " ")
		if len([]rune(preview)) > 60 {
			preview = string([]rune(preview)[:57]) + "..."
		}
		status := "empty destination"
		if note.WillOverwrite {
			overwrites++
			status = highlight(output, "WILL OVERWRITE")
		}
		fmt.Fprintf(output, "  %d  %-20s  %s  [%s]\n", note.Note.ID, note.Note.ModelName, preview, status)
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

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(file.Fd())
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
		fmt.Fprintf(r.output, "Retrying note %d (%s, attempt %d/%d): %v\n", event.NoteID, event.Operation, event.Attempt, event.MaxAttempts, event.Err)
	case ankitts.ProgressFailed:
		fmt.Fprintf(r.output, "FAILED note %d (%s): %v\n", event.NoteID, event.Operation, event.Err)
	case ankitts.ProgressCompleted:
		if event.Operation == ankitts.OperationUpdateNote {
			fmt.Fprintf(r.output, "Generated note %d\n", event.NoteID)
		}
	}
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
