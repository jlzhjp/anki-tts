package tts

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestContainer(t *testing.T) {
	container := NewContainer()
	service := fakeService{}
	if err := container.Add("Zulu", service); err != nil {
		t.Fatal(err)
	}
	if err := container.Add("Alpha", service); err != nil {
		t.Fatal(err)
	}
	got := container.Services()
	if names := []string{got[0].Name, got[1].Name}; !reflect.DeepEqual(names, []string{"Alpha", "Zulu"}) {
		t.Fatalf("service names = %v", names)
	}
	if err := container.Add("Alpha", service); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate error = %v", err)
	}
}

type fakeService struct{}

func (fakeService) Generate(context.Context, Input) (Voice, error) { return Voice{}, nil }
