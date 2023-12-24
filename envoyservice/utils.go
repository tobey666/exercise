package envoyservice

import (
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"google.golang.org/protobuf/types/known/durationpb"

	"bear-kong-cp/utils"
)

// NewRoute will generate an envoy route.Route.
// If the parameter timeout is not nil, we'll set it for Route Action
func NewRoute(name string, timeout *time.Duration) *route.Route {
	var timeoutDuration *durationpb.Duration
	if timeout != nil {
		timeoutDuration = durationpb.New(*timeout)
	}

	r := &route.Route{
		Name: name,
		Match: &route.RouteMatch{
			PathSpecifier: &route.RouteMatch_Prefix{
				Prefix: "/",
			},
			Headers: []*route.HeaderMatcher{
				{
					Name: utils.ServiceStr,
					HeaderMatchSpecifier: &route.HeaderMatcher_StringMatch{
						StringMatch: &matcherv3.StringMatcher{
							MatchPattern: &matcherv3.StringMatcher_Exact{
								Exact: name,
							},
						},
					},
				},
			},
		},
		Action: &route.Route_Route{
			Route: &route.RouteAction{
				ClusterSpecifier: &route.RouteAction_Cluster{
					Cluster: name,
				},
				Timeout: timeoutDuration,
			},
		},
	}

	return r
}

// NewCluster will generate an envoy clusterv3.Cluster
func NewCluster(name, addr string, port int32) *clusterv3.Cluster {
	c := &clusterv3.Cluster{
		Name: name,
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: name,
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpointv3.LbEndpoint{
						{
							HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
								Endpoint: &endpointv3.Endpoint{
									Address: &corev3.Address{
										Address: &corev3.Address_SocketAddress{
											SocketAddress: &corev3.SocketAddress{
												Address: addr,
												PortSpecifier: &corev3.SocketAddress_PortValue{
													PortValue: uint32(port),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return c
}
