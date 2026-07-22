package ankitts

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestServiceContainer(t *testing.T) {
	container := NewServiceContainer()
	service := containerFakeService{}
	if err := container.Add("Zulu", service); err != nil {
		t.Fatal(err)
	}
	if err := container.Add("Alpha", service); err != nil {
		t.Fatal(err)
	}
	if names := container.Names(); !reflect.DeepEqual(names, []string{"Alpha", "Zulu"}) {
		t.Fatalf("service names = %v", names)
	}
	if err := container.Add("Alpha", service); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate error = %v", err)
	}
}

type containerFakeService struct{}

func (containerFakeService) Generate(context.Context, Input) (Voice, error) { return nil, nil }
