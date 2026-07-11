package catalog

import (
	"github.com/snyderb-de/sys-bozo/internal/packages"
	"gopkg.in/yaml.v3"
)

type PlacementScope string

const (
	ScopeCurrentHost PlacementScope = "current-host"
	ScopeProfile     PlacementScope = "profile"
)

type PlacementRequest struct {
	Candidate  packages.Candidate
	LogicalKey string
	Hostname   string
	Scope      PlacementScope
}

type Tool struct {
	Key             string
	Category        string
	ProviderIDs     map[string]string
	DefaultProfiles []string
	ConfigSync      string
}

type Host struct {
	Name    string
	OS      string
	Arch    string
	Profile string
	User    string
	Include []string
	Exclude []string
}

type Store struct {
	Repo      string
	ToolsPath string
	HostsPath string
	Tools     map[string]Tool
	Hosts     map[string]Host

	toolsDoc   *yaml.Node
	hostsDoc   *yaml.Node
	toolsBytes []byte
	hostsBytes []byte
}
