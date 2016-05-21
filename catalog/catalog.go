package catalog

import (
	"fmt"

	"github.com/dnaeon/gru/module"
	"github.com/dnaeon/gru/resource"
)

// Catalog type represents a collection of modules and resources
type Catalog struct {
	// Loaded modules after topological sorting
	Modules []*module.Module

	// Instantiated resources from the loaded modules
	Resources []resource.Resource

	// Catalog configuration
	Config *Config
}

// Config type represents a set of settings to use when
// creating and processing the catalog
type Config struct {
	// Name of main module to load
	Main string

	// Do not take any actions, just report what would be done
	DryRun bool

	// Module configuration settings to use
	ModuleConfig *module.Config
}

// Run processes the catalog
func (c *Catalog) Run() error {
	// Use the same writer as the one used by the resources
	w := c.Config.ModuleConfig.ResourceConfig.Writer

	fmt.Fprintf(w, "Loaded %d resources from %d modules\n", len(c.Resources), len(c.Modules))
	for _, r := range c.Resources {
		id := r.ResourceID()

		state, err := r.Evaluate()
		if err != nil {
			fmt.Fprintf(w, "%s %s\n", id, err)
			continue
		}

		if c.Config.DryRun {
			continue
		}

		// TODO: Skip resources which have failed dependencies

		var resourceErr error
		switch {
		case state.Want == state.Current:
			// Resource is in the desired state
			break
		case state.Want == resource.StatePresent || state.Want == resource.StateRunning:
			// Resource is absent, should be present
			if state.Current == resource.StateAbsent || state.Current == resource.StateStopped {
				fmt.Fprintf(w, "%s is %s, should be %s\n", id, state.Current, state.Want)
				resourceErr = r.Create()
			}
		case state.Want == resource.StateAbsent || state.Want == resource.StateStopped:
			// Resource is present, should be absent
			if state.Current == resource.StatePresent || state.Current == resource.StateRunning {
				fmt.Fprintf(w, "%s is %s, should be %s\n", id, state.Current, state.Want)
				resourceErr = r.Delete()
			}
		default:
			fmt.Fprintf(w, "%s unknown state(s): want %s, current %s\n", id, state.Want, state.Current)
			continue
		}

		if resourceErr != nil {
			fmt.Fprintf(w, "%s %s\n", id, resourceErr)
		}

		// Update resource if needed
		if state.Update {
			fmt.Fprintf(w, "%s resource is out of date, will be updated\n", id)
			if err := r.Update(); err != nil {
				fmt.Fprintf(w, "%s %s\n", id, err)
			}
		}
	}

	return nil
}

// Load creates a new catalog from the provided configuration
func Load(config *Config) (*Catalog, error) {
	c := &Catalog{
		Modules:   make([]*module.Module, 0),
		Resources: make([]resource.Resource, 0),
		Config:    config,
	}

	// Discover and load the modules from the provided
	// module path, sort the import graph and
	// finally add the sorted modules to the catalog
	modules, err := module.DiscoverAndLoad(config.ModuleConfig)
	if err != nil {
		return c, err
	}

	modulesGraph, err := module.ImportGraph(config.Main, config.ModuleConfig.Path)
	if err != nil {
		return c, err
	}

	modulesSorted, err := modulesGraph.Sort()
	if err != nil {
		return c, err
	}

	for _, node := range modulesSorted {
		c.Modules = append(c.Modules, modules[node.Name])
	}

	// Build the dependency graph for the resources from the
	// loaded modules and sort them
	collection, err := module.ResourceCollection(c.Modules)
	if err != nil {
		return c, err
	}

	collectionGraph, err := collection.DependencyGraph()
	if err != nil {
		return c, err
	}

	collectionSorted, err := collectionGraph.Sort()
	if err != nil {
		return c, err
	}

	for _, node := range collectionSorted {
		c.Resources = append(c.Resources, collection[node.Name])
	}

	return c, nil
}
