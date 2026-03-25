package injector

import (
	"testing"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	"github.com/obtraceai/obtrace-zero/pkg/detector"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func basePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "myapp:latest"},
			},
		},
	}
}

func baseConfig() InjectionConfig {
	return InjectionConfig{
		APIKey:         "obt_test_key",
		IngestEndpoint: "https://ingest.test:4317",
		ServiceName:    "test-service",
		Environment:    "test",
	}
}

func TestInjectSDK(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangNodeJS,
		Strategy: crd.StrategySDK,
	}

	result := inj.Inject(pod, det, baseConfig())

	if result.Annotations[AnnotationInjected] != "true" {
		t.Error("expected injected annotation")
	}
	if result.Annotations[AnnotationLanguage] != "nodejs" {
		t.Errorf("expected nodejs language annotation, got %s", result.Annotations[AnnotationLanguage])
	}
	if result.Labels[LabelInstrumented] != "true" {
		t.Error("expected instrumented label")
	}
	if len(result.Spec.InitContainers) != 1 {
		t.Errorf("expected 1 init container, got %d", len(result.Spec.InitContainers))
	}
	if result.Spec.InitContainers[0].Name != "obtrace-agent-init" {
		t.Errorf("expected obtrace-agent-init, got %s", result.Spec.InitContainers[0].Name)
	}
	if len(result.Spec.Volumes) != 1 {
		t.Errorf("expected 1 volume, got %d", len(result.Spec.Volumes))
	}

	foundNodeOptions := false
	for _, env := range result.Spec.Containers[0].Env {
		if env.Name == "NODE_OPTIONS" {
			foundNodeOptions = true
		}
	}
	if !foundNodeOptions {
		t.Error("expected NODE_OPTIONS env var for nodejs SDK injection")
	}
}

func TestInjectEBPF(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangGo,
		Strategy: crd.StrategyEBPF,
	}

	result := inj.Inject(pod, det, baseConfig())

	if len(result.Spec.Containers) != 2 {
		t.Errorf("expected 2 containers (app + ebpf sidecar), got %d", len(result.Spec.Containers))
	}

	sidecar := result.Spec.Containers[1]
	if sidecar.Name != "obtrace-ebpf" {
		t.Errorf("expected obtrace-ebpf sidecar, got %s", sidecar.Name)
	}
	if sidecar.SecurityContext == nil || sidecar.SecurityContext.Capabilities == nil {
		t.Fatal("expected security context with capabilities")
	}

	hasBPF := false
	for _, cap := range sidecar.SecurityContext.Capabilities.Add {
		if cap == "BPF" {
			hasBPF = true
		}
	}
	if !hasBPF {
		t.Error("expected BPF capability on ebpf sidecar")
	}

	if result.Spec.ShareProcessNamespace == nil || !*result.Spec.ShareProcessNamespace {
		t.Error("expected ShareProcessNamespace to be true for ebpf")
	}
}

func TestInjectHybrid(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangNodeJS,
		Strategy: crd.StrategyHybrid,
	}

	result := inj.Inject(pod, det, baseConfig())

	if len(result.Spec.InitContainers) != 1 {
		t.Errorf("expected init container for SDK part of hybrid, got %d", len(result.Spec.InitContainers))
	}
	if len(result.Spec.Containers) != 2 {
		t.Errorf("expected 2 containers (app + ebpf sidecar) for hybrid, got %d", len(result.Spec.Containers))
	}
}

func TestInjectSetsBaseEnvVars(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangPython,
		Strategy: crd.StrategySDK,
	}

	result := inj.Inject(pod, det, baseConfig())

	envMap := map[string]corev1.EnvVar{}
	for _, e := range result.Spec.Containers[0].Env {
		envMap[e.Name] = e
	}

	requiredEnvs := []string{
		"OBTRACE_API_KEY", "OBTRACE_INGEST_URL", "OBTRACE_SERVICE_NAME",
		"OBTRACE_ENVIRONMENT", "OBTRACE_LANGUAGE", "OBTRACE_AGENT_PATH",
		"OBTRACE_POD_NAME", "OBTRACE_POD_NAMESPACE", "OBTRACE_NODE_NAME",
	}
	for _, name := range requiredEnvs {
		if _, ok := envMap[name]; !ok {
			t.Errorf("expected env var %s to be set", name)
		}
	}

	if envMap["OBTRACE_API_KEY"].Value != "obt_test_key" {
		t.Errorf("expected api key obt_test_key, got %s", envMap["OBTRACE_API_KEY"].Value)
	}
}

func TestInjectWithSamplingConfig(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangNodeJS,
		Strategy: crd.StrategySDK,
	}
	cfg := baseConfig()
	cfg.Sampling = &crd.SamplingConfig{TraceRatio: 0.5}

	result := inj.Inject(pod, det, cfg)

	found := false
	for _, e := range result.Spec.Containers[0].Env {
		if e.Name == "OBTRACE_TRACE_SAMPLE_RATIO" {
			found = true
			if e.Value != "0.5000" {
				t.Errorf("expected 0.5000, got %s", e.Value)
			}
		}
	}
	if !found {
		t.Error("expected OBTRACE_TRACE_SAMPLE_RATIO env var")
	}
}

func TestInjectNilAnnotationsAndLabels(t *testing.T) {
	inj := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "myapp:latest"},
			},
		},
	}
	det := detector.DetectionResult{
		Language: crd.LangNodeJS,
		Strategy: crd.StrategySDK,
	}

	result := inj.Inject(pod, det, baseConfig())

	if result.Annotations == nil {
		t.Error("expected annotations to be initialized")
	}
	if result.Labels == nil {
		t.Error("expected labels to be initialized")
	}
}

func TestInjectJavaAgent(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangJava,
		Strategy: crd.StrategySDK,
	}

	result := inj.Inject(pod, det, baseConfig())

	found := false
	for _, e := range result.Spec.Containers[0].Env {
		if e.Name == "JAVA_TOOL_OPTIONS" {
			found = true
		}
	}
	if !found {
		t.Error("expected JAVA_TOOL_OPTIONS for java SDK injection")
	}
}

func TestInjectCustomImageTag(t *testing.T) {
	inj := New()
	pod := basePod()
	det := detector.DetectionResult{
		Language: crd.LangNodeJS,
		Strategy: crd.StrategySDK,
	}
	cfg := baseConfig()
	cfg.AgentImageTag = "v2.0.0"

	result := inj.Inject(pod, det, cfg)

	initImage := result.Spec.InitContainers[0].Image
	expected := AgentImage + ":v2.0.0"
	if initImage != expected {
		t.Errorf("expected image %s, got %s", expected, initImage)
	}
}
