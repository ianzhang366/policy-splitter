package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	uzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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

const (
	clusterLabel = "kcp.dev/cluster"
	ownedByLabel = "kcp.dev/owned-by"
)

// PolicyReconciler reconciles a policy CR
type PolicyReconciler struct {
	client.Client
	logger logr.Logger
}

func (r *PolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// your logic here
	r.logger.Info(fmt.Sprintf("enter reconcile: %s", req))
	defer r.logger.Info(fmt.Sprintf("exit reconcile: %s", req))

	inPlc := &policiesv1.Policy{}

	if err := r.Client.Get(ctx, req.NamespacedName, inPlc); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		r.logger.Error(err, "failed to get the policy object on entry of the reconcile")
	}

	labels := inPlc.GetLabels()

	if len(labels) == 0 || labels[clusterLabel] == "" {
		// This is a root policy; get its leafs.
		sel, err := k8slabels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, inPlc.Name))
		if err != nil {
			return ctrl.Result{}, err
		}

		leafs := &policiesv1.PolicyList{}
		if err := r.List(ctx, leafs, &client.ListOptions{LabelSelector: sel}); err != nil {
			return ctrl.Result{}, err
		}

		if len(leafs.Items) == 0 {
			if err := r.createLeafs(ctx, inPlc); err != nil {
				r.logger.Error(err, "failed to create leafs")
				return ctrl.Result{}, err
			}
		}
	} else {
		rootPolicyName := labels[ownedByLabel]
		// A leaf policy was updated; get others and aggregate status.
		sel, err := k8slabels.Parse(fmt.Sprintf("%s=%s", ownedByLabel, rootPolicyName))
		if err != nil {
			r.logger.Error(err, "failed to parse labels")
			return ctrl.Result{}, nil
		}

		// list all none root policy
		others := &policiesv1.PolicyList{}
		if err := r.List(ctx, others, &client.ListOptions{LabelSelector: sel}); err != nil {
			r.logger.Error(err, "failed to parse labels")
			return ctrl.Result{}, err
		}

		rootPolicy := &policiesv1.Policy{}

		if err := r.Get(ctx,
			types.NamespacedName{Name: rootPolicyName, Namespace: req.Namespace},
			rootPolicy); err != nil {
			return ctrl.Result{}, err
		}

		// Aggregate .status from all leafs.
		rootPolicy = rootPolicy.DeepCopy()
		rootPolicy.Status.Placement = []*policiesv1.Placement{}
		rootPolicy.Status.Status = []*policiesv1.CompliancePerClusterStatus{}

		fmt.Printf("root policy %s/%s, status: %#v\n", rootPolicy.Namespace, rootPolicy.Name, rootPolicy.Status)
		for _, o := range others.Items {
			fmt.Printf("other policy %s/%s, status: %#v\n", o.Namespace, o.Name, o.Status)
			rootPolicy.Status.Placement = append(rootPolicy.Status.Placement, o.Status.Placement...)
			rootPolicy.Status.Status = append(rootPolicy.Status.Status, o.Status.Status...)
		}

		if err := r.Status().Update(ctx, rootPolicy); err != nil {
			if k8serrors.IsConflict(err) {
				return ctrl.Result{}, err
			}

			r.logger.Error(err, "failed to update rootPolicy status")
		}
	}

	return ctrl.Result{}, nil
}

func (r *PolicyReconciler) createLeafs(ctx context.Context, root *policiesv1.Policy) error {
	cls := &unstructured.UnstructuredList{}

	previous := root.DeepCopy()

	cls.SetAPIVersion("cluster.example.dev/v1alpha1")
	cls.SetKind("Cluster")

	if err := r.List(ctx, cls); err != nil {
		return err
	}

	if len(cls.Items) == 0 {
		root.Status.Details = []*policiesv1.DetailsPerTemplate{{
			History: []policiesv1.ComplianceHistory{{
				Message: "kcp has no clusters registered to receive Deployments",
			}},
		}}

		return nil
	}

	if len(cls.Items) == 1 {
		// nothing to split, just label Deployment for the only cluster.
		if root.Labels == nil {
			root.Labels = map[string]string{}
		}

		root.Labels[clusterLabel] = cls.Items[0].GetName()

		if !equality.Semantic.DeepEqual(previous, root) {
			return r.Update(ctx, root)
		}

		return nil
	}

	// put policy to all or matched label clusters
	for _, cl := range cls.Items {
		r.logger.Info(fmt.Sprintf("got cluster; %s/%s", cl.GetNamespace(), cl.GetName()))
		vd := root.DeepCopy()

		// TODO: munge cluster name
		vd.Name = fmt.Sprintf("%s--%s", root.Name, cl.GetName())

		if vd.Labels == nil {
			vd.Labels = map[string]string{}
		}

		vd.Labels[clusterLabel] = cl.GetName()
		vd.Labels[ownedByLabel] = root.Name

		// Set OwnerReference so deleting the Deployment deletes all virtual deployments.
		vd.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "policy.open-cluster-management.io/v1",
			Kind:       "Policy",
			UID:        root.UID,
			Name:       root.Name,
		}}

		// TODO: munge namespace
		vd.SetResourceVersion("")
		if err := r.Create(ctx, vd); err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return err
			}

		}

		r.logger.Info(fmt.Sprintf("created child deployment %q", vd.Name))
	}

	return nil
}
