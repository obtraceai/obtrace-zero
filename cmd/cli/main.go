package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
)

const version = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "install":
		cmdInstall(os.Args[2:])
	case "uninstall":
		cmdUninstall()
	case "status":
		cmdStatus()
	case "discover":
		cmdDiscover(os.Args[2:])
	case "instrument":
		cmdInstrument(os.Args[2:])
	case "version":
		fmt.Printf("obtrace-zero v%s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`obtrace-zero — Zero-touch observability for Kubernetes

Usage:
  obtrace-zero <command> [flags]

Commands:
  install      Install obtrace-zero operator into the cluster
  uninstall    Remove obtrace-zero from the cluster
  status       Show instrumentation status across the cluster
  discover     Scan cluster and show what would be instrumented
  instrument   Create ObtraceInstrumentation resource for a namespace
  version      Print version

Install:
  obtrace-zero install --api-key=obt_live_xxx
  obtrace-zero install --api-key=obt_live_xxx --ingest=https://ingest.obtrace.io
  obtrace-zero install --api-key=obt_live_xxx --namespaces=default,production
  obtrace-zero install --api-key=obt_live_xxx --strategy=hybrid

Discover (dry-run):
  obtrace-zero discover
  obtrace-zero discover --namespace=production
  obtrace-zero discover --output=json

Instrument:
  obtrace-zero instrument --namespace=production --api-key=obt_live_xxx
  obtrace-zero instrument --namespace=default --strategy=ebpf`)
}

func cmdInstall(args []string) {
	flags := parseFlags(args)

	apiKey := flags["api-key"]
	if apiKey == "" {
		apiKey = os.Getenv("OBTRACE_API_KEY")
	}
	if apiKey == "" {
		fatal("--api-key is required (or set OBTRACE_API_KEY)")
	}

	ingestURL := flags["ingest"]
	if ingestURL == "" {
		ingestURL = os.Getenv("OBTRACE_INGEST_URL")
	}
	if ingestURL == "" {
		ingestURL = "https://ingest-edge.obtrace.svc.cluster.local:8080"
	}

	strategy := flags["strategy"]
	if strategy == "" {
		strategy = "auto"
	}

	namespaces := flags["namespaces"]
	imageTag := flags["image-tag"]
	if imageTag == "" {
		imageTag = version
	}

	fmt.Println("🔍 Checking cluster connectivity...")
	if err := helmCheck(); err != nil {
		fatal("helm not found or cluster unreachable: %v", err)
	}

	fmt.Println("📦 Installing obtrace-zero operator...")
	helmArgs := []string{
		"upgrade", "--install", "obtrace-zero",
		"oci://ghcr.io/obtrace/charts/obtrace-zero",
		"--namespace", "obtrace-system",
		"--create-namespace",
		"--set", fmt.Sprintf("operator.image.tag=%s", imageTag),
		"--set", fmt.Sprintf("config.apiKey=%s", apiKey),
		"--set", fmt.Sprintf("config.ingestEndpoint=%s", ingestURL),
		"--set", fmt.Sprintf("config.strategy=%s", strategy),
	}

	if namespaces != "" {
		helmArgs = append(helmArgs, "--set", fmt.Sprintf("config.namespaces={%s}", namespaces))
	}

	cmd := exec.Command("helm", helmArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("\n⚠️  Helm install failed. Falling back to kubectl apply...")
		kubectlInstall(apiKey, ingestURL, strategy, namespaces, imageTag)
		return
	}

	fmt.Println("\n✅ obtrace-zero installed successfully!")
	fmt.Println("   Operator is now watching for pods and auto-instrumenting.")
	fmt.Printf("   Strategy: %s\n", strategy)
	fmt.Printf("   Ingest: %s\n", ingestURL)
	if namespaces != "" {
		fmt.Printf("   Namespaces: %s\n", namespaces)
	} else {
		fmt.Println("   Namespaces: all (cluster-wide)")
	}
	fmt.Println("\n   Run 'obtrace-zero status' to check instrumentation state.")
}

func kubectlInstall(apiKey, ingestURL, strategy, namespaces, imageTag string) {
	manifest := generateManifest(apiKey, ingestURL, strategy, namespaces, imageTag)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("kubectl apply failed: %v", err)
	}

	fmt.Println("\n✅ obtrace-zero installed via kubectl!")
}

func cmdUninstall() {
	fmt.Println("🗑️  Removing obtrace-zero...")
	cmd := exec.Command("helm", "uninstall", "obtrace-zero", "--namespace", "obtrace-system")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cmd2 := exec.Command("kubectl", "delete", "namespace", "obtrace-system")
		cmd2.Stdout = os.Stdout
		cmd2.Stderr = os.Stderr
		cmd2.Run()
	}
	fmt.Println("✅ obtrace-zero removed.")
}

func cmdStatus() {
	fmt.Println("obtrace-zero status\n")

	out, err := exec.Command("kubectl", "get", "pods", "-n", "obtrace-system", "-o", "wide").Output()
	if err != nil {
		fatal("cannot reach cluster: %v", err)
	}
	fmt.Println("Operator:")
	fmt.Println(string(out))

	out, err = exec.Command("kubectl", "get", "pods", "--all-namespaces",
		"-l", "obtrace.io/instrumented=true",
		"-o", "custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,LANGUAGE:.metadata.annotations.obtrace\\.io/detected-language,STRATEGY:.metadata.annotations.obtrace\\.io/strategy,FRAMEWORK:.metadata.annotations.obtrace\\.io/detected-framework",
	).Output()
	if err == nil && len(out) > 0 {
		fmt.Println("Instrumented Pods:")
		fmt.Println(string(out))
	} else {
		fmt.Println("No instrumented pods found yet.")
	}
}

func cmdDiscover(args []string) {
	flags := parseFlags(args)
	namespace := flags["namespace"]
	outputFmt := flags["output"]

	nsFlag := "--all-namespaces"
	if namespace != "" {
		nsFlag = fmt.Sprintf("--namespace=%s", namespace)
	}

	out, err := exec.Command("kubectl", "get", "deployments,statefulsets,daemonsets",
		nsFlag, "-o", "json").Output()
	if err != nil {
		fatal("cannot list workloads: %v", err)
	}

	var result struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image   string   `json:"image"`
							Command []string `json:"command"`
							Args    []string `json:"args"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		fatal("cannot parse workload list: %v", err)
	}

	type discRow struct {
		Namespace string       `json:"namespace"`
		Name      string       `json:"name"`
		Kind      string       `json:"kind"`
		Image     string       `json:"image"`
		Language  crd.Language `json:"language"`
		Framework string       `json:"framework"`
		Strategy  string       `json:"strategy"`
	}

	var rows []discRow
	for _, item := range result.Items {
		if isSystemNS(item.Metadata.Namespace) {
			continue
		}
		image := ""
		if len(item.Spec.Template.Spec.Containers) > 0 {
			image = item.Spec.Template.Spec.Containers[0].Image
		}
		lang, fw, strat := detectFromImage(image)
		rows = append(rows, discRow{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Kind:      item.Kind,
			Image:     image,
			Language:  lang,
			Framework: fw,
			Strategy:  strat,
		})
	}

	if outputFmt == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rows)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAMESPACE\tNAME\tKIND\tLANGUAGE\tFRAMEWORK\tSTRATEGY\tIMAGE\n")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Namespace, r.Name, r.Kind, r.Language, r.Framework, r.Strategy, truncate(r.Image, 50))
	}
	w.Flush()

	fmt.Printf("\n%d workloads discovered. Run 'obtrace-zero install --api-key=xxx' to instrument them.\n", len(rows))
}

func cmdInstrument(args []string) {
	flags := parseFlags(args)

	ns := flags["namespace"]
	if ns == "" {
		fatal("--namespace is required")
	}
	apiKey := flags["api-key"]
	if apiKey == "" {
		apiKey = os.Getenv("OBTRACE_API_KEY")
	}
	if apiKey == "" {
		fatal("--api-key is required")
	}
	strategy := flags["strategy"]
	if strategy == "" {
		strategy = "auto"
	}
	ingest := flags["ingest"]
	if ingest == "" {
		ingest = "https://ingest-edge.obtrace.svc.cluster.local:8080"
	}

	instrYAML := fmt.Sprintf(`apiVersion: obtrace.io/v1alpha1
kind: ObtraceInstrumentation
metadata:
  name: obtrace-%s
  namespace: obtrace-system
spec:
  apiKey: "%s"
  ingestEndpoint: "%s"
  strategy: "%s"
  namespaces:
    - "%s"
  propagation:
    injectHeaders: true
    extractHeaders: true
`, ns, apiKey, ingest, strategy, ns)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(instrYAML)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("failed to create instrumentation: %v", err)
	}

	fmt.Printf("✅ Namespace '%s' is now instrumented (strategy: %s)\n", ns, strategy)
	fmt.Println("   Existing pods will be instrumented on next restart.")
	fmt.Println("   New pods will be auto-instrumented on creation.")
}

func detectFromImage(image string) (crd.Language, string, string) {
	img := strings.ToLower(image)
	switch {
	case strings.Contains(img, "node") || strings.Contains(img, "bun") || strings.Contains(img, "next"):
		fw := ""
		if strings.Contains(img, "next") { fw = "nextjs" }
		return crd.LangNodeJS, fw, "sdk"
	case strings.Contains(img, "python") || strings.Contains(img, "fastapi") || strings.Contains(img, "django") || strings.Contains(img, "flask"):
		fw := ""
		if strings.Contains(img, "fastapi") { fw = "fastapi" }
		if strings.Contains(img, "django") { fw = "django" }
		if strings.Contains(img, "flask") { fw = "flask" }
		return crd.LangPython, fw, "sdk"
	case strings.Contains(img, "openjdk") || strings.Contains(img, "temurin") || strings.Contains(img, "corretto") || strings.Contains(img, "spring"):
		fw := ""
		if strings.Contains(img, "spring") { fw = "spring" }
		return crd.LangJava, fw, "sdk"
	case strings.Contains(img, "dotnet") || strings.Contains(img, "aspnet"):
		return crd.LangDotNet, "", "sdk"
	case strings.Contains(img, "php") || strings.Contains(img, "laravel"):
		return crd.LangPHP, "", "sdk"
	case strings.Contains(img, "ruby") || strings.Contains(img, "rails"):
		return crd.LangRuby, "", "sdk"
	case strings.Contains(img, "golang") || strings.Contains(img, "rust"):
		return crd.LangGo, "", "ebpf"
	default:
		return crd.LangUnknown, "", "ebpf"
	}
}

func isSystemNS(ns string) bool {
	sys := map[string]bool{
		"kube-system": true, "kube-public": true, "kube-node-lease": true,
		"cert-manager": true, "linkerd": true, "linkerd-viz": true,
		"argocd": true, "obtrace-system": true, "obtrace-infra": true,
	}
	return sys[ns]
}

func parseFlags(args []string) map[string]string {
	flags := map[string]string{}
	for _, a := range args {
		if strings.HasPrefix(a, "--") {
			parts := strings.SplitN(strings.TrimPrefix(a, "--"), "=", 2)
			if len(parts) == 2 {
				flags[parts[0]] = parts[1]
			} else {
				flags[parts[0]] = "true"
			}
		}
	}
	return flags
}

func helmCheck() error {
	return exec.Command("helm", "version", "--short").Run()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func generateManifest(apiKey, ingestURL, strategy, namespaces, imageTag string) string {
	nsYAML := ""
	if namespaces != "" {
		for _, ns := range strings.Split(namespaces, ",") {
			nsYAML += fmt.Sprintf("\n    - \"%s\"", strings.TrimSpace(ns))
		}
	}

	return fmt.Sprintf(`---
apiVersion: v1
kind: Namespace
metadata:
  name: obtrace-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: obtrace-zero
  namespace: obtrace-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: obtrace-zero
rules:
  - apiGroups: [""]
    resources: ["pods", "namespaces", "secrets", "configmaps"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources: ["deployments", "statefulsets", "daemonsets"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["obtrace.io"]
    resources: ["obtraceinstrumentations"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
  - apiGroups: ["admissionregistration.k8s.io"]
    resources: ["mutatingwebhookconfigurations"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: obtrace-zero
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: obtrace-zero
subjects:
  - kind: ServiceAccount
    name: obtrace-zero
    namespace: obtrace-system
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: obtrace-zero-operator
  namespace: obtrace-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: obtrace-zero-operator
  template:
    metadata:
      labels:
        app: obtrace-zero-operator
    spec:
      serviceAccountName: obtrace-zero
      containers:
        - name: operator
          image: ghcr.io/obtrace/obtrace-zero-operator:%s
          ports:
            - containerPort: 9443
              name: webhook
            - containerPort: 8080
              name: metrics
            - containerPort: 8081
              name: health
          readinessProbe:
            httpGet:
              path: /readyz
              port: health
            initialDelaySeconds: 5
          livenessProbe:
            httpGet:
              path: /healthz
              port: health
            initialDelaySeconds: 10
          volumeMounts:
            - name: webhook-certs
              mountPath: /tmp/k8s-webhook-server/serving-certs
              readOnly: true
      volumes:
        - name: webhook-certs
          secret:
            secretName: obtrace-zero-webhook-certs
---
apiVersion: v1
kind: Service
metadata:
  name: obtrace-zero-webhook
  namespace: obtrace-system
spec:
  selector:
    app: obtrace-zero-operator
  ports:
    - port: 443
      targetPort: 9443
---
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: obtrace-zero
webhooks:
  - name: pod-mutator.obtrace.io
    admissionReviewVersions: ["v1"]
    sideEffects: None
    clientConfig:
      service:
        name: obtrace-zero-webhook
        namespace: obtrace-system
        path: /mutate-pods
    rules:
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["pods"]
    failurePolicy: Ignore
    namespaceSelector:
      matchExpressions:
        - key: obtrace.io/exclude
          operator: NotIn
          values: ["true"]
        - key: kubernetes.io/metadata.name
          operator: NotIn
          values: ["kube-system", "kube-public", "obtrace-system"]
---
apiVersion: obtrace.io/v1alpha1
kind: ObtraceInstrumentation
metadata:
  name: obtrace-default
  namespace: obtrace-system
spec:
  apiKey: "%s"
  ingestEndpoint: "%s"
  strategy: "%s"
  namespaces: %s
  propagation:
    injectHeaders: true
    extractHeaders: true
`, imageTag, apiKey, ingestURL, strategy, func() string {
		if nsYAML != "" {
			return nsYAML
		}
		return "\n    - \"*\""
	}())
}

var _ = context.Background
