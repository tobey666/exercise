package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"bear-kong-cp/utils"
)

var meshMatchLabels = map[string]string{
	utils.MeshSelector: utils.Enabled,
}

// newConf will generate a configmap with give namespace and ServiceMeta sets data
func newConf(ns string, cfg string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      utils.ConfigMapName,
			Labels:    meshMatchLabels,
		},
		Data: map[string]string{
			"config": cfg,
		},
	}
}

func CreateManager(ctx context.Context) (manager.Manager, error) {
	var log = logf.Log.WithName("controller")
	log.Info("Starting controller")

	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		MetricsBindAddress: "0",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to new controller-runtimer manger, error: %w", err)
	}

	selector, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchLabels: meshMatchLabels,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to construct label selector predicate, error: %w", err)
	}

	err = builder.
		ControllerManagedBy(mgr). // Create the ControllerManagedBy
		For(&corev1.ConfigMap{}, builder.WithPredicates(selector)).
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			if object.GetNamespace() == utils.SystemNamespace { // Don't to anything for
				return nil
			}
			return []reconcile.Request{
				{types.NamespacedName{Namespace: object.GetNamespace(), Name: utils.ConfigMapName}},
			}
		})).
		Complete(&MeshConfReconciler{
			Client: mgr.GetClient(),
		})
	return mgr, err
}

// MeshConfReconciler is a simple ControllerManagedBy example implementation.
type MeshConfReconciler struct {
	client.Client
}

type ServiceMeta struct {
	Name string `json:"name"`
	Ip   string `json:"ip"`
	Port int32  `json:"port"`
}

func (a *MeshConfReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := logf.Log.WithName("reconcile").WithValues("namespace", req.Namespace, "name", req.Name)
	log.Info("reconciling", "Request-NamespacedName", req.String())

	services := &corev1.ServiceList{}
	err := a.List(ctx, services, client.InNamespace(req.Namespace), client.MatchingLabels(meshMatchLabels))
	if err != nil {
		log.Error(err, "failed to list service", "Request-NamespacedName", req.String(), "MatchingLabel", meshMatchLabels)
		return reconcile.Result{}, err
	}

	log.Info("try to generate ServiceMeta sets")
	var srvs []ServiceMeta
	for _, s := range services.Items {
		if s.Spec.ClusterIP == "" || len(s.Spec.Ports) == 0 {
			log.V(1).Info("ignore service due to no ClusterIP or Ports", "Service", s.String())
			continue
		}
		srvs = append(srvs, ServiceMeta{
			Name: s.Name,
			Ip:   s.Spec.ClusterIP,
			Port: s.Spec.Ports[0].Port,
		})
	}
	sort.SliceStable(srvs, func(i, j int) bool {
		return srvs[i].Name < srvs[j].Name
	})
	log.V(1).Info("ServiceMeta sets", "data", srvs)

	data, err := json.MarshalIndent(srvs, "", "  ")
	if err != nil {
		log.Error(err, "failed to marshal ServiceMeta sets")
		return reconcile.Result{}, err
	}

	configMap := newConf(req.Namespace, string(data))

	logger := log.WithValues("CofigMap", configMap.String())
	if err = a.Update(ctx, configMap); err != nil && k8s_errors.IsNotFound(err) {
		logger.Info("no configmap found, try to create")
		if cerr := a.Create(ctx, configMap); cerr != nil {
			logger.Error(err, "failed to create configmap")
			return reconcile.Result{}, cerr
		}
	} else if err != nil {
		logger.Error(err, "failed to update configmap")
		return reconcile.Result{}, err
	}

	logger.Info("updated configmap successfully")
	return reconcile.Result{}, nil
}
