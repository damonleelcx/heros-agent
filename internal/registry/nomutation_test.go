package registry

import (
	"reflect"
	"strings"
	"testing"
)

// Task 2.2 / prompt-authoring spec: "No interface — HTTP, CLI, UI, or library — SHALL express mutation
// or deletion of a published prompt version." This asserts the LIBRARY surface directly: the registry
// Store exposes no method that could update or delete a prompt. Immutability does not depend on callers
// choosing not to attempt it — the operation is not expressible.
//
// A test rather than a review note (design.md / QA 6.3): a reviewer can miss an added `DeletePrompt`;
// this fails the build the moment one appears.
func TestStore_ExposesNoPromptMutationMethod(t *testing.T) {
	forbidden := []string{"update", "delete", "mutate", "remove", "edit", "overwrite", "setprompt"}
	typ := reflect.TypeOf(&Store{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		if !strings.Contains(name, "prompt") {
			continue
		}
		for _, bad := range forbidden {
			if strings.Contains(name, bad) {
				t.Fatalf("registry.Store exposes %q — a published prompt version must never be mutable or deletable through any interface", typ.Method(i).Name)
			}
		}
	}
}
