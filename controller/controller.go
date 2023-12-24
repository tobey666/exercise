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
			utils.ConfigMapDataKey: cfg,
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
		ControllerManagedBy(mgr).                                   // Create the ControllerManagedBy
		For(&corev1.ConfigMap{}, builder.WithPredicates(selector)). // the controller-runtime cache already set up a sharedIndexInformer with 'For'
		Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			if object.GetNamespace() == utils.SystemNamespace { // Don't to anything for
				return nil
			}

			// The workqueue data are all belong to ConfigMap type
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

	log.Info("try to generate new ServiceMeta sets")
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

	var hasOldConfigMap, isNeedUpdate = true, false
	var oldConfigMap corev1.ConfigMap
	// Since the controller-runtime manager started, there's a sharedIndexInformer runs.
	// And the 'Client.Get()' function would find data from the Cache.
	// If you want to get the real time data from APIServer, you should use 'mgr.GetAPIReader().Get()'
	err = a.Get(ctx, types.NamespacedName{
		Namespace: req.Namespace,
		Name:      utils.ConfigMapName,
	}, &oldConfigMap)
	if nil != err {
		if k8s_errors.IsNotFound(err) {
			hasOldConfigMap = false
		} else {
			log.Error(err, "failed to get configmap")
			return reconcile.Result{}, err
		}
	}

	if !hasOldConfigMap {
		log.Info("no configmap found, try to create", "Namespace", req.Namespace, "ConfigmapName", utils.ConfigMapName)
		if cerr := a.Create(ctx, configMap); cerr != nil {
			log.Error(err, "failed to create configmap")
			return reconcile.Result{}, cerr
		}
	} else {
		// no need to call reflect.DeepEqual with this simple struct to decrease the performance.
		// reflect.DeepEqual(oldConfigMap,configMap)

		// check the Configmap labels
		isEnabled, ok := oldConfigMap.GetLabels()[utils.MeshSelector]
		if !ok {
			log.V(1).Info("the old Configmap lacks of mesh labels, try to update it")
			isNeedUpdate = true
		} else {
			if isEnabled != utils.Enabled {
				log.V(1).Info("the old Configmap mesh label value is not same with 'enabled', try to update it")
				isNeedUpdate = true
			}
		}

		// check the Configmap data
		oldConfigMapData := oldConfigMap.Data[utils.ConfigMapDataKey]
		newConfigMapData := configMap.Data[utils.ConfigMapDataKey]
		if oldConfigMapData != newConfigMapData {
			log.V(1).Info("the old configmap data is already out of data, try to update it")
			isNeedUpdate = true
		}

		if isNeedUpdate {
			log.Info("try to update configmap", "ConfigMap", configMap.String())
			err := a.Update(ctx, configMap)
			if nil != err {
				log.Error(err, "failed to update configmap")
				return reconcile.Result{}, err
			}
			log.Info("updated configmap successfully")
		} else {
			log.Info("no need to update configmap", "ConfigMap-NS", configMap.Namespace, "ConfigMap-Name", configMap.Name)
		}
	}

	return reconcile.Result{}, nil
}
