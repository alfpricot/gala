package manager

import (
	"errors"

	"github.com/alfpricot/gala/internal/semver"
)

// noneManager is for an ecosystem gala has no adapter for (Maven, Gradle,
// Ruby, PHP, Swift, Dart, a Helm chart, a docs site): there is no manifest to
// write and nothing to publish. Tags carry the version, version files carry it
// into whatever formats the project uses, and hooks or a workflow do the rest.
type noneManager struct{}

func (n *noneManager) Name() string { return "none" }

func (n *noneManager) Tools() []string { return nil }

func (n *noneManager) Manifest(string, string) string { return "" }

func (n *noneManager) ManifestVersion(string) (semver.Version, bool, error) {
	return semver.Version{}, false, nil
}

func (n *noneManager) PackageName(string) (string, error) {
	return "", errors.New(`manager "none" has no manifest to read a name from; set name on the [[package]]`)
}

func (n *noneManager) SetVersion(string, semver.Version) error { return nil }

func (n *noneManager) CheckVersion(string, semver.Version) error { return nil }

func (n *noneManager) RefreshLock(string, string) ([]string, error) { return nil, nil }

// Preflight refuses publish = true: gala has no registry to push to, and a
// silently skipped publish would read as a successful one.
func (n *noneManager) Preflight(_ string, publish bool) error {
	if publish {
		return errors.New(`manager "none" cannot publish — set publish = false and publish from a workflow or a postBump command`)
	}
	return nil
}

func (n *noneManager) Publish(string) error { return nil }

func (n *noneManager) Recovery(string, string) string { return "" }
