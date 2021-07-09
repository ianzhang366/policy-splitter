package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	uzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	policiesv1 "github.com/open-cluster-management/governance-policy-propagator/pkg/apis/policy/v1"
)

var scheme = runtime.NewScheme()

func init() {
	log.SetLogger(zap.New(zap.RawZapOpts(uzap.AddCallerSkip(1)), zap.UseDevMode(true)))
	policiesv1.SchemeBuilder.AddToScheme(scheme)
}

func main() {
	var kubeconfig *string

	kubeconfig = flag.String("kubecfg", "", "absolute path to the kubeconfig file")
	flag.Parse()

	entryLog := log.Log.WithName("entrypoint")

	// use the current context in kubeconfig
	cfg, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		entryLog.Error(err, "failed to load kubeconfig from flag")
		os.Exit(1)
	}

	// Setup a Manager
	entryLog.Info("setting up manager")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		entryLog.Error(err, "unable to set up overall controller manager")
		os.Exit(1)
	}

	// Setup a new controller to reconcile ReplicaSets
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&policiesv1.Policy{}).
		Complete(&PolicyReconciler{
			Client: mgr.GetClient(),
			logger: log.Log.WithName("policy-splitter"),
		}); err != nil {
		entryLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	entryLog.Info("starting manager")
	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}

// PolicyReconciler reconciles a policy CR
type PolicyReconciler struct {
	client.Client
	logger logr.Logger
}

func (r *PolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// your logic here
	r.logger.Info(fmt.Sprintf("reconcile request: %s", req))

	return ctrl.Result{}, nil
}
