package processor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	types "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cache "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resource "github.com/envoyproxy/go-control-plane/pkg/resource/v3"

	prxycfg "github.com/fmgornick/dynamic-envoy/utils/config/proxy"
	univcfg "github.com/fmgornick/dynamic-envoy/utils/config/universal"
	usercfg "github.com/fmgornick/dynamic-envoy/utils/config/user"
	parser "github.com/fmgornick/dynamic-envoy/utils/parser"
	watcher "github.com/fmgornick/dynamic-envoy/utils/watcher"
)

type EnvoyProcessor struct {
	Cache        cache.SnapshotCache        // snapshot config (output for envoyproxy)
	Configs      map[string]*univcfg.Config // map of universal configs
	ListenerInfo univcfg.ListenerInfo       // info on what ports and addresses to listen on
	Node         string                     // name of node for snapshot
	version      uint                       // keeps track of version number for our envoyproxy config
}

func NewProcessor(node string, iAddr string, eAddr string, iPort uint, ePort uint) *EnvoyProcessor {
	return &EnvoyProcessor{
		Cache:   cache.NewSnapshotCache(false, cache.IDHash{}, nil),
		Configs: make(map[string]*univcfg.Config),
		ListenerInfo: univcfg.ListenerInfo{
			InternalAddress: iAddr,
			ExternalAddress: eAddr,
			InternalPort:    iPort,
			ExternalPort:    ePort,
		},
		Node:    node,
		version: 0,
	}
}

// take change, update configs map, update snapshot cache
func (e *EnvoyProcessor) Process(msg watcher.Message) error {
	/* -------------------- MESSAGE CASES -------------------- */
	// new file:     walk through if it's a directory, then call ProcessFile
	// file changed: walk through if it's a directory, then call ProcessFile
	// file deleted: delete corresponding config in map
	// file moved:   delete corresponding config in map
	if msg.Operation == watcher.Move || msg.Operation == watcher.Delete {
		if e.Configs[msg.Path] != nil {
			delete(e.Configs, msg.Path)
		} else {
			// if it's a directory then delete every key corresponding to it's elements
			for key := range e.Configs {
				if strings.HasPrefix(key, msg.Path) {
					delete(e.Configs, key)
				}
			}
		}
	} else {
		// check if file is a directory
		info, err := os.Stat(msg.Path)
		if err != nil {
			return fmt.Errorf("path check error: %+v", err)
		}

		// if it's a directory, then we want to call our operations on all the subdirectories and files
		// if it's a file, then we want to call ProcessFile, to actually update the config
		if info.IsDir() {
			err := filepath.Walk(msg.Path, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				// don't want to recursively call ourself, otherwise it's an infinite loop
				if path == msg.Path {
					return nil
				}

				// process change for all the sub-directories via recursive call
				// if it's a file, then we want to call ProcessFile, to actually update the config
				if info.IsDir() {
					return e.Process(watcher.Message{
						Operation: msg.Operation,
						Path:      path,
					})
				} else {
					return e.processFile(watcher.Message{
						Operation: msg.Operation,
						Path:      path,
					})
				}
			})
			if err != nil {
				return fmt.Errorf("failed to walk directory path: %+v", err)
			}
		} else {
			err := e.processFile(msg)
			if err != nil {
				return fmt.Errorf("failed to process file: %+v", err)
			}
		}
	}
	// generate new snapshot from configuration and update the cache
	if len(e.Configs) != 0 {
		return e.setSnapshot()
	}
	return nil
}

// called by ProcessChange, updates config of newly created/modified files
func (e *EnvoyProcessor) processFile(msg watcher.Message) error {
	var err error
	var bags []usercfg.Bag
	var config *univcfg.Config

	/* -------------------- MESSAGE CASES -------------------- */
	// new file:     add it's configuration to our existing one
	// file changed: delete existing configuration of file, then re-add it
	// file deleted: covered by Process
	// file moved:   covered by Process
	if msg.Operation == watcher.Modify {
		delete(e.Configs, msg.Path)
	}
	if bags, err = usercfg.ParseFile(msg.Path); err != nil {
		return err
	}
	if config, err = parser.Parse(bags, e.ListenerInfo); err != nil {
		return err
	}
	e.Configs[msg.Path] = config

	return nil
}

// create resources array to hold all our listener configurations
func makeListeners(config *univcfg.Config) []types.Resource {
	var resources []types.Resource

	for _, l := range config.Listeners {
		resources = append(resources, prxycfg.MakeListener(l.Address, l.Name, l.Port))
	}

	return resources
}

// create resources array to hold all our cluster configurations
func makeClusters(config *univcfg.Config) []types.Resource {
	var resources []types.Resource

	for _, c := range config.Clusters {
		resources = append(resources, prxycfg.MakeCluster(c.Name, c.Policy))
	}

	return resources
}

// create resources array to hold all our route configurations
func makeRoutes(config *univcfg.Config) []types.Resource {
	// keep track of internal and external routes
	var internalRoutes []*route.Route
	var externalRoutes []*route.Route

	var resources []types.Resource

	// iterate through internal routes listed in internal listener
	// add each route to our internal route array
	for _, routeName := range config.Listeners["internal"].Routes {
		r := config.Routes[routeName]
		internalRoutes = append(internalRoutes, prxycfg.MakeRoute(r.ClusterName, r.Path, r.Type))
	}
	// iterate through internal routes listed in external listener
	// add each route to our external route array
	for _, routeName := range config.Listeners["external"].Routes {
		r := config.Routes[routeName]
		externalRoutes = append(externalRoutes, prxycfg.MakeRoute(r.ClusterName, r.Path, r.Type))
	}
	// add internal route configuration to resources array for internal routes
	resources = append(resources, &route.RouteConfiguration{
		Name: "internal-routes",
		VirtualHosts: []*route.VirtualHost{{
			Name:    "internal-routes",
			Domains: []string{"*"},
			Routes:  internalRoutes,
		}},
	})
	// add internal route configuration to resources array for external routes
	resources = append(resources, &route.RouteConfiguration{
		Name: "external-routes",
		VirtualHosts: []*route.VirtualHost{{
			Name:    "external-routes",
			Domains: []string{"*"},
			Routes:  externalRoutes,
		}},
	})

	return resources
}

// create resources array to hold all our endpoint configurations
func makeEndpoints(config *univcfg.Config) []types.Resource {
	var resources []types.Resource
	var endpoints []*endpoint.LbEndpoint

	for clusterName, edps := range config.Endpoints {
		// create endpoint array of all the endpoints that a single cluster maps to
		for _, e := range edps {
			endpoints = append(endpoints, prxycfg.MakeEndpoint(e.Address, e.Port, e.Weight))
		}
		// add this new array of endpoints to our resources array
		resources = append(resources, &endpoint.ClusterLoadAssignment{
			ClusterName: clusterName,
			Endpoints: []*endpoint.LocalityLbEndpoints{{
				LbEndpoints: endpoints,
			}},
		})
	}

	return resources
}

// turns map of universal configs into snapshot, then sets the cache
func (e *EnvoyProcessor) setSnapshot() error {
	cfg := univcfg.MergeConfigs(e.Configs)
	// turn our universal configs into envoy proxy configs and add them to snapshot map
	snapshot, err := cache.NewSnapshot(e.newVersion(),
		map[resource.Type][]types.Resource{
			resource.ListenerType: makeListeners(cfg),
			resource.ClusterType:  makeClusters(cfg),
			resource.RouteType:    makeRoutes(cfg),
			resource.EndpointType: makeEndpoints(cfg),
		},
	)
	if err != nil {
		return fmt.Errorf("problem generating snapshot: %+v", err)
	}
	// make sure our cache is consistent with itself
	if err = snapshot.Consistent(); err != nil {
		return fmt.Errorf("snapshot inconsistency: \n\n%+v", err)
	}
	// set our cache
	if err = e.Cache.SetSnapshot(context.Background(), "envoy-instance", snapshot); err != nil {
		return fmt.Errorf("snapshot error: %+v\n\n%+v", snapshot, err)
	}

	// return cache to the caller
	return nil
}

func (e *EnvoyProcessor) newVersion() string {
	e.version++
	return strconv.Itoa(int(e.version))
}
