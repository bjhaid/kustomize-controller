package main

import (
	"context"
	"os"
	"time"

	flag "github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"sigs.k8s.io/cli-utils/pkg/kstatus/polling"
	ctrl "sigs.k8s.io/controller-runtime"
	crtlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/fluxcd/pkg/runtime/client"
	"github.com/fluxcd/pkg/runtime/events"
	"github.com/fluxcd/pkg/runtime/logger"
	"github.com/fluxcd/pkg/runtime/metrics"
	sourcev1 "github.com/fluxcd/source-controller/api/v1beta1"

	kustomizev1 "github.com/fluxcd/kustomize-controller/api/v1beta2"
	"github.com/fluxcd/kustomize-controller/controllers"
	// +kubebuilder:scaffold:imports
)

const controllerName = "kustomize-controller"

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = sourcev1.AddToScheme(scheme)
	_ = kustomizev1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		eventsAddr         string
		sourcePath         string
		revision           string
		requeueDependency  time.Duration
		clientOptions      client.Options
		logOptions         logger.Options
		watchAllNamespaces bool
	)

	flag.StringVar(&eventsAddr, "events-addr", "", "The address of the events receiver.")
	flag.StringVar(&revision, "revision", "", "Revision is a human readable identifier traceable in the origin source system. It can be a Git commit SHA, Git tag etc.")
	flag.DurationVar(&requeueDependency, "requeue-dependency", 30*time.Second, "The interval at which failing dependencies are reevaluated.")
	flag.BoolVar(&watchAllNamespaces, "watch-all-namespaces", true,
		"Watch for custom resources in all namespaces, if set to false it will only watch the runtime namespace.")
	flag.StringVar(&sourcePath, "source-path", "", "The path to the kustomization directory")
	clientOptions.BindFlags(flag.CommandLine)
	logOptions.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(logger.NewLogger(logOptions))

	var eventRecorder *events.Recorder
	if eventsAddr != "" {
		if er, err := events.NewRecorder(eventsAddr, controllerName); err != nil {
			setupLog.Error(err, "unable to create event recorder")
			os.Exit(1)
		} else {
			eventRecorder = er
		}
	}

	metricsRecorder := metrics.NewRecorder()
	crtlmetrics.Registry.MustRegister(metricsRecorder.Collectors()...)

	watchNamespace := ""
	if !watchAllNamespaces {
		watchNamespace = os.Getenv("RUNTIME_NAMESPACE")
	}

	restConfig := client.GetConfigOrDie(clientOptions)
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
		LeaderElection:     false,
		Namespace:          watchNamespace,
		Logger:             ctrl.Log,
		DryRunClient:       true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	c := &controllers.KustomizationReconciler{
		ControllerName:        controllerName,
		Client:                mgr.GetClient(),
		Scheme:                mgr.GetScheme(),
		EventRecorder:         mgr.GetEventRecorderFor(controllerName),
		ExternalEventRecorder: eventRecorder,
		MetricsRecorder:       metricsRecorder,
		StatusPoller:          polling.NewStatusPoller(mgr.GetClient(), mgr.GetRESTMapper()),
		SourcePath:            sourcePath,
		Revision:              revision,
	}

	if err = c.SetupWithManager(mgr, controllers.KustomizationReconcilerOptions{
		MaxConcurrentReconciles: 2,
	}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", controllerName)
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	ctx, cancel := context.WithCancel(ctrl.SetupSignalHandler())
	go func() {
		if err := mgr.GetCache().Start(ctx); err != nil {
			setupLog.Error(err, "problem starting cache")
			os.Exit(1)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		setupLog.Error(err, "problem starting cache")
		os.Exit(1)
	}

	req := ctrl.Request{}
	req.Name = "foo"
	req.Namespace = "bar"
	ctx = ctrl.LoggerInto(ctx, logger.NewLogger(logOptions))
	c.Reconcile(ctx, req)
	cancel()
}
