package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

var (
	ErrInvalidCatalog        = errors.New("invalid catalog")
	ErrDuplicateKey          = errors.New("duplicate mapping key")
	ErrNonRegularCatalogFile = errors.New("catalog file is not a regular file")
)

var providerFields = [...]string{"nix", "brew", "brewCask", "dnf", "apt"}

func Load(repo string) (*Store, error) {
	toolsPath := filepath.Join(repo, "catalog", "tools.yaml")
	hostsPath := filepath.Join(repo, "catalog", "hosts.yaml")

	toolsBytes, toolsDoc, err := loadDocument(toolsPath)
	if err != nil {
		return nil, fmt.Errorf("load tools catalog: %w", err)
	}
	hostsBytes, hostsDoc, err := loadDocument(hostsPath)
	if err != nil {
		return nil, fmt.Errorf("load hosts catalog: %w", err)
	}

	tools, err := parseTools(toolsDoc)
	if err != nil {
		return nil, fmt.Errorf("parse tools catalog: %w", err)
	}
	hosts, err := parseHosts(hostsDoc)
	if err != nil {
		return nil, fmt.Errorf("parse hosts catalog: %w", err)
	}

	return &Store{
		Repo: repo, ToolsPath: toolsPath, HostsPath: hostsPath,
		Tools: tools, Hosts: hosts,
		toolsDoc: toolsDoc, hostsDoc: hostsDoc,
		toolsBytes: toolsBytes, hostsBytes: hostsBytes,
	}, nil
}

func loadDocument(path string) ([]byte, *yaml.Node, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, nil, fmt.Errorf("%s: %w", path, ErrNonRegularCatalogFile)
		}
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.Join(fmt.Errorf("%s: %w", path, ErrNonRegularCatalogFile), file.Close())
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("%w: decode YAML: %v", ErrInvalidCatalog, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, fmt.Errorf("%w: multiple YAML documents", ErrInvalidCatalog)
		}
		return nil, nil, fmt.Errorf("%w: decode trailing YAML: %v", ErrInvalidCatalog, err)
	}
	if err := rejectDuplicateKeys(&doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalidCatalog, err)
	}
	return data, &doc, nil
}

func parseTools(doc *yaml.Node) (map[string]Tool, error) {
	root, err := documentMapping(doc)
	if err != nil {
		return nil, err
	}
	toolsNode, err := requiredMapping(root, "tools")
	if err != nil {
		return nil, err
	}

	tools := make(map[string]Tool, len(toolsNode.Content)/2)
	for i := 0; i < len(toolsNode.Content); i += 2 {
		key, err := scalarString(toolsNode.Content[i])
		if err != nil {
			return nil, fmt.Errorf("tool key: %w", err)
		}
		node := toolsNode.Content[i+1]
		if node.Kind != yaml.MappingNode {
			return nil, invalidType("tool "+key, "mapping", node)
		}
		tool := Tool{Key: key, ProviderIDs: make(map[string]string)}
		if value := mappingValue(node, "category"); value != nil {
			tool.Category, err = scalarString(value)
			if err != nil {
				return nil, fmt.Errorf("tool %s category: %w", key, err)
			}
		}
		if value := mappingValue(node, "defaultProfiles"); value != nil {
			tool.DefaultProfiles, err = sequenceStrings(value)
			if err != nil {
				return nil, fmt.Errorf("tool %s defaultProfiles: %w", key, err)
			}
		}
		for _, field := range providerFields {
			if value := mappingValue(node, field); value != nil {
				tool.ProviderIDs[field], err = scalarString(value)
				if err != nil {
					return nil, fmt.Errorf("tool %s %s: %w", key, field, err)
				}
			}
		}
		if config := mappingValue(node, "config"); config != nil {
			if config.Kind != yaml.MappingNode {
				return nil, invalidType("tool "+key+" config", "mapping", config)
			}
			if sync := mappingValue(config, "sync"); sync != nil {
				tool.ConfigSync, err = scalarString(sync)
				if err != nil {
					return nil, fmt.Errorf("tool %s config.sync: %w", key, err)
				}
			}
		}
		if value := mappingValue(node, "platforms"); value != nil {
			if _, err := sequenceStrings(value); err != nil {
				return nil, fmt.Errorf("tool %s platforms: %w", key, err)
			}
		}
		if value := mappingValue(node, "optional"); value != nil && (value.Kind != yaml.ScalarNode || value.Tag != "!!bool") {
			return nil, invalidType("tool "+key+" optional", "boolean", value)
		}
		tools[key] = tool
	}
	return tools, nil
}

func parseHosts(doc *yaml.Node) (map[string]Host, error) {
	root, err := documentMapping(doc)
	if err != nil {
		return nil, err
	}
	hostsNode, err := requiredMapping(root, "hosts")
	if err != nil {
		return nil, err
	}

	hosts := make(map[string]Host, len(hostsNode.Content)/2)
	for i := 0; i < len(hostsNode.Content); i += 2 {
		name, err := scalarString(hostsNode.Content[i])
		if err != nil {
			return nil, fmt.Errorf("host key: %w", err)
		}
		node := hostsNode.Content[i+1]
		if node.Kind != yaml.MappingNode {
			return nil, invalidType("host "+name, "mapping", node)
		}
		host := Host{Name: name}
		for field, target := range map[string]*string{
			"os": &host.OS, "arch": &host.Arch, "profile": &host.Profile, "user": &host.User,
		} {
			if value := mappingValue(node, field); value != nil {
				*target, err = scalarString(value)
				if err != nil {
					return nil, fmt.Errorf("host %s %s: %w", name, field, err)
				}
			}
		}
		for field, target := range map[string]*[]string{"include": &host.Include, "exclude": &host.Exclude} {
			if value := mappingValue(node, field); value != nil {
				*target, err = sequenceStrings(value)
				if err != nil {
					return nil, fmt.Errorf("host %s %s: %w", name, field, err)
				}
			}
		}
		for _, field := range []string{"dotfiles_flake_key", "notes"} {
			if value := mappingValue(node, field); value != nil {
				if _, err := scalarString(value); err != nil {
					return nil, fmt.Errorf("host %s %s: %w", name, field, err)
				}
			}
		}
		hosts[name] = host
	}
	return hosts, nil
}

func documentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, invalidType("document root", "mapping", doc)
	}
	return doc.Content[0], nil
}

func requiredMapping(root *yaml.Node, key string) (*yaml.Node, error) {
	value := mappingValue(root, key)
	if value == nil {
		return nil, fmt.Errorf("%w: missing root mapping %q", ErrInvalidCatalog, key)
	}
	if value.Kind != yaml.MappingNode {
		return nil, invalidType("root "+key, "mapping", value)
	}
	return value, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Kind == yaml.ScalarNode && mapping.Content[i].Tag == "!!str" && mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func sequenceStrings(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, invalidType("value", "sequence of strings", node)
	}
	values := make([]string, len(node.Content))
	for i, item := range node.Content {
		value, err := scalarString(item)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
		values[i] = value
	}
	return values, nil
}

func scalarString(node *yaml.Node) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", invalidType("value", "string", node)
	}
	return node.Value, nil
}

func rejectDuplicateKeys(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for i := 0; i < len(node.Content); i += 2 {
			key, err := scalarString(node.Content[i])
			if err != nil {
				return fmt.Errorf("mapping key at line %d: %w", node.Content[i].Line, err)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("%w %q at line %d", ErrDuplicateKey, key, node.Content[i].Line)
			}
			seen[key] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := rejectDuplicateKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func invalidType(field, want string, node *yaml.Node) error {
	kind := yaml.Kind(0)
	tag := ""
	if node != nil {
		kind = node.Kind
		tag = node.Tag
	}
	return fmt.Errorf("%w: %s must be %s (kind %d, tag %q)", ErrInvalidCatalog, field, want, kind, tag)
}
