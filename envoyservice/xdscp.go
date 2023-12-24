package envoyservice

import (
	"context"
	"net"

	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	xds "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func RunXDSServer(ctx context.Context, snapshotCache cache.SnapshotCache) error {
	server := xds.NewServer(ctx, snapshotCache, xds.CallbackFuncs{
		StreamRequestFunc: func(streamId int64, request *discoverygrpc.DiscoveryRequest) error {
			logf.Log.V(1).Info("xds request received", "streamId", streamId, "node", request.Node.Id, "cluster", request.Node.Cluster, "resources", request.ResourceNames, "curVersionInfo", request.VersionInfo)
			return nil
		},
		StreamResponseFunc: func(ctx context.Context, streamId int64, request *discoverygrpc.DiscoveryRequest, response *discoverygrpc.DiscoveryResponse) {
			logf.Log.V(1).Info("xds response returned", "streamId", streamId, "node", request.Node.Id, "cluster", request.Node.Cluster, "versionInfo", response.VersionInfo)
		},
	})
	grpcServer := grpc.NewServer()
	lis, _ := net.Listen("tcp", ":5000")

	errChan := make(chan error)
	go func() {
		discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, server)
		errChan <- grpcServer.Serve(lis)
	}()
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		grpcServer.GracefulStop()
		return nil
	}
}
