package crd

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	GroupName = "obtrace.io"
	Version   = "v1alpha1"
)

type InstrumentationStrategy string

const (
	StrategyAuto    InstrumentationStrategy = "auto"
	StrategySDK     InstrumentationStrategy = "sdk"
	StrategyEBPF    InstrumentationStrategy = "ebpf"
	StrategyHybrid  InstrumentationStrategy = "hybrid"
	StrategyDisable InstrumentationStrategy = "disable"
)

type Language string

const (
	LangNodeJS Language = "nodejs"
	LangPython Language = "python"
	LangJava   Language = "java"
	LangGo     Language = "go"
	LangDotNet Language = "dotnet"
	LangPHP    Language = "php"
	LangRuby   Language = "ruby"
	LangRust   Language = "rust"
	LangUnknown Language = "unknown"
)

type ObtraceInstrumentation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ObtraceInstrumentationSpec   `json:"spec"`
	Status            ObtraceInstrumentationStatus `json:"status,omitempty"`
}

type ObtraceInstrumentationSpec struct {
	APIKey          string                  `json:"apiKey,omitempty"`
	APIKeySecretRef *SecretKeyRef           `json:"apiKeySecretRef,omitempty"`
	IngestEndpoint  string                  `json:"ingestEndpoint,omitempty"`
	Strategy        InstrumentationStrategy `json:"strategy,omitempty"`
	Selector        *metav1.LabelSelector   `json:"selector,omitempty"`
	Namespaces      []string                `json:"namespaces,omitempty"`
	ExcludeNames    []string                `json:"excludeNames,omitempty"`
	LanguageHints   map[string]Language     `json:"languageHints,omitempty"`
	Sampling        *SamplingConfig         `json:"sampling,omitempty"`
	Propagation     PropagationConfig       `json:"propagation,omitempty"`
	ResourceAttrs   map[string]string       `json:"resourceAttributes,omitempty"`
}

type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type SamplingConfig struct {
	TraceRatio  float64            `json:"traceRatio,omitempty"`
	RulesPerSvc map[string]float64 `json:"rulesPerService,omitempty"`
}

type PropagationConfig struct {
	InjectHeaders  bool `json:"injectHeaders,omitempty"`
	ExtractHeaders bool `json:"extractHeaders,omitempty"`
}

type ObtraceInstrumentationStatus struct {
	Phase             string                  `json:"phase,omitempty"`
	InstrumentedPods  int                     `json:"instrumentedPods,omitempty"`
	DiscoveredWorkloads []DiscoveredWorkload  `json:"discoveredWorkloads,omitempty"`
	Conditions        []metav1.Condition      `json:"conditions,omitempty"`
}

type DiscoveredWorkload struct {
	Name           string                  `json:"name"`
	Namespace      string                  `json:"namespace"`
	Kind           string                  `json:"kind"`
	Language       Language                `json:"language"`
	Framework      string                  `json:"framework,omitempty"`
	Strategy       InstrumentationStrategy `json:"strategy"`
	Instrumented   bool                    `json:"instrumented"`
	LastDetectedAt metav1.Time             `json:"lastDetectedAt"`
}

type ObtraceInstrumentationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ObtraceInstrumentation `json:"items"`
}

func (in *ObtraceInstrumentation) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(ObtraceInstrumentation)
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.TypeMeta = out.TypeMeta
	out.Spec = in.Spec
	out.Status = in.Status
	if in.Spec.Namespaces != nil {
		out.Spec.Namespaces = make([]string, len(in.Spec.Namespaces))
		copy(out.Spec.Namespaces, in.Spec.Namespaces)
	}
	if in.Spec.ExcludeNames != nil {
		out.Spec.ExcludeNames = make([]string, len(in.Spec.ExcludeNames))
		copy(out.Spec.ExcludeNames, in.Spec.ExcludeNames)
	}
	if in.Spec.LanguageHints != nil {
		out.Spec.LanguageHints = make(map[string]Language, len(in.Spec.LanguageHints))
		for k, v := range in.Spec.LanguageHints {
			out.Spec.LanguageHints[k] = v
		}
	}
	if in.Spec.ResourceAttrs != nil {
		out.Spec.ResourceAttrs = make(map[string]string, len(in.Spec.ResourceAttrs))
		for k, v := range in.Spec.ResourceAttrs {
			out.Spec.ResourceAttrs[k] = v
		}
	}
	return out
}

func (in *ObtraceInstrumentationList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	out := new(ObtraceInstrumentationList)
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ObtraceInstrumentation, len(in.Items))
		for i := range in.Items {
			out.Items[i] = *in.Items[i].DeepCopyObject().(*ObtraceInstrumentation)
		}
	}
	return out
}
