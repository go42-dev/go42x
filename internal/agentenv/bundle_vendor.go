package agentenv

import (
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/go42-dev/go42x/internal/config"
	"github.com/go42-dev/go42x/internal/fspatch"
	"github.com/go42-dev/go42x/internal/vendoring"
)

// vendorBundle holds the vendoring manifest and lock from one side of a bundle merge.
type vendorBundle struct {
	manifest bundleFile
	lock     bundleFile
}

// prepareVendorLock schedules the lock for the merged vendoring manifest in dir.
func prepareVendorLock(plan *fspatch.Plan, dir string, manifest bundleFile, incoming map[string][]byte) error {
	var current vendorBundle
	var err error
	current.manifest.data, current.manifest.exists, err = plan.Read(filepath.Join(dir, vendoring.ManifestFile))
	if err != nil {
		return err
	}

	target := filepath.Join(dir, vendoring.LockFile)
	current.lock.data, current.lock.exists, err = plan.Read(target)
	if err != nil {
		return err
	}

	var bundled vendorBundle
	bundled.manifest.data, bundled.manifest.exists = incoming[vendoring.ManifestFile]
	bundled.lock.data, bundled.lock.exists = incoming[vendoring.LockFile]
	lock, err := mergeVendorLock(manifest, current, bundled)
	if err != nil {
		return fmt.Errorf("merge %s: %w", vendoring.LockFile, err)
	}

	if lock.exists {
		return plan.Write(target, lock.data, fspatch.KindSources, false)
	}

	return plan.Remove(target, fspatch.KindSources)
}

// mergeVendorLock builds the lock for a merged vendoring manifest instead of merging
// lock text, whose manifest checksum changes with every manifest edit. A dependency
// that the incoming bundle ships takes the incoming locked state, because the update
// installs the bundled copies of its files, so its definition must match the bundle.
// Other dependencies keep the project's locked state, which must match the project manifest.
func mergeVendorLock(manifest bundleFile, current, incoming vendorBundle) (bundleFile, error) {
	if !manifest.exists {
		return bundleFile{}, nil
	}

	merged, err := vendoring.ParseManifest(manifest.data)
	if err != nil {
		return bundleFile{}, fmt.Errorf("merged %s: %w", vendoring.ManifestFile, err)
	}

	bundled, bundledLock, err := parseVendorBundle(incoming)
	if err != nil {
		return bundleFile{}, fmt.Errorf("bundled %w", err)
	}

	lock := &vendoring.Lock{
		Manifest:     vendoring.ManifestFile,
		Dependencies: make(map[string]vendoring.LockedDependency, len(merged.Dependencies)),
	}
	if lock.ManifestSHA256, err = merged.Digest(); err != nil {
		return bundleFile{}, err
	}

	ids := slices.Sorted(maps.Keys(merged.Dependencies))
	// Only project dependencies need the project lock; it may be stale or invalid otherwise.
	project, projectLock := &vendoring.Manifest{}, &vendoring.Lock{}
	if i := slices.IndexFunc(ids, func(id string) bool {
		_, ok := bundled.Dependencies[id]
		return !ok
	}); i >= 0 {
		if project, projectLock, err = parseVendorBundle(current); err != nil {
			return bundleFile{}, fmt.Errorf("project dependency %s: %w; %s", ids[i], err, vendorUpdateHint())
		}
	}

	for _, id := range ids {
		dependency := merged.Dependencies[id]
		if definition, ok := bundled.Dependencies[id]; ok {
			if !reflect.DeepEqual(dependency, definition) {
				return bundleFile{}, fmt.Errorf(
					"dependency %s differs from the bundled definition, whose files the update installs", id)
			}

			lock.Dependencies[id] = bundledLock.Dependencies[id]
			continue
		}

		if definition, ok := project.Dependencies[id]; !ok || !reflect.DeepEqual(dependency, definition) {
			return bundleFile{}, fmt.Errorf("project dependency %s is not locked; %s", id, vendorUpdateHint())
		}

		lock.Dependencies[id] = projectLock.Dependencies[id]
	}

	// Keep existing bytes when either side already holds this lock.
	if reflect.DeepEqual(lock, bundledLock) {
		return incoming.lock, nil
	}
	if current.lock.exists {
		if existing, err := vendoring.ParseLock(current.lock.data); err == nil && reflect.DeepEqual(lock, existing) {
			return current.lock, nil
		}
	}

	data, err := lock.Marshal()
	if err != nil {
		return bundleFile{}, err
	}

	return bundleFile{
		data:   data,
		exists: true,
	}, nil
}

// parseVendorBundle reads a manifest and the lock that must match it. A side without
// a manifest has no dependencies.
func parseVendorBundle(files vendorBundle) (*vendoring.Manifest, *vendoring.Lock, error) {
	empty := map[string]vendoring.LockedDependency{}
	if !files.manifest.exists {
		return &vendoring.Manifest{Dependencies: map[string]vendoring.Dependency{}},
			&vendoring.Lock{Dependencies: empty}, nil
	}

	manifest, err := vendoring.ParseManifest(files.manifest.data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", vendoring.ManifestFile, err)
	}
	if !files.lock.exists {
		return nil, nil, fmt.Errorf("%s is missing", vendoring.LockFile)
	}

	lock, err := vendoring.ParseLock(files.lock.data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", vendoring.LockFile, err)
	}
	if err := lock.Matches(manifest); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", vendoring.LockFile, err)
	}

	return manifest, lock, nil
}

func vendorUpdateHint() string {
	return fmt.Sprintf("run go42x vendor update --manifest %s before updating",
		filepath.ToSlash(filepath.Join(config.Directory, vendoring.ManifestFile)))
}
