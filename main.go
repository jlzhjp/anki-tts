package main

import (
	"context"
	"fmt"
	"os"

	"jlzhjp.dev/anki-tts/anki"
)

func main() {
	client := anki.NewClient()
	decks, err := client.ListDecks(context.Background())
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		return
	}
	for _, deck := range decks {
		fmt.Println(deck)
		notes, err := client.ListNotes(context.Background(), deck)
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			return
		}

		for _, note := range notes {
			fmt.Printf("\t%v", note.Fields)
		}
	}
}
