package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	"github.com/obtraceai/obtrace-zero/pkg/detector"
	"github.com/obtraceai/obtrace-zero/pkg/injector"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type PodMutator struct {
	Client   client.Client
	Detector *detector.Detector
	Injector *injector.Injector
	decoder  admission.Decoder
}

func NewPodMutator(c client.Client) *PodMutator {
	return &PodMutator{
		Client:   c,
		Detector: detector.New(),
		Injector: injector.New(),
		decoder:  admission.NewDecoder(nil),
	}
}

func (m *PodMutator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := json.Unmarshal(req.Object.Raw, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	if m.isAlreadyInjected(pod) {
		return admission.Allowed("already instrumented")
	}

	if m.isExcluded(pod) {
		return admission.Allowed("excluded from instrumentation")
	}

	instrConfig, err := m.findInstrumentationConfig(ctx, req.Namespace)
	if err != nil || instrConfig == nil {
		return admission.Allowed("no ObtraceInstrumentation found for namespace")
	}

	detection := m.Detector.Detect(ctx, pod)

	if instrConfig.Spec.Strategy != "" && instrConfig.Spec.Strategy != crd.StrategyAuto {
		if instrConfig.Spec.Strategy != crd.StrategyDisable {
			detection.Strategy = instrConfig.Spec.Strategy
		} else {
			return admission.Allowed("instrumentation disabled")
		}
	}

	if hint, ok := instrConfig.Spec.LanguageHints[m.workloadName(pod)]; ok {
		detection.Language = hint
		if hint == crd.LangGo || hint == crd.LangRust {
			detection.Strategy = crd.StrategyEBPF
		} else {
			detection.Strategy = crd.StrategySDK
		}
	}

	serviceName := m.resolveServiceName(pod)
	environment := m.resolveEnvironment(pod, req.Namespace)

	injCfg := injector.InjectionConfig{
		APIKey:         instrConfig.Spec.APIKey,
		IngestEndpoint: instrConfig.Spec.IngestEndpoint,
		ServiceName:    serviceName,
		Environment:    environment,
		Sampling:       instrConfig.Spec.Sampling,
		ResourceAttrs:  instrConfig.Spec.ResourceAttrs,
	}

	mutated := m.Injector.Inject(pod, detection, injCfg)

	marshaledPod, err := json.Marshal(mutated)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (m *PodMutator) isAlreadyInjected(pod *corev1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}
	return pod.Annotations[injector.AnnotationInjected] == "true"
}

func (m *PodMutator) isExcluded(pod *corev1.Pod) bool {
	if pod.Annotations == nil {
		return false
	}
	if pod.Annotations["obtrace.io/exclude"] == "true" {
		return true
	}
	if pod.Labels != nil && pod.Labels["obtrace.io/exclude"] == "true" {
		return true
	}
	return false
}

func (m *PodMutator) findInstrumentationConfig(ctx context.Context, namespace string) (*crd.ObtraceInstrumentation, error) {
	list := &crd.ObtraceInstrumentationList{}
	if err := m.Client.List(ctx, list); err != nil {
		return nil, err
	}

	for _, instr := range list.Items {
		if len(instr.Spec.Namespaces) == 0 {
			return &instr, nil
		}
		for _, ns := range instr.Spec.Namespaces {
			if ns == namespace || ns == "*" {
				return &instr, nil
			}
		}
	}
	return nil, nil
}

func (m *PodMutator) resolveServiceName(pod *corev1.Pod) string {
	if name, ok := pod.Labels["app.kubernetes.io/name"]; ok {
		return name
	}
	if name, ok := pod.Labels["app"]; ok {
		return name
	}
	if pod.GenerateName != "" {
		return strings.TrimSuffix(pod.GenerateName, "-")
	}
	if pod.Name != "" {
		return pod.Name
	}
	return "unknown-service"
}

func (m *PodMutator) resolveEnvironment(pod *corev1.Pod, namespace string) string {
	if env, ok := pod.Labels["obtrace.io/environment"]; ok {
		return env
	}
	if env, ok := pod.Labels["app.kubernetes.io/environment"]; ok {
		return env
	}

	switch {
	case strings.Contains(namespace, "prod"):
		return "production"
	case strings.Contains(namespace, "stag"):
		return "staging"
	case strings.Contains(namespace, "dev"):
		return "development"
	default:
		return namespace
	}
}

func (m *PodMutator) workloadName(pod *corev1.Pod) string {
	if pod.GenerateName != "" {
		parts := strings.Split(strings.TrimSuffix(pod.GenerateName, "-"), "-")
		if len(parts) > 1 {
			return strings.Join(parts[:len(parts)-1], "-")
		}
		return parts[0]
	}
	return pod.Name
}

var _ admission.Handler = &PodMutator{}
var _ fmt.Stringer = (*PodMutator)(nil)

func (m *PodMutator) String() string {
	return "obtrace-zero-pod-mutator"
}
