package detector

import (
	"context"
	"testing"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDetectNodeJS(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "node:20-alpine", Command: []string{"node", "server.js"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangNodeJS {
		t.Errorf("expected nodejs, got %s", r.Language)
	}
	if r.Strategy != crd.StrategySDK {
		t.Errorf("expected sdk strategy, got %s", r.Strategy)
	}
}

func TestDetectPython(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "python:3.12", Command: []string{"uvicorn", "main:app"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangPython {
		t.Errorf("expected python, got %s", r.Language)
	}
	if r.Framework != "fastapi" {
		t.Errorf("expected fastapi framework, got %s", r.Framework)
	}
}

func TestDetectJava(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "eclipse-temurin:21", Command: []string{"java", "-jar", "app.jar"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangJava {
		t.Errorf("expected java, got %s", r.Language)
	}
	if r.Strategy != crd.StrategySDK {
		t.Errorf("expected sdk strategy, got %s", r.Strategy)
	}
}

func TestDetectGo(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "golang:1.24"},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangGo {
		t.Errorf("expected go, got %s", r.Language)
	}
	if r.Strategy != crd.StrategyEBPF {
		t.Errorf("expected ebpf strategy, got %s", r.Strategy)
	}
}

func TestDetectDotNet(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "mcr.microsoft.com/dotnet/aspnet:8.0", Command: []string{"dotnet", "MyApp.dll"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangDotNet {
		t.Errorf("expected dotnet, got %s", r.Language)
	}
}

func TestDetectPHP(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "php:8.3-fpm"},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangPHP {
		t.Errorf("expected php, got %s", r.Language)
	}
}

func TestDetectRuby(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "ruby:3.3", Command: []string{"rails", "server"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangRuby {
		t.Errorf("expected ruby, got %s", r.Language)
	}
	if r.Framework != "rails" {
		t.Errorf("expected rails framework, got %s", r.Framework)
	}
}

func TestDetectRust(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "rust:1.77"},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangRust {
		t.Errorf("expected rust, got %s", r.Language)
	}
	if r.Strategy != crd.StrategyEBPF {
		t.Errorf("expected ebpf strategy, got %s", r.Strategy)
	}
}

func TestDetectUnknown(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "nginx:latest"},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangUnknown {
		t.Errorf("expected unknown, got %s", r.Language)
	}
	if r.Strategy != crd.StrategyEBPF {
		t.Errorf("expected ebpf fallback, got %s", r.Strategy)
	}
}

func TestDetectFromEnvHint(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image: "custom-image:latest",
					Env: []corev1.EnvVar{
						{Name: "OBTRACE_LANGUAGE", Value: "python"},
					},
				},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangPython {
		t.Errorf("expected python from env hint, got %s", r.Language)
	}
	if r.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0 for env hint, got %.1f", r.Confidence)
	}
}

func TestDetectFromLabel(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"obtrace.io/language": "java",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "custom-image:latest"},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangJava {
		t.Errorf("expected java from label, got %s", r.Language)
	}
}

func TestDetectNextJSFramework(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "node:20", Command: []string{"next", "start"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangNodeJS {
		t.Errorf("expected nodejs, got %s", r.Language)
	}
	if r.Framework != "nextjs" {
		t.Errorf("expected nextjs framework, got %s", r.Framework)
	}
}

func TestDetectSpringFramework(t *testing.T) {
	d := New()
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Image: "eclipse-temurin:21", Command: []string{"java", "-jar", "spring-app.jar"}},
			},
		},
	}
	r := d.Detect(context.Background(), pod)
	if r.Language != crd.LangJava {
		t.Errorf("expected java, got %s", r.Language)
	}
	if r.Framework != "spring" {
		t.Errorf("expected spring framework, got %s", r.Framework)
	}
}
