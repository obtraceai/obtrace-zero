package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	"github.com/obtraceai/obtrace-zero/pkg/discovery"
	"github.com/obtraceai/obtrace-zero/pkg/webhook"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("obtrace-zero operator starting")

	webhookPort := envOrDefault("WEBHOOK_PORT", "9443")
	metricsAddr := envOrDefault("METRICS_ADDR", ":8080")
	healthAddr := envOrDefault("HEALTH_ADDR", ":8081")
	certDir := envOrDefault("CERT_DIR", "/tmp/k8s-webhook-server/serving-certs")
	discoveryInterval := envOrDefault("DISCOVERY_INTERVAL", "60")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: healthAddr,
		WebhookServer: ctrlwebhook.NewServer(ctrlwebhook.Options{
			Port:    mustAtoi(webhookPort),
			CertDir: certDir,
		}),
	})
	if err != nil {
		log.Fatalf("unable to create manager: %v", err)
	}

	mutator := webhook.NewPodMutator(mgr.GetClient())
	mgr.GetWebhookServer().Register("/mutate-pods", &admission.Webhook{Handler: mutator})

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Fatalf("unable to set up health check: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Fatalf("unable to set up ready check: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go runDiscoveryLoop(ctx, mgr, discoveryInterval)

	log.Printf("starting operator (webhook=:%s metrics=%s health=%s)", webhookPort, metricsAddr, healthAddr)
	if err := mgr.Start(ctx); err != nil {
		log.Fatalf("operator exited with error: %v", err)
	}
}

func runDiscoveryLoop(ctx context.Context, mgr ctrl.Manager, intervalStr string) {
	interval := 60
	fmt.Sscanf(intervalStr, "%d", &interval)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	disc := discovery.New(mgr.GetClient())

	time.Sleep(5 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			workloads, err := disc.DiscoverAll(ctx, nil)
			if err != nil {
				log.Printf("discovery error: %v", err)
				continue
			}
			log.Printf("discovered %d workloads", len(workloads))
			for _, w := range workloads {
				log.Printf("  %s/%s (%s) → lang=%s framework=%s strategy=%s confidence=%.1f",
					w.Namespace, w.Name, w.Kind, w.Language, w.Framework, w.Strategy, w.Confidence)
			}
		}
	}
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func mustAtoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	if n == 0 {
		n = 9443
	}
	return n
}

var _ = http.StatusOK
var _ = crd.StrategyAuto
