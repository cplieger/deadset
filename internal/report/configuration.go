package report

import (
	"fmt"
	"reflect"
)

// Configuration is one entry of the build matrix an analysis ran, in one of
// the two shapes the schema admits: a platform, which is an operating system
// and an architecture with the build tags in effect, or a project, which is one
// compiler configuration file. Exactly one of Platform and Project is set.
type Configuration struct {
	// ID is the identifier every finding names the configuration by.
	ID string

	// Platform is the platform shape, nil for a project entry.
	Platform *Platform

	// Project is the project shape: the target-relative path of the compiler
	// configuration file, empty for a platform entry.
	Project string
}

// Platform is the platform shape of a build configuration.
type Platform struct {
	OS   string
	Arch string
	Tags []string
}

// ConfigurationNotBuilt is one configuration an analysis derived from the
// tree, could not build and dropped from its matrix, with the first line of
// the error that dropped it.
type ConfigurationNotBuilt struct {
	Configuration

	Error string
}

// configurationMembers is a build configuration entry as a document writes it:
// every member either shape declares, each nil where the entry does not name
// it. The field order is the schema's member order for both shapes.
type configurationMembers struct {
	ID      *string   `json:"id,omitzero"`
	OS      *string   `json:"os,omitzero"`
	Arch    *string   `json:"arch,omitzero"`
	Tags    *[]string `json:"tags,omitzero"`
	Project *string   `json:"project,omitzero"`
	Error   *string   `json:"error,omitzero"`
}

// MarshalJSON writes the entry in its own shape.
func (c Configuration) MarshalJSON() ([]byte, error) {
	return marshal(c.members())
}

// UnmarshalJSON reads one entry of either shape, refusing an entry that names
// members of both shapes or of neither, and the error member, which only an
// entry of configurations_not_built carries.
func (c *Configuration) UnmarshalJSON(data []byte) error {
	var read configurationMembers
	if err := decodeObject(data, reflect.ValueOf(&read).Elem()); err != nil {
		return err
	}
	if read.Error != nil {
		return at("error", errUndeclared)
	}
	held, err := read.configuration()
	if err != nil {
		return err
	}
	*c = held
	return nil
}

// MarshalJSON writes the entry in its own shape, followed by its error.
func (c ConfigurationNotBuilt) MarshalJSON() ([]byte, error) {
	written := c.members()
	written.Error = &c.Error
	return marshal(written)
}

// UnmarshalJSON reads one entry of either shape with the error that dropped
// it.
func (c *ConfigurationNotBuilt) UnmarshalJSON(data []byte) error {
	var read configurationMembers
	if err := decodeObject(data, reflect.ValueOf(&read).Elem()); err != nil {
		return err
	}
	held, err := read.configuration()
	if err != nil {
		return err
	}
	if read.Error == nil {
		return at("error", errMissing)
	}
	*c = ConfigurationNotBuilt{Configuration: held, Error: *read.Error}
	return nil
}

// members is the entry as a document writes it.
func (c Configuration) members() configurationMembers {
	written := configurationMembers{ID: &c.ID}
	if c.Platform != nil {
		written.OS, written.Arch, written.Tags = &c.Platform.OS, &c.Platform.Arch, &c.Platform.Tags
	}
	if c.Project != "" {
		written.Project = &c.Project
	}
	return written
}

// configuration is the entry the members name, read in the shape the members
// present decide: a member only a platform declares makes the entry a
// platform, and the project member makes it a project.
func (m *configurationMembers) configuration() (Configuration, error) {
	platform := m.OS != nil || m.Arch != nil || m.Tags != nil
	switch {
	case platform && m.Project != nil:
		return Configuration{}, fmt.Errorf("%w: the entry carries members of both shapes, a platform and a project", errShape)
	case !platform && m.Project == nil:
		return Configuration{}, fmt.Errorf("%w: the entry carries the members of neither shape, a platform or a project", errShape)
	}
	for _, required := range []struct {
		name    string
		present bool
	}{
		{"id", m.ID != nil},
		{"os", !platform || m.OS != nil},
		{"arch", !platform || m.Arch != nil},
		{"tags", !platform || m.Tags != nil},
	} {
		if !required.present {
			return Configuration{}, at(required.name, errMissing)
		}
	}
	if !platform {
		return Configuration{ID: *m.ID, Project: *m.Project}, nil
	}
	return Configuration{ID: *m.ID, Platform: &Platform{OS: *m.OS, Arch: *m.Arch, Tags: *m.Tags}}, nil
}
