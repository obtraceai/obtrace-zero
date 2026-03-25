package crd

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDeepCopyObjectNil(t *testing.T) {
	var instr *ObtraceInstrumentation
	if instr.DeepCopyObject() != nil {
		t.Error("expected nil for nil receiver")
	}
}

func TestDeepCopyObjectPreservesFields(t *testing.T) {
	orig := &ObtraceInstrumentation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: ObtraceInstrumentationSpec{
			APIKey:         "key123",
			IngestEndpoint: "https://ingest.test",
			Strategy:       StrategySDK,
			Namespaces:     []string{"ns1", "ns2"},
			ExcludeNames:   []string{"excluded"},
			LanguageHints:  map[string]Language{"myapp": LangNodeJS},
			ResourceAttrs:  map[string]string{"env": "prod"},
		},
	}

	copied := orig.DeepCopyObject().(*ObtraceInstrumentation)

	if copied.Name != "test" {
		t.Errorf("expected name test, got %s", copied.Name)
	}

	copied.Spec.Namespaces[0] = "modified"
	if orig.Spec.Namespaces[0] == "modified" {
		t.Error("modifying copy should not affect original (namespaces)")
	}

	copied.Spec.LanguageHints["new"] = LangPython
	if _, ok := orig.Spec.LanguageHints["new"]; ok {
		t.Error("modifying copy should not affect original (language hints)")
	}

	copied.Spec.ResourceAttrs["new"] = "val"
	if _, ok := orig.Spec.ResourceAttrs["new"]; ok {
		t.Error("modifying copy should not affect original (resource attrs)")
	}
}

func TestDeepCopyListNil(t *testing.T) {
	var list *ObtraceInstrumentationList
	if list.DeepCopyObject() != nil {
		t.Error("expected nil for nil receiver")
	}
}

func TestDeepCopyList(t *testing.T) {
	list := &ObtraceInstrumentationList{
		Items: []ObtraceInstrumentation{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "item1"},
				Spec: ObtraceInstrumentationSpec{
					Namespaces: []string{"ns1"},
				},
			},
		},
	}

	copied := list.DeepCopyObject().(*ObtraceInstrumentationList)

	if len(copied.Items) != 1 {
		t.Errorf("expected 1 item, got %d", len(copied.Items))
	}
	if copied.Items[0].Name != "item1" {
		t.Errorf("expected item1, got %s", copied.Items[0].Name)
	}
}
