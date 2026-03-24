package detector

import (
	"context"
	"strings"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	corev1 "k8s.io/api/core/v1"
)

type DetectionResult struct {
	Language  crd.Language
	Framework string
	Strategy  crd.InstrumentationStrategy
	EnvVars   map[string]string
	Confidence float64
}

type Detector struct{}

func New() *Detector {
	return &Detector{}
}

func (d *Detector) Detect(_ context.Context, pod *corev1.Pod) DetectionResult {
	for _, c := range pod.Spec.Containers {
		if r := d.detectFromContainer(c); r.Language != crd.LangUnknown {
			return r
		}
	}
	if r := d.detectFromLabels(pod.Labels); r.Language != crd.LangUnknown {
		return r
	}
	return DetectionResult{
		Language:  crd.LangUnknown,
		Strategy:  crd.StrategyEBPF,
		Confidence: 0.3,
	}
}

func (d *Detector) detectFromContainer(c corev1.Container) DetectionResult {
	image := strings.ToLower(c.Image)
	cmd := strings.Join(append(c.Command, c.Args...), " ")
	cmdLower := strings.ToLower(cmd)

	for _, env := range c.Env {
		if env.Name == "OBTRACE_LANGUAGE" {
			return d.resultFromHint(crd.Language(env.Value))
		}
	}

	if d.matchAny(image, "node", "bun", "deno") || d.matchAny(cmdLower, "node ", "bun ", "npm ", "npx ", "next ", "nest ") {
		fw := d.detectNodeFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangNodeJS, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "python", "fastapi", "flask", "django", "uvicorn", "gunicorn") || d.matchAny(cmdLower, "python ", "uvicorn ", "gunicorn ", "flask ", "django") {
		fw := d.detectPythonFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangPython, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "openjdk", "eclipse-temurin", "amazoncorretto", "maven", "gradle", "spring") || d.matchAny(cmdLower, "java ", "mvn ", "gradle") {
		fw := d.detectJavaFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangJava, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "golang", "gcr.io/distroless") || d.matchAny(cmdLower, "/app ", "/server ", "/main ") {
		if d.matchAny(image, "golang") {
			return DetectionResult{Language: crd.LangGo, Framework: "", Strategy: crd.StrategyEBPF, Confidence: 0.8}
		}
	}
	if d.matchAny(image, "mcr.microsoft.com/dotnet", "aspnet") || d.matchAny(cmdLower, "dotnet ") {
		fw := d.detectDotNetFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangDotNet, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "php", "laravel", "symfony", "wordpress") || d.matchAny(cmdLower, "php ", "php-fpm", "artisan") {
		fw := d.detectPHPFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangPHP, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "ruby", "rails", "puma", "unicorn", "sidekiq") || d.matchAny(cmdLower, "ruby ", "rails ", "puma ", "bundle ") {
		fw := d.detectRubyFramework(image, cmdLower)
		return DetectionResult{Language: crd.LangRuby, Framework: fw, Strategy: crd.StrategySDK, Confidence: 0.9}
	}
	if d.matchAny(image, "rust") {
		return DetectionResult{Language: crd.LangRust, Framework: "", Strategy: crd.StrategyEBPF, Confidence: 0.7}
	}

	return DetectionResult{Language: crd.LangUnknown, Strategy: crd.StrategyEBPF, Confidence: 0.3}
}

func (d *Detector) detectFromLabels(labels map[string]string) DetectionResult {
	if lang, ok := labels["obtrace.io/language"]; ok {
		return d.resultFromHint(crd.Language(lang))
	}
	if _, ok := labels["obtrace.io/instrument"]; ok {
		return DetectionResult{Language: crd.LangUnknown, Strategy: crd.StrategyEBPF, Confidence: 0.5}
	}
	return DetectionResult{Language: crd.LangUnknown}
}

func (d *Detector) resultFromHint(lang crd.Language) DetectionResult {
	strategy := crd.StrategySDK
	if lang == crd.LangGo || lang == crd.LangRust || lang == crd.LangUnknown {
		strategy = crd.StrategyEBPF
	}
	return DetectionResult{Language: lang, Strategy: strategy, Confidence: 1.0}
}

func (d *Detector) detectNodeFramework(image, cmd string) string {
	if d.matchAny(cmd, "next") || d.matchAny(image, "next") { return "nextjs" }
	if d.matchAny(cmd, "nest") || d.matchAny(image, "nest") { return "nestjs" }
	if d.matchAny(cmd, "express") { return "express" }
	if d.matchAny(cmd, "elysia", "bun") { return "elysia" }
	return ""
}

func (d *Detector) detectPythonFramework(image, cmd string) string {
	if d.matchAny(cmd, "uvicorn", "fastapi") || d.matchAny(image, "fastapi") { return "fastapi" }
	if d.matchAny(cmd, "flask") || d.matchAny(image, "flask") { return "flask" }
	if d.matchAny(cmd, "django") || d.matchAny(image, "django") { return "django" }
	return ""
}

func (d *Detector) detectJavaFramework(image, cmd string) string {
	if d.matchAny(image, "spring") || d.matchAny(cmd, "spring") { return "spring" }
	if d.matchAny(cmd, "quarkus") { return "quarkus" }
	if d.matchAny(cmd, "micronaut") { return "micronaut" }
	return ""
}

func (d *Detector) detectDotNetFramework(image, cmd string) string {
	if d.matchAny(image, "aspnet") { return "aspnet" }
	return ""
}

func (d *Detector) detectPHPFramework(image, cmd string) string {
	if d.matchAny(image, "laravel") || d.matchAny(cmd, "artisan") { return "laravel" }
	if d.matchAny(image, "symfony") { return "symfony" }
	if d.matchAny(image, "wordpress") { return "wordpress" }
	return ""
}

func (d *Detector) detectRubyFramework(image, cmd string) string {
	if d.matchAny(image, "rails") || d.matchAny(cmd, "rails") { return "rails" }
	if d.matchAny(cmd, "sidekiq") { return "sidekiq" }
	return ""
}

func (d *Detector) matchAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
