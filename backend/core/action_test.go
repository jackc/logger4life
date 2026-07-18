package core

import (
	"context"
	"errors"
	"testing"
)

type validationParams struct {
	Value string `json:"value"`
}

func (p *validationParams) Validate() error {
	if p.Value == "" {
		return errors.New("value is required")
	}
	return nil
}

var validationTestAction = Define(ActionDef[validationParams, string]{
	Name: "core_test_validate", Description: "test action",
	Handler: func(_ context.Context, _ *Core, p validationParams) (string, error) { return p.Value, nil },
})

func TestActionTypedAndDynamicInvocation(t *testing.T) {
	c := New(Config{})
	got, err := validationTestAction.Call(context.Background(), c, validationParams{Value: "ok"})
	if err != nil || got != "ok" {
		t.Fatalf("Call() = %q, %v", got, err)
	}
	gotAny, err := c.InvokeJSON(context.Background(), validationTestAction.Name(), []byte(`{"value":"json"}`))
	if err != nil || gotAny != "json" {
		t.Fatalf("InvokeJSON() = %v, %v", gotAny, err)
	}
	if _, err := c.InvokeJSON(context.Background(), validationTestAction.Name(), []byte(`{}`)); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestCatalogIsSorted(t *testing.T) {
	items := Catalog()
	for i := 1; i < len(items); i++ {
		if items[i-1].Name() > items[i].Name() {
			t.Fatalf("catalog is not sorted")
		}
	}
}
