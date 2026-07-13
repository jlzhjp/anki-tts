package textutil

import "testing"

func TestFromHTML(t *testing.T) {
	got, err := FromHTML(`<div>Hello&nbsp;<b>world</b><br>next &amp; last</div>`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Hello world\nnext & last"; got != want {
		t.Fatalf("FromHTML() = %q, want %q", got, want)
	}
}
