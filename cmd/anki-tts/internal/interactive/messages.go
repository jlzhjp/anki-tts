package interactive

import (
	tea "charm.land/bubbletea/v2"

	ankitts "jlzhjp.dev/anki-tts"
	"jlzhjp.dev/anki-tts/anki"
)

type decksLoadedMsg struct {
	decks []string
	err   error
}
type notesLoadedMsg struct {
	notes []anki.Note
	err   error
}
type deckSelectedMsg struct{ deck string }
type noteSelectedMsg struct{ note anki.Note }
type sourceSelectedMsg struct{ field string }
type destinationSelectedMsg struct{ field string }
type actionSelectedMsg struct{ confirmed bool }
type serviceSelectedMsg struct{ service string }
type generatedMsg struct {
	request ankitts.GenerationRequest
	result  ankitts.GenerateResult
	err     error
}
type screenFailedMsg struct {
	err   error
	retry tea.Cmd
}
type retryMsg struct{ cmd tea.Cmd }
type dismissErrorMsg struct{}

func messageCmd(message tea.Msg) tea.Cmd { return func() tea.Msg { return message } }
func failCmd(err error, retry tea.Cmd) tea.Cmd {
	return messageCmd(screenFailedMsg{err: err, retry: retry})
}
