package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	corev1 "k8s.io/api/core/v1"
	k8s_types "k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"bear-kong-cp/controller"
	"bear-kong-cp/envoyservice"
	"bear-kong-cp/utils"
)

func main() {
	logf.SetLogger(zap.New())

	wg := &sync.WaitGroup{}
	stop := make(chan struct{})
	defer close(stop)

	wg.Add(3)
	ctx, cancel := context.WithCancel(signals.SetupSignalHandler())
	mgr, err := controller.CreateManager(ctx)
	if err != nil {
		panic(err)
	}

	// start controller-runtime manager
	go func() {
		defer wg.Done()
		err := mgr.Start(ctx)
		if err != nil {
			logf.Log.Error(err, "controller failed")
			cancel()
		}
	}()

	snapshotCache := cache.NewSnapshotCacheWithHeartbeating(ctx, true, cache.IDHash{}, nil, time.Second)

	// run envoy XDS server
	go func() {
		defer wg.Done()
		err := envoyservice.RunXDSServer(ctx, snapshotCache)
		if err != nil {
			logf.Log.Error(err, "xds service failed")
			cancel()
		}
	}()

	// update ads at intervals
	go func() {
		ticker := time.NewTicker(time.Second * 5)
		defer func() {
			ticker.Stop()
			wg.Done()
		}()
		for {
			select {
			case <-ctx.Done():
				logf.Log.Info("shutting down ADS intervals")
				return
			case t := <-ticker.C:
				logf.Log.Info("tick", "time", t)
				configMap := &corev1.ConfigMap{}
				err := mgr.GetClient().Get(ctx, k8s_types.NamespacedName{Namespace: "default", Name: utils.ConfigMapName}, configMap)
				if err != nil {
					logf.Log.Error(err, "failed retrieving conf!")
					continue
				}
				all := []controller.ServiceMeta{}
				err = json.Unmarshal([]byte(configMap.Data["config"]), &all)
				if err != nil {
					logf.Log.Error(err, "failed reading configMap")
					continue
				}
				var clusters []types.Resource
				var routes []*route.Route
				for _, s := range all {
					tmpRoute := envoyservice.NewRoute(s.Name)
					routes = append(routes, tmpRoute)

					tmpCluster := envoyservice.NewCluster(s.Name, s.Ip, s.Port)
					clusters = append(clusters, tmpCluster)
				}

				snap, err := cache.NewSnapshot(time.Now().String(), map[resource.Type][]types.Resource{
					resource.ListenerType: {},
					resource.ClusterType:  clusters,
					resource.EndpointType: {},
					resource.RouteType: {
						&route.RouteConfiguration{
							Name: "outbound_route",
							VirtualHosts: []*route.VirtualHost{{
								Name:    "mesh",
								Domains: []string{"*"},
								Routes:  routes,
							}},
						},
					},
				})
				if err != nil {
					logf.Log.Error(err, "failed creating snapshot")
					continue
				}

				for _, s := range all {
					err := snapshotCache.SetSnapshot(ctx, s.Name, snap)
					if err != nil {
						logf.Log.Error(err, "failed setting snapshot")
						continue
					}
				}
				logf.Log.V(1).Info("update ADS at intervals successfully")
			}
		}
	}()

	<-ctx.Done()
	wg.Wait()
}
