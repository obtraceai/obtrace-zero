package injector

import (
	"fmt"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	"github.com/obtraceai/obtrace-zero/pkg/detector"
	corev1 "k8s.io/api/core/v1"
)

const (
	AgentVolumeName = "obtrace-agent"
	AgentMountPath  = "/obtrace"
	AgentImage      = "ghcr.io/obtrace/obtrace-zero-agent"
	EBPFImage       = "ghcr.io/obtrace/obtrace-zero-ebpf"
	AnnotationInjected = "obtrace.io/injected"
	AnnotationLanguage = "obtrace.io/detected-language"
	AnnotationStrategy = "obtrace.io/strategy"
	AnnotationFramework = "obtrace.io/detected-framework"
	LabelInstrumented  = "obtrace.io/instrumented"
)

type InjectionConfig struct {
	APIKey         string
	IngestEndpoint string
	ServiceName    string
	Environment    string
	AgentImageTag  string
	Sampling       *crd.SamplingConfig
	ResourceAttrs  map[string]string
}

type Injector struct{}

func New() *Injector {
	return &Injector{}
}

func (inj *Injector) Inject(pod *corev1.Pod, detection detector.DetectionResult, cfg InjectionConfig) *corev1.Pod {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	pod.Annotations[AnnotationInjected] = "true"
	pod.Annotations[AnnotationLanguage] = string(detection.Language)
	pod.Annotations[AnnotationStrategy] = string(detection.Strategy)
	if detection.Framework != "" {
		pod.Annotations[AnnotationFramework] = detection.Framework
	}
	pod.Labels[LabelInstrumented] = "true"

	baseEnv := inj.buildBaseEnv(cfg, detection)

	switch detection.Strategy {
	case crd.StrategySDK:
		inj.injectSDK(pod, detection, cfg, baseEnv)
	case crd.StrategyEBPF:
		inj.injectEBPF(pod, cfg, baseEnv)
	case crd.StrategyHybrid:
		inj.injectSDK(pod, detection, cfg, baseEnv)
		inj.injectEBPF(pod, cfg, baseEnv)
	}

	return pod
}

func (inj *Injector) injectSDK(pod *corev1.Pod, detection detector.DetectionResult, cfg InjectionConfig, baseEnv []corev1.EnvVar) {
	agentVol := corev1.Volume{
		Name: AgentVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, agentVol)

	tag := cfg.AgentImageTag
	if tag == "" {
		tag = "latest"
	}

	initContainer := corev1.Container{
		Name:  "obtrace-agent-init",
		Image: fmt.Sprintf("%s:%s", AgentImage, tag),
		Command: []string{"/bin/sh", "-c",
			fmt.Sprintf("cp -r /agent/%s/* %s/ 2>/dev/null || true", detection.Language, AgentMountPath),
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: AgentVolumeName, MountPath: AgentMountPath},
		},
	}
	pod.Spec.InitContainers = append(pod.Spec.InitContainers, initContainer)

	langEnv := inj.languageSpecificEnv(detection)
	allEnv := append(baseEnv, langEnv...)

	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].VolumeMounts = append(pod.Spec.Containers[i].VolumeMounts,
			corev1.VolumeMount{Name: AgentVolumeName, MountPath: AgentMountPath, ReadOnly: true},
		)
		pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, allEnv...)
	}
}

func (inj *Injector) injectEBPF(pod *corev1.Pod, cfg InjectionConfig, baseEnv []corev1.EnvVar) {
	tag := cfg.AgentImageTag
	if tag == "" {
		tag = "latest"
	}

	sidecar := corev1.Container{
		Name:  "obtrace-ebpf",
		Image: fmt.Sprintf("%s:%s", EBPFImage, tag),
		Env:   baseEnv,
		SecurityContext: &corev1.SecurityContext{
			Privileged: boolPtr(false),
			Capabilities: &corev1.Capabilities{
				Add: []corev1.Capability{
					"BPF",
					"NET_ADMIN",
					"SYS_PTRACE",
					"PERFMON",
				},
			},
		},
		Resources: ebpfResources(),
	}

	pod.Spec.Containers = append(pod.Spec.Containers, sidecar)
	pod.Spec.ShareProcessNamespace = boolPtr(true)
}

func (inj *Injector) buildBaseEnv(cfg InjectionConfig, detection detector.DetectionResult) []corev1.EnvVar {
	envs := []corev1.EnvVar{
		{Name: "OBTRACE_API_KEY", Value: cfg.APIKey},
		{Name: "OBTRACE_INGEST_URL", Value: cfg.IngestEndpoint},
		{Name: "OBTRACE_SERVICE_NAME", Value: cfg.ServiceName},
		{Name: "OBTRACE_ENVIRONMENT", Value: cfg.Environment},
		{Name: "OBTRACE_LANGUAGE", Value: string(detection.Language)},
		{Name: "OBTRACE_AGENT_PATH", Value: AgentMountPath},
		{Name: "OBTRACE_POD_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
		}},
		{Name: "OBTRACE_POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
		}},
		{Name: "OBTRACE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
		}},
	}

	if cfg.Sampling != nil && cfg.Sampling.TraceRatio > 0 {
		envs = append(envs, corev1.EnvVar{
			Name:  "OBTRACE_TRACE_SAMPLE_RATIO",
			Value: fmt.Sprintf("%.4f", cfg.Sampling.TraceRatio),
		})
	}

	for k, v := range cfg.ResourceAttrs {
		envs = append(envs, corev1.EnvVar{
			Name:  fmt.Sprintf("OBTRACE_ATTR_%s", k),
			Value: v,
		})
	}

	return envs
}

func (inj *Injector) languageSpecificEnv(detection detector.DetectionResult) []corev1.EnvVar {
	switch detection.Language {
	case crd.LangNodeJS:
		return []corev1.EnvVar{
			{Name: "NODE_OPTIONS", Value: fmt.Sprintf("--require %s/obtrace-loader.js", AgentMountPath)},
		}
	case crd.LangPython:
		return []corev1.EnvVar{
			{Name: "PYTHONPATH", Value: fmt.Sprintf("%s:${PYTHONPATH}", AgentMountPath)},
			{Name: "OBTRACE_PYTHON_AUTOINSTRUMENT", Value: "1"},
			{Name: "PYTHONSTARTUP", Value: fmt.Sprintf("%s/obtrace_loader.py", AgentMountPath)},
		}
	case crd.LangJava:
		return []corev1.EnvVar{
			{Name: "JAVA_TOOL_OPTIONS", Value: fmt.Sprintf("-javaagent:%s/obtrace-agent.jar", AgentMountPath)},
		}
	case crd.LangDotNet:
		return []corev1.EnvVar{
			{Name: "DOTNET_STARTUP_HOOKS", Value: fmt.Sprintf("%s/Obtrace.AutoInstrument.dll", AgentMountPath)},
			{Name: "DOTNET_ADDITIONAL_DEPS", Value: fmt.Sprintf("%s/additionalDeps", AgentMountPath)},
		}
	case crd.LangPHP:
		return []corev1.EnvVar{
			{Name: "PHP_INI_SCAN_DIR", Value: fmt.Sprintf("%s/php.d/:%s", AgentMountPath, "${PHP_INI_SCAN_DIR}")},
		}
	case crd.LangRuby:
		return []corev1.EnvVar{
			{Name: "RUBYOPT", Value: fmt.Sprintf("-r %s/obtrace_loader", AgentMountPath)},
		}
	default:
		return nil
	}
}

func boolPtr(b bool) *bool { return &b }

